package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// Recebimento de requisições. A cozinha confere cada entrega e informa quanto
// recebeu; se for menos que o entregue, a diferença fica como divergência até a
// gerência registrar o recebimento do restante (conciliação). Nenhuma destas
// operações movimenta estoque: a saída é a da entrega.

type materialReceiveInput struct {
	OperationID      string `json:"operation_id"`
	ReceivedQuantity string `json:"received_quantity"`
	Reason           string `json:"reason"`
}
type materialReconcileInput struct {
	OperationID string `json:"operation_id"`
	Quantity    string `json:"quantity"`
	Notes       string `json:"notes"`
}

// matReceiptDone: a requisição já foi toda entregue e conferida.
func matReceiptDone(status string) bool {
	return status == "RECEBIDA" || status == "RECEBIDA_COM_DIVERGENCIA"
}

// matDecodeOptional aceita corpo vazio como {} (a confirmação integral antiga não mandava nada).
func matDecodeOptional(w http.ResponseWriter, r *http.Request, v any) bool {
	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 1<<20))
	r.Body.Close()
	if err != nil {
		writeJSONError(w, 400, "Dados inválidos.")
		return false
	}
	if len(bytes.TrimSpace(raw)) == 0 {
		return true
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if d.Decode(v) != nil {
		writeJSONError(w, 400, "Dados inválidos.")
		return false
	}
	var extra any
	if !errors.Is(d.Decode(&extra), io.EOF) {
		writeJSONError(w, 400, "Envie um único documento JSON.")
		return false
	}
	return true
}

type matDeliveryState struct {
	RequestID int64
	Received  bool
	Quantity  string // entregue
	Got       string // recebido até agora ("" se ainda não conferida)
	Pending   string // entregue - recebido
}

func matLockDelivery(ctx context.Context, tx *sql.Tx, did int64) (matDeliveryState, error) {
	var s matDeliveryState
	err := tx.QueryRowContext(ctx, `SELECT request_id,received_at IS NOT NULL,quantity::text,COALESCE(received_quantity::text,''),
  (quantity-COALESCE(received_quantity,0))::numeric(14,3)::text FROM material_request_deliveries WHERE id=$1 FOR UPDATE`, did).
		Scan(&s.RequestID, &s.Received, &s.Quantity, &s.Got, &s.Pending)
	if errors.Is(err, sql.ErrNoRows) {
		return s, matMissing()
	}
	return s, err
}

