package main

import (
	"context"
	"crypto/subtle"
	"database/sql"
	"errors"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"
)

// Comprovante de entrega (fase 3). A entrega e o comprovante entram juntos: o mesmo
// envio grava o evento "entregue" e a foto, a assinatura ou o código conferido.
// A imagem fica no PostgreSQL (bytea), limitada a 5 MB pela migração 035.

const proofMaxBytes = 5 << 20

var proofTemperaturePattern = regexp.MustCompile(`^-?[0-9]{1,3}(\.[0-9]{1,2})?$`)

func (a *app) registerProofHandlers(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/delivery-orders/{id}/deliver", a.requireAuth("dispatch", "write", a.handleDeliverWithProof))
	mux.HandleFunc("GET /api/delivery-proofs/{id}/content", a.requireAuth("dispatch", "read", a.handleProofContent))
}

func (a *app) handleDeliverWithProof(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	// Foto pelo celular pode demorar mais que o limite geral do servidor.
	_ = http.NewResponseController(w).SetReadDeadline(time.Now().Add(90 * time.Second))
	r.Body = http.MaxBytesReader(w, r.Body, proofMaxBytes+(256<<10))
	if err := r.ParseMultipartForm(1 << 20); err != nil {
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			writeJSONError(w, http.StatusRequestEntityTooLarge, "A imagem passa de 5 MB. Tire a foto de novo com menos resolução.")
			return
		}
		writeJSONError(w, http.StatusBadRequest, "Dados inválidos. Confira os campos e tente de novo.")
		return
	}
	defer r.MultipartForm.RemoveAll()
	field := func(name string) string { return strings.TrimSpace(r.FormValue(name)) }

	opID, kind, receiver := field("operation_id"), field("kind"), field("receiver_name")
	if opID == "" {
		writeJSONError(w, http.StatusBadRequest, "Entrega sem identificador. Tente de novo.")
		return
	}
	if kind != "foto" && kind != "assinatura" && kind != "codigo" {
		writeJSONError(w, http.StatusBadRequest, "Escolha o comprovante: foto, assinatura ou código.")
		return
	}
	temperature := normalizeDecimalInput(field("temperature_c"))
	if temperature != "" && !proofTemperaturePattern.MatchString(temperature) {
		writeJSONError(w, http.StatusBadRequest, "Temperatura inválida. Use graus Celsius, como 65 ou 4,5.")
		return
	}
	lat, okLat := parseCoordinate(field("latitude"), 90)
	lng, okLng := parseCoordinate(field("longitude"), 180)
	if !okLat || !okLng || (lat == "") != (lng == "") {
		writeJSONError(w, http.StatusBadRequest, "Localização inválida. Informe latitude e longitude juntas, ou deixe as duas em branco.")
		return
	}
	occurred := time.Now().UTC()
	if v := field("occurred_at"); v != "" {
		t, err := time.Parse(time.RFC3339, v)
		if err != nil {
			writeJSONError(w, http.StatusBadRequest, "Horário da entrega inválido.")
			return
		}
		occurred = t.UTC()
	}

	var content []byte
	var contentType string
	if kind != "codigo" {
		file, _, err := r.FormFile("file")
		if err != nil {
			writeJSONError(w, http.StatusBadRequest, "Anexe a foto ou a assinatura da entrega.")
			return
		}
		content, err = io.ReadAll(io.LimitReader(file, proofMaxBytes+1))
		file.Close()
		if err != nil {
			writeJSONError(w, http.StatusBadRequest, "Não foi possível ler a imagem. Tente de novo.")
			return
		}
		if len(content) > proofMaxBytes {
			writeJSONError(w, http.StatusRequestEntityTooLarge, "A imagem passa de 5 MB. Tire a foto de novo com menos resolução.")
			return
		}
		// O tipo vem do conteúdo, não do que o navegador declarou.
		contentType = http.DetectContentType(content)
		if contentType != "image/png" && contentType != "image/jpeg" {
			writeJSONError(w, http.StatusBadRequest, "A imagem precisa ser PNG ou JPEG.")
			return
		}
	}

	ctx := r.Context()
	tx, err := a.db.BeginTx(ctx, nil)
	if err != nil {
		logisticsFail(w, http.StatusInternalServerError, "Não foi possível registrar a entrega.", "deliver begin failed", err)
		return
	}
	defer tx.Rollback()
	var current, token string
	var version int64
	err = tx.QueryRowContext(ctx, `SELECT status,tracking_token,version FROM delivery_orders WHERE id=$1 FOR UPDATE`, id).Scan(&current, &token, &version)
	if errors.Is(err, sql.ErrNoRows) {
		writeJSONError(w, http.StatusNotFound, "Entrega não encontrada.")
		return
	}
	if err != nil {
		logisticsFail(w, http.StatusInternalServerError, "Não foi possível registrar a entrega.", "deliver lookup failed", err)
		return
	}
	var existing int64
	err = tx.QueryRowContext(ctx, `SELECT id FROM delivery_events WHERE operation_id=$1`, opID).Scan(&existing)
	if err == nil {
		writeJSON(w, http.StatusOK, map[string]any{"status": current, "version": version, "replayed": true})
		return
	}
	if !errors.Is(err, sql.ErrNoRows) {
		logisticsFail(w, http.StatusInternalServerError, "Não foi possível registrar a entrega.", "deliver dedupe failed", err)
		return
	}
	if !deliveryTransitionAllowed(current, "entregue") {
		writeJSONError(w, http.StatusConflict, "Não dá para entregar um pedido que está "+current+". Registre a chegada antes.")
		return
	}
	if kind == "codigo" && subtle.ConstantTimeCompare([]byte(field("code")), []byte(token)) != 1 {
		writeJSONError(w, http.StatusBadRequest, "Código de confirmação não confere com este pedido.")
		return
	}

	eventID, newVersion, err := recordDeliveryStatus(ctx, tx, deliveryStatusChange{
		OrderID: id, Status: "entregue", OperationID: opID, Latitude: lat, Longitude: lng,
		Notes: field("notes"), UserID: userFromContext(ctx).ID, OccurredAt: occurred,
	})
	if err != nil {
		logisticsFail(w, http.StatusInternalServerError, "Não foi possível registrar a entrega.", "deliver event failed", err)
		return
	}
	var proofID int64
	err = tx.QueryRowContext(ctx, `
INSERT INTO delivery_proofs (order_id,event_id,kind,receiver_name,temperature_c,content,content_type)
VALUES ($1,$2,$3,$4,$5::numeric,$6,$7) RETURNING id`,
		id, eventID, kind, optionalText(receiver), optionalText(temperature), nilIfEmpty(content), optionalText(contentType)).Scan(&proofID)
	if err != nil {
		if strings.Contains(err.Error(), "temperature_c") {
			writeJSONError(w, http.StatusBadRequest, "Temperatura fora da faixa aceita (de -30 a 100 °C).")
			return
		}
		logisticsFail(w, http.StatusInternalServerError, "Não foi possível registrar a entrega.", "deliver proof failed", err)
		return
	}
	if err := tx.Commit(); err != nil {
		logisticsFail(w, http.StatusInternalServerError, "Não foi possível registrar a entrega.", "deliver commit failed", err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"id": eventID, "proof_id": proofID, "status": "entregue", "version": newVersion})
}

