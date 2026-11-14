package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"
	"unicode"
)

type printJob struct {
	ID         int64  `json:"id"`
	CrateID    int64  `json:"crate_id"`
	Revision   int    `json:"revision"`
	ZPL        string `json:"zpl"`
	ClaimToken string `json:"claim_token,omitempty"`
	Status     string `json:"status"`
}

func printerText(s string, max int) string {
	replacements := strings.NewReplacer("á", "a", "à", "a", "â", "a", "ã", "a", "é", "e", "ê", "e", "í", "i", "ó", "o", "ô", "o", "õ", "o", "ú", "u", "ç", "c", "Á", "A", "À", "A", "Â", "A", "Ã", "A", "É", "E", "Ê", "E", "Í", "I", "Ó", "O", "Ô", "O", "Õ", "O", "Ú", "U", "Ç", "C")
	s = replacements.Replace(s)
	var b strings.Builder
	for _, r := range s {
		if r == '^' || r == '~' || r == '\\' || r < 32 || unicode.IsControl(r) {
			continue
		}
		if r > 126 {
			continue
		}
		b.WriteRune(r)
		if b.Len() >= max {
			break
		}
	}
	return b.String()
}
func crateZPL(c crateRow) string {
	name := printerText(strings.ToUpper(c.Product), 23)
	doc := printerText(c.Document, 34)
	if doc == "" {
		doc = "NAO INFORMADO"
	}
	return fmt.Sprintf(`^XA
^CI28
^PW480
^LL480
^LH0,0
^FO20,20^A0N,43,36^FD%s^FS
^FO20,85^A0N,54,52^FD%s KG^FS
^FO20,163^A0N,26,24^FDCAIXA %d/%d^FS
^FO20,205^A0N,28,25^FD%s^FS
^FO20,258^A0N,24,21^FDENTRADA: %s^FS
^FO20,299^A0N,24,21^FDLOTE: TF-%d^FS
^FO20,340^A0N,23,19^FDNF: %s^FS
^FO315,157^BQN,2,5^FDLA,%s^FS
^FO20,425^A0N,20,20^FDTOTALFOOD - REV %d^FS
^XZ
`, name, strings.ReplaceAll(c.Weight, ".", ","), c.Position, c.Total, c.Code, c.Received, c.OriginLotID, doc, c.Code, c.Revision)
}
func enqueueCrateLabel(ctx context.Context, tx *sql.Tx, crateID, actorID int64) error {
	c, err := getCrate(ctx, tx, crateID, false)
	if err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE print_jobs SET status='CANCELLED',last_error='Substituido por etiqueta mais recente'
  WHERE crate_id=$1 AND status='PENDING'`, crateID); err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO print_jobs(crate_id,revision,zpl,created_by) VALUES($1,$2,$3,$4)
  ON CONFLICT (crate_id,revision) DO NOTHING`, crateID, c.Revision, crateZPL(c), actorID)
	return err
}
func (a *app) registerPrintHandlers(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/print/devices", a.requireAuth("warehouses", "write", a.handlePrintPair))
	mux.HandleFunc("GET /api/print/devices", a.requireAuth("warehouses", "write", a.handlePrintDevices))
	mux.HandleFunc("POST /api/print/devices/{id}/revoke", a.requireAuth("warehouses", "write", a.handlePrintRevoke))
	mux.HandleFunc("GET /api/print/jobs", a.requireAuth("movement_entry", "write", a.handlePrintJobs))
	mux.HandleFunc("POST /api/print/jobs/{id}/retry", a.requireAuth("movement_entry", "write", a.handlePrintRetry))
	mux.HandleFunc("POST /api/print/jobs/claim", a.requirePrinter(a.handlePrintClaim))
	mux.HandleFunc("POST /api/print/jobs/{id}/ack", a.requirePrinter(a.handlePrintAck))
	mux.HandleFunc("POST /api/print/jobs/{id}/fail", a.requirePrinter(a.handlePrintFail))
}

type printerCtxKey struct{}