func (a *app) handleMaterialReceive(w http.ResponseWriter, r *http.Request) {
	id, ok := matPath(w, r, "id")
	if !ok {
		return
	}
	did, ok := matPath(w, r, "delivery_id")
	if !ok {
		return
	}
	var req materialReceiveInput
	if !matDecodeOptional(w, r, &req) {
		return
	}
	req.ReceivedQuantity = strings.TrimSpace(req.ReceivedQuantity)
	req.Reason = strings.TrimSpace(req.Reason)
	if req.ReceivedQuantity != "" && !materialQtyPattern.MatchString(req.ReceivedQuantity) {
		writeJSONError(w, 400, "Informe a quantidade recebida, com até 3 casas decimais.")
		return
	}
	if len(req.Reason) > 500 {
		writeJSONError(w, 400, "O motivo da diferença aceita até 500 caracteres.")
		return
	}
	ctx := r.Context()
	actor := userFromContext(ctx).ID
	manager, err := a.hasPermission(ctx, actor, "material_fulfillment", "write")
	if err != nil {
		a.matRespond(w, err)
		return
	}
	tx, err := a.db.BeginTx(ctx, nil)
	if err != nil {
		a.matRespond(w, err)
		return
	}
	defer tx.Rollback()
	status, owner, _, err := matLocked(ctx, tx, id)
	if err != nil {
		a.matRespond(w, err)
		return
	}
	if actor != owner && !manager {
		a.matRespond(w, matErr(403, "Somente o solicitante ou gerente pode confirmar recebimento."))
		return
	}
	if status == "CANCELADA" {
		a.matRespond(w, matConflict("Requisição cancelada."))
		return
	}
	d, err := matLockDelivery(ctx, tx, did)
	if err != nil {
		a.matRespond(w, err)
		return
	}
	if d.RequestID != id {
		a.matRespond(w, matMissing())
		return
	}
	want := req.ReceivedQuantity
	if want == "" {
		want = d.Quantity // sem quantidade: recebimento integral
	}
	if d.Received {
		// Repetir a mesma conferência não muda nada; uma conferência diferente é recusada.
		var first string
		if err = tx.QueryRowContext(ctx, "SELECT quantity::text FROM material_request_receipts WHERE delivery_id=$1 AND kind='RECEBIMENTO'", did).Scan(&first); err != nil {
			a.matRespond(w, err)
			return
		}
		var same bool
		if err = tx.QueryRowContext(ctx, "SELECT $1::numeric=$2::numeric", first, want).Scan(&same); err != nil {
			a.matRespond(w, err)
			return
		}
		if req.ReceivedQuantity != "" && !same {
			a.matRespond(w, matConflict("Esta entrega já foi conferida com outra quantidade. Atualize a página."))
			return
		}
		writeJSON(w, 200, map[string]any{"id": did, "status": "confirmed", "received_quantity": d.Got, "pending_divergence": d.Pending})
		return
	}
	var fits, full bool
	var missing string
	if err = tx.QueryRowContext(ctx, "SELECT $1::numeric<=$2::numeric,$1::numeric=$2::numeric,($2::numeric-$1::numeric)::numeric(14,3)::text", want, d.Quantity).Scan(&fits, &full, &missing); err != nil {
		a.matRespond(w, err)
		return
	}
	if !fits {
		a.matRespond(w, matBad("A quantidade recebida não pode ser maior que a entregue ("+d.Quantity+")."))
		return
	}
	if !full && req.Reason == "" {
		a.matRespond(w, matBad("Informe o motivo da diferença entre o entregue e o recebido."))
		return
	}
	reason := req.Reason
	if full {
		reason = ""
	}
	if _, err = tx.ExecContext(ctx, "UPDATE material_request_deliveries SET received_by=$2,received_at=now(),received_quantity=$3,divergence_reason=$4 WHERE id=$1", did, actor, want, reason); err != nil {
		a.matRespond(w, err)
		return
	}
	if _, err = tx.ExecContext(ctx, "INSERT INTO material_request_receipts(delivery_id,kind,quantity,notes,actor_id) VALUES($1,'RECEBIMENTO',$2,$3,$4)", did, want, reason, actor); err != nil {
		a.matRespond(w, err)
		return
	}
	if full {
		err = matEvent(ctx, tx, id, actor, "RECEBIDA", fmt.Sprintf("Entrega %d conferida", did))
	} else {
		err = matEvent(ctx, tx, id, actor, "DIVERGENCIA", fmt.Sprintf("Entrega %d: recebido %s de %s, faltam %s. Motivo: %s", did, want, d.Quantity, missing, reason))
	}
	if err != nil {
		a.matRespond(w, err)
		return
	}
	if err = matRefresh(ctx, tx, id); err != nil {
		a.matRespond(w, err)
		return
	}
	if err = tx.Commit(); err != nil {
		a.matRespond(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"id": did, "status": "confirmed", "received_quantity": want, "pending_divergence": missing})
}