func nilIfEmpty(b []byte) any {
	if len(b) == 0 {
		return nil
	}
	return b
}

func (a *app) handleProofContent(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var content []byte
	var contentType sql.NullString
	err := a.db.QueryRowContext(r.Context(), `SELECT content,content_type FROM delivery_proofs WHERE id=$1`, id).Scan(&content, &contentType)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && !contentType.Valid) {
		writeJSONError(w, http.StatusNotFound, "Comprovante sem imagem.")
		return
	}
	if err != nil {
		logisticsFail(w, http.StatusInternalServerError, "Não foi possível carregar o comprovante.", "proof content failed", err)
		return
	}
	w.Header().Set("Content-Type", contentType.String)
	w.Header().Set("Cache-Control", "private, max-age=86400")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	_, _ = w.Write(content)
}

// deliveryProofs lista os comprovantes sem a imagem; a imagem sai por handleProofContent.
func (a *app) deliveryProofs(ctx context.Context, orderID int64) ([]map[string]any, error) {
	rows, err := a.db.QueryContext(ctx, `
SELECT id,kind,receiver_name,temperature_c::text,content_type IS NOT NULL,created_at
FROM delivery_proofs WHERE order_id=$1 ORDER BY id`, orderID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var id int64
		var kind string
		var receiver, temperature sql.NullString
		var hasImage bool
		var at time.Time
		if err := rows.Scan(&id, &kind, &receiver, &temperature, &hasImage, &at); err != nil {
			return nil, err
		}
		out = append(out, map[string]any{
			"id": id, "kind": kind, "receiver_name": nullText(receiver), "temperature_c": nullText(temperature),
			"has_image": hasImage, "created_at": at.UTC().Format(time.RFC3339),
		})
	}
	return out, rows.Err()
}