func (a *app) requirePrinter(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		if token == "" || len(token) > 256 || token == r.Header.Get("Authorization") {
			writeJSONError(w, 401, "Impressora não autorizada.")
			return
		}
		var id int64
		err := a.db.QueryRowContext(r.Context(), `SELECT id FROM print_devices WHERE token_hash=$1 AND active=true`, hashToken(token)).Scan(&id)
		if err != nil {
			writeJSONError(w, 401, "Impressora não autorizada.")
			return
		}
		next(w, r.WithContext(context.WithValue(r.Context(), printerCtxKey{}, id)))
	}
}
func printerID(ctx context.Context) int64 { id, _ := ctx.Value(printerCtxKey{}).(int64); return id }
func (a *app) handlePrintPair(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name string `json:"name"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	req.Name = strings.TrimSpace(req.Name)
	if len(req.Name) < 3 || len(req.Name) > 60 {
		writeJSONError(w, 400, "Informe um nome de 3 a 60 caracteres.")
		return
	}
	token, err := newToken()
	if err != nil {
		writeJSONError(w, 500, "Não foi possível gerar credenciais.")
		return
	}
	var id int64
	err = a.db.QueryRowContext(r.Context(), `INSERT INTO print_devices(name,token_hash) VALUES($1,$2) RETURNING id`, req.Name, hashToken(token)).Scan(&id)
	if err != nil {
		writeJSONError(w, 409, "Já existe uma impressora com esse nome ou não foi possível cadastrar.")
		return
	}
	writeJSON(w, 201, map[string]any{"id": id, "name": req.Name, "token": token})
}
func (a *app) handlePrintDevices(w http.ResponseWriter, r *http.Request) {
	rows, err := a.db.QueryContext(r.Context(), `SELECT id,name,active FROM print_devices ORDER BY id`)
	if err != nil {
		writeJSONError(w, 500, "Falha ao consultar impressoras.")
		return
	}
	defer rows.Close()
	var devices = []map[string]any{}
	for rows.Next() {
		var id int64
		var name string
		var active bool
		if err = rows.Scan(&id, &name, &active); err != nil {
			writeJSONError(w, 500, "Falha ao consultar impressoras.")
			return
		}
		devices = append(devices, map[string]any{"id": id, "name": name, "active": active})
	}
	writeJSON(w, 200, map[string]any{"devices": devices})
}
func (a *app) handlePrintJobs(w http.ResponseWriter, r *http.Request) {
	rows, err := a.db.QueryContext(r.Context(), `SELECT j.id,j.crate_id,j.revision,j.status,c.box_position,c.box_total,
   i.name FROM print_jobs j JOIN stock_crates c ON c.id=j.crate_id JOIN product_skus s ON s.id=c.sku_id
   JOIN inventory_items i ON i.id=s.item_id ORDER BY j.id DESC LIMIT 100`)
	if err != nil {
		writeJSONError(w, 500, "Falha ao consultar fila.")
		return
	}
	defer rows.Close()
	jobs := []map[string]any{}
	for rows.Next() {
		var id, cid int64
		var rev, pos, total int
		var status, product string
		if err = rows.Scan(&id, &cid, &rev, &status, &pos, &total, &product); err != nil {
			writeJSONError(w, 500, "Falha ao consultar fila.")
			return
		}
		jobs = append(jobs, map[string]any{"id": id, "crate_id": cid, "crate_code": crateCode(cid), "revision": rev,
			"status": status, "product": product, "position": pos, "total": total})
	}
	writeJSON(w, 200, map[string]any{"jobs": jobs})
}
func (a *app) handlePrintClaim(w http.ResponseWriter, r *http.Request) {
	id := printerID(r.Context())
	tx, err := a.db.BeginTx(r.Context(), nil)
	if err != nil {
		writeJSONError(w, 503, "Fila indisponível.")
		return
	}
	defer tx.Rollback()
	// A job that lost its confirmation is not blindly sent again.
	_, err = tx.ExecContext(r.Context(), `UPDATE print_jobs SET status='UNCERTAIN',
  last_error='Tempo de confirmação esgotado' WHERE status='CLAIMED' AND claim_expires_at<now()`)
	if err != nil {
		writeJSONError(w, 503, "Fila indisponível.")
		return
	}
	token, err := newToken()
	if err != nil {
		writeJSONError(w, 503, "Fila indisponível.")
		return
	}
	var job printJob
	err = tx.QueryRowContext(r.Context(), `WITH next_job AS (
   SELECT id FROM print_jobs WHERE status='PENDING' AND (device_id IS NULL OR device_id=$1)
   ORDER BY id FOR UPDATE SKIP LOCKED LIMIT 1
  )
  UPDATE print_jobs j SET status='CLAIMED',device_id=$1,claim_token_hash=$2,claimed_at=now(),
    claim_expires_at=now()+interval '2 minutes'
  FROM next_job n WHERE j.id=n.id RETURNING j.id,j.crate_id,j.revision,j.zpl,j.status`, id, hashToken(token)).
		Scan(&job.ID, &job.CrateID, &job.Revision, &job.ZPL, &job.Status)
	if errors.Is(err, sql.ErrNoRows) {
		if err = tx.Commit(); err != nil {
			writeJSONError(w, 503, "Fila indisponível.")
			return
		}
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if err != nil {
		log.Printf("printer claim db error: %v", err)
		writeJSONError(w, 503, "Fila indisponível.")
		return
	}
	if _, err = tx.ExecContext(r.Context(), `UPDATE print_devices SET last_seen_at=now() WHERE id=$1`, id); err != nil {
		writeJSONError(w, 503, "Fila indisponível.")
		return
	}
	if err = tx.Commit(); err != nil {
		writeJSONError(w, 503, "Fila indisponível.")
		return
	}
	if r.URL.Query().Get("format") == "zpl" {
		w.Header().Set("Content-Type", "application/vnd.zebra-zpl")
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Print-Job-ID", strconv.FormatInt(job.ID, 10))
		w.Header().Set("X-Print-Claim", token)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(job.ZPL))
		return
	}
	job.ClaimToken = token
	writeJSON(w, 200, job)
}

type printAckInput struct {
	ClaimToken string `json:"claim_token"`
	Error      string `json:"error,omitempty"`
}

func (a *app) handlePrintAck(w http.ResponseWriter, r *http.Request)  { a.completePrintJob(w, r, false) }
func (a *app) handlePrintFail(w http.ResponseWriter, r *http.Request) { a.completePrintJob(w, r, true) }
func (a *app) completePrintJob(w http.ResponseWriter, r *http.Request, failed bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		writeJSONError(w, 400, "Trabalho inválido.")
		return
	}
	var req printAckInput
	if !decodeJSON(w, r, &req) {
		return
	}
	if len(req.ClaimToken) < 32 || len(req.ClaimToken) > 256 || len(req.Error) > 500 {
		writeJSONError(w, 400, "Confirmação inválida.")
		return
	}
	status := "SENT"
	if failed {
		status = "FAILED"
	}
	var updated string
	err = a.db.QueryRowContext(r.Context(), `UPDATE print_jobs SET status=$1,sent_at=CASE WHEN $1='SENT' THEN now() ELSE NULL END,
    last_error=CASE WHEN $1='FAILED' THEN $5 ELSE NULL END
    WHERE id=$2 AND device_id=$3 AND claim_token_hash=$4 AND (status='CLAIMED' OR status=$1)
    RETURNING status`, status, id, printerID(r.Context()), hashToken(req.ClaimToken), req.Error).Scan(&updated)
	if errors.Is(err, sql.ErrNoRows) {
		writeJSONError(w, 409, "Trabalho não reservado por esta impressora ou já encerrado.")
		return
	}
	if err != nil {
		writeJSONError(w, 503, "Não foi possível confirmar o trabalho.")
		return
	}
	writeJSON(w, 200, map[string]any{"id": id, "status": updated})
}
func (a *app) handlePrintRetry(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		writeJSONError(w, 400, "Trabalho inválido.")
		return
	}
	result, err := a.db.ExecContext(r.Context(), `UPDATE print_jobs SET status='PENDING',claim_token_hash=NULL,claimed_at=NULL,claim_expires_at=NULL,last_error=NULL
  WHERE id=$1 AND status IN ('UNCERTAIN','FAILED') AND revision=(SELECT revision FROM stock_crates WHERE id=crate_id)`, id)
	if err != nil {
		writeJSONError(w, 500, "Falha ao reenfileirar.")
		return
	}
	rows, _ := result.RowsAffected()
	if rows == 0 {
		writeJSONError(w, 409, "Só é possível reenviar um trabalho com falha ou confirmação incerta, ainda atual.")
		return
	}
	writeJSON(w, 200, map[string]any{"id": id, "status": "PENDING"})
}

func (a *app) handlePrintRevoke(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		writeJSONError(w, 400, "Impressora inválida.")
		return
	}
	result, err := a.db.ExecContext(r.Context(), `UPDATE print_devices SET active=false WHERE id=$1`, id)
	if err != nil {
		writeJSONError(w, 500, "Não foi possível revogar a impressora.")
		return
	}
	n, _ := result.RowsAffected()
	if n == 0 {
		writeJSONError(w, 404, "Impressora não encontrada.")
		return
	}
	writeJSON(w, 200, map[string]any{"id": id, "status": "revoked"})
}