// handleMaterialReconcile registra que parte (ou todo) do que faltava em uma entrega
// foi recebida depois. Só quem tem material_divergences/write (Diretor e Gerente).
func (a *app) handleMaterialReconcile(w http.ResponseWriter, r *http.Request) {
	id, ok := matPath(w, r, "id")
	if !ok {
		return
	}
	did, ok := matPath(w, r, "delivery_id")
	if !ok {
		return
	}
	var req materialReconcileInput
	if !matDecode(w, r, &req) {
		return
	}
	req.OperationID = strings.TrimSpace(req.OperationID)
	req.Quantity = strings.TrimSpace(req.Quantity)
	req.Notes = strings.TrimSpace(req.Notes)
	if !matOp(req.OperationID) || !matQty(req.Quantity) || len(req.Notes) > 500 {
		writeJSONError(w, 400, "Informe a quantidade recebida depois, maior que zero e com até 3 casas decimais.")
		return
	}
	h := matHash(struct {
		materialReconcileInput
		Request, Delivery int64
	}{req, id, did})
	ctx := r.Context()
	actor := userFromContext(ctx).ID
	tx, err := a.db.BeginTx(ctx, nil)
	if err != nil {
		a.matRespond(w, err)
		return
	}
	defer tx.Rollback()
	status, _, _, err := matLocked(ctx, tx, id)
	if err != nil {
		a.matRespond(w, err)
		return
	}
	d, err := matLockDelivery(ctx, tx, did)
	if err != nil {
		a.matRespond(w, err)
		return
	}
	if d.RequestID != id {
		a.matRespond(w, matMissing())
		return
	}
	var oldHash string
	var oldActor int64
	err = tx.QueryRowContext(ctx, "SELECT request_hash,actor_id FROM material_request_receipts WHERE operation_id=$1", req.OperationID).Scan(&oldHash, &oldActor)
	if err == nil {
		if oldHash != h || oldActor != actor {
			a.matRespond(w, matConflict("Este envio já foi utilizado com outros dados."))
			return
		}
		writeJSON(w, 200, map[string]any{"id": did, "status": "reconciled", "received_quantity": d.Got, "pending_divergence": d.Pending})
		return
	}
	if !errors.Is(err, sql.ErrNoRows) {
		a.matRespond(w, err)
		return
	}
	if status == "CANCELADA" {
		a.matRespond(w, matConflict("Requisição cancelada."))
		return
	}
	if !d.Received {
		a.matRespond(w, matConflict("A cozinha ainda não conferiu esta entrega."))
		return
	}
	var fits bool
	var left string
	if err = tx.QueryRowContext(ctx, "SELECT $1::numeric<=$2::numeric,($2::numeric-$1::numeric)::numeric(14,3)::text", req.Quantity, d.Pending).Scan(&fits, &left); err != nil {
		a.matRespond(w, err)
		return
	}
	if !fits {
		a.matRespond(w, matConflict("Só faltam "+d.Pending+" para conciliar nesta entrega. Atualize a página."))
		return
	}
	var got string
	if err = tx.QueryRowContext(ctx, "UPDATE material_request_deliveries SET received_quantity=received_quantity+$2::numeric WHERE id=$1 RETURNING received_quantity::text", did, req.Quantity).Scan(&got); err != nil {
		a.matRespond(w, err)
		return
	}
	if _, err = tx.ExecContext(ctx, "INSERT INTO material_request_receipts(delivery_id,kind,quantity,notes,operation_id,request_hash,actor_id) VALUES($1,'CONCILIACAO',$2,$3,$4,$5,$6)", did, req.Quantity, req.Notes, req.OperationID, h, actor); err != nil {
		a.matRespond(w, err)
		return
	}
	detail := fmt.Sprintf("Entrega %d: recebimento de %s registrado depois, faltam %s", did, req.Quantity, left)
	if req.Notes != "" {
		detail += ". " + req.Notes
	}
	if err = matEvent(ctx, tx, id, actor, "CONCILIADA", detail); err != nil {
		a.matRespond(w, err)
		return
	}
	if err = matRefresh(ctx, tx, id); err != nil {
		a.matRespond(w, err)
		return
	}
	if err = tx.Commit(); err != nil {
		a.matRespond(w, err)
		return
	}
	writeJSON(w, 201, map[string]any{"id": did, "status": "reconciled", "received_quantity": got, "pending_divergence": left})
}
