package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"testing"
)

// printQueueFixture recebe uma caixa (que enfileira a etiqueta) e pareia uma impressora.
type printQueueFixture struct {
	a          *app
	userCtx    context.Context
	printerCtx context.Context
	printerID  int64
	crateID    int64
	user       int64
}

func newPrintQueueFixture(t *testing.T, name string) printQueueFixture {
	t.Helper()
	db := openTestDatabase(t)
	if err := runMigrations(context.Background(), db, filepath.Join("..", "..", "migrations")); err != nil {
		t.Fatal(err)
	}
	a := &app{db: db}
	user := crateTestUser(t, db)
	sku := crateTestSKU(t, db)
	var wh int64
	if err := db.QueryRow("SELECT id FROM warehouses WHERE name='VR'").Scan(&wh); err != nil {
		t.Fatal(err)
	}
	userCtx := context.WithValue(context.Background(), authCtxKey, authUser{ID: user})
	rec := crateTestRequest(t, a.handleCrateReceive, "POST", "/api/crates/receive", crateReceiveRequest{
		OperationID: "followup-receive-" + name, SKUID: sku, WarehouseID: wh, UnitCost: "3.00", Weights: []string{"10.000"}}, userCtx)
	if rec.Code != 200 {
		t.Fatalf("receive: %d %s", rec.Code, rec.Body.String())
	}
	var receipt map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &receipt); err != nil {
		t.Fatal(err)
	}
	crateID := int64(receipt["crates"].([]any)[0].(map[string]any)["id"].(float64))
	pair := crateTestRequest(t, a.handlePrintPair, "POST", "/api/print/devices", map[string]string{"name": "printer-" + name}, userCtx)
	if pair.Code != 201 {
		t.Fatalf("pair: %d %s", pair.Code, pair.Body.String())
	}
	var paired map[string]any
	if err := json.Unmarshal(pair.Body.Bytes(), &paired); err != nil {
		t.Fatal(err)
	}
	printerID := int64(paired["id"].(float64))
	return printQueueFixture{a: a, userCtx: userCtx, printerCtx: context.WithValue(context.Background(), printerCtxKey{}, printerID),
		printerID: printerID, crateID: crateID, user: user}
}

func (f printQueueFixture) claim(t *testing.T) (int64, string, int) {
	t.Helper()
	rr := crateTestRequest(t, f.a.handlePrintClaim, "POST", "/api/print/jobs/claim", map[string]any{}, f.printerCtx)
	if rr.Code == http.StatusNoContent {
		return 0, "", rr.Code
	}
	if rr.Code != 200 {
		t.Fatalf("claim: %d %s", rr.Code, rr.Body.String())
	}
	var out struct {
		ID         int64  `json:"id"`
		ClaimToken string `json:"claim_token"`
		Revision   int    `json:"revision"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if out.ID == 0 || len(out.ClaimToken) < 32 {
		t.Fatalf("claim without job or token: %s", rr.Body.String())
	}
	return out.ID, out.ClaimToken, out.Revision
}

func (f printQueueFixture) confirm(t *testing.T, method string, jobID int64, token string, want int) {
	t.Helper()
	req := httptest.NewRequest("POST", fmt.Sprintf("/api/print/jobs/%d/%s", jobID, method),
		bytes.NewReader(mustJSON(t, printAckInput{ClaimToken: token, Error: "Impressora sem papel"}))).WithContext(f.printerCtx)
	req.SetPathValue("id", strconv.FormatInt(jobID, 10))
	rr := httptest.NewRecorder()
	switch method {
	case "ack":
		f.a.handlePrintAck(rr, req)
	case "fail":
		f.a.handlePrintFail(rr, req)
	default:
		t.Fatalf("unknown method %s", method)
	}
	if rr.Code != want {
		t.Fatalf("%s job %d: %d, wanted %d: %s", method, jobID, rr.Code, want, rr.Body.String())
	}
}

func (f printQueueFixture) status(t *testing.T, jobID int64) string {
	t.Helper()
	var s string
	if err := f.a.db.QueryRow("SELECT status FROM print_jobs WHERE id=$1", jobID).Scan(&s); err != nil {
		t.Fatal(err)
	}
	return s
}

// 2.3: no Render gratuito não há processo em segundo plano. Uma reserva vencida
// precisa aparecer como incerta na fila mesmo que nenhuma impressora busque de novo.
func TestPrintQueueListExpiresStaleClaimWithoutPrinter(t *testing.T) {
	f := newPrintQueueFixture(t, "expire-list")
	job, _, _ := f.claim(t)
	if _, err := f.a.db.Exec("UPDATE print_jobs SET claim_expires_at=now()-interval '1 second' WHERE id=$1", job); err != nil {
		t.Fatal(err)
	}
	rr := crateTestRequest(t, f.a.handlePrintJobs, "GET", "/api/print/jobs", nil, f.userCtx)
	if rr.Code != 200 {
		t.Fatalf("list: %d %s", rr.Code, rr.Body.String())
	}
	var out struct {
		Jobs []struct {
			ID     int64  `json:"id"`
			Status string `json:"status"`
		} `json:"jobs"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, j := range out.Jobs {
		if j.ID == job {
			found = true
			if j.Status != "UNCERTAIN" {
				t.Fatalf("expired claim listed as %s, wanted UNCERTAIN", j.Status)
			}
		}
	}
	if !found {
		t.Fatalf("job %d missing from queue list", job)
	}
}

// 2.3: a lista de impressoras também mostra o teste vencido como sem confirmação.
func TestPrintDeviceListExpiresStaleTestClaim(t *testing.T) {
	f := newPrintQueueFixture(t, "expire-devices")
	// Tira a etiqueta da caixa da frente, para a busca pegar o teste.
	if _, err := f.a.db.Exec("UPDATE print_jobs SET status='CANCELLED' WHERE crate_id=$1", f.crateID); err != nil {
		t.Fatal(err)
	}
	test := httptest.NewRequest("POST", fmt.Sprintf("/api/print/devices/%d/test", f.printerID), nil).WithContext(f.userCtx)
	test.SetPathValue("id", strconv.FormatInt(f.printerID, 10))
	tr := httptest.NewRecorder()
	f.a.handlePrintTest(tr, test)
	if tr.Code != 201 && tr.Code != 200 {
		t.Fatalf("test label: %d %s", tr.Code, tr.Body.String())
	}
	job, _, _ := f.claim(t)
	if _, err := f.a.db.Exec("UPDATE print_jobs SET claim_expires_at=now()-interval '1 second' WHERE id=$1", job); err != nil {
		t.Fatal(err)
	}
	devices := f.listDevices(t)
	if devices[0]["last_test_status"] != "UNCERTAIN" {
		t.Fatalf("expired test listed as %v, wanted UNCERTAIN", devices[0]["last_test_status"])
	}
}

func (f printQueueFixture) listDevices(t *testing.T) []map[string]any {
	t.Helper()
	rr := crateTestRequest(t, f.a.handlePrintDevices, "GET", "/api/print/devices", nil, f.userCtx)
	if rr.Code != 200 {
		t.Fatalf("devices: %d %s", rr.Code, rr.Body.String())
	}
	var out struct {
		Devices []map[string]any `json:"devices"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	for _, d := range out.Devices {
		if int64(d["id"].(float64)) == f.printerID {
			return []map[string]any{d}
		}
	}
	t.Fatalf("printer %d missing from list", f.printerID)
	return nil
}

// 2.7: "visto por último" vale para toda busca, inclusive com a fila vazia,
// e aparece na lista de impressoras.
func TestPrintClaimUpdatesLastSeenOnEmptyQueue(t *testing.T) {
	f := newPrintQueueFixture(t, "last-seen")
	if _, err := f.a.db.Exec("UPDATE print_jobs SET status='CANCELLED' WHERE crate_id=$1", f.crateID); err != nil {
		t.Fatal(err)
	}
	if d := f.listDevices(t)[0]; d["last_seen_at"] != nil {
		t.Fatalf("new printer already seen: %v", d["last_seen_at"])
	}
	if _, _, code := f.claim(t); code != http.StatusNoContent {
		t.Fatalf("empty queue claim: %d", code)
	}
	var seen bool
	if err := f.a.db.QueryRow("SELECT last_seen_at IS NOT NULL FROM print_devices WHERE id=$1", f.printerID).Scan(&seen); err != nil || !seen {
		t.Fatalf("last_seen_at not updated on empty queue: seen=%v err=%v", seen, err)
	}
	if d := f.listDevices(t)[0]; d["last_seen_at"] == nil || d["last_seen_at"] == "" {
		t.Fatalf("last_seen_at missing from device list: %v", d)
	}
}

// 2.8: falha informada pelo ESP, reenvio manual e nova reserva com outro token;
// a confirmação com o token antigo é recusada.
func TestPrintFailureThenManualRetry(t *testing.T) {
	f := newPrintQueueFixture(t, "fail-retry")
	job, token, _ := f.claim(t)
	f.confirm(t, "fail", job, token, http.StatusOK)
	if s := f.status(t, job); s != "FAILED" {
		t.Fatalf("after fail: %s", s)
	}
	if _, _, code := f.claim(t); code != http.StatusNoContent {
		t.Fatalf("failed job must not be claimed again automatically: %d", code)
	}
	retry := httptest.NewRequest("POST", fmt.Sprintf("/api/print/jobs/%d/retry", job), bytes.NewReader([]byte("{}"))).WithContext(f.userCtx)
	retry.SetPathValue("id", strconv.FormatInt(job, 10))
	rr := httptest.NewRecorder()
	f.a.handlePrintRetry(rr, retry)
	if rr.Code != 200 {
		t.Fatalf("retry: %d %s", rr.Code, rr.Body.String())
	}
	again, token2, _ := f.claim(t)
	if again != job || token2 == token {
		t.Fatalf("retry must reclaim the same job with a new token: job=%d", again)
	}
	f.confirm(t, "ack", job, token, http.StatusConflict)
	f.confirm(t, "ack", job, token2, http.StatusOK)
	if s := f.status(t, job); s != "SENT" {
		t.Fatalf("after ack: %s", s)
	}
}

// 2.8: nova revisão da caixa (ex.: repesagem) cancela a etiqueta pendente
// antiga; a impressora recebe só a revisão nova, e a antiga não pode ser reenviada.
func TestPrintNewRevisionCancelsPendingLabel(t *testing.T) {
	f := newPrintQueueFixture(t, "revision")
	var oldJob int64
	if err := f.a.db.QueryRow("SELECT id FROM print_jobs WHERE crate_id=$1 AND status='PENDING'", f.crateID).Scan(&oldJob); err != nil {
		t.Fatal(err)
	}
	tx, err := f.a.db.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec("UPDATE stock_crates SET revision=revision+1 WHERE id=$1", f.crateID); err != nil {
		tx.Rollback()
		t.Fatal(err)
	}
	if err = enqueueCrateLabel(context.Background(), tx, f.crateID, f.user); err != nil {
		tx.Rollback()
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if s := f.status(t, oldJob); s != "CANCELLED" {
		t.Fatalf("old pending label: %s, wanted CANCELLED", s)
	}
	job, token, revision := f.claim(t)
	if job == oldJob || revision != 2 {
		t.Fatalf("claim got job %d revision %d, wanted the new revision", job, revision)
	}
	f.confirm(t, "ack", job, token, http.StatusOK)
	if _, _, code := f.claim(t); code != http.StatusNoContent {
		t.Fatalf("cancelled label must not be printed: %d", code)
	}
	retry := httptest.NewRequest("POST", fmt.Sprintf("/api/print/jobs/%d/retry", oldJob), bytes.NewReader([]byte("{}"))).WithContext(f.userCtx)
	retry.SetPathValue("id", strconv.FormatInt(oldJob, 10))
	rr := httptest.NewRecorder()
	f.a.handlePrintRetry(rr, retry)
	if rr.Code != http.StatusConflict {
		t.Fatalf("retry of a cancelled old revision: %d", rr.Code)
	}
}
