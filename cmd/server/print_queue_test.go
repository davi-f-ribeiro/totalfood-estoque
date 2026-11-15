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

// A cold Render wake happens before claim. After claim the ESP can take longer
// than the lease; its authentic ACK must still reconcile if no manual retry took place.
func TestPrintLeaseLateACKAndManualRetry(t *testing.T) {
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
	receive := crateReceiveRequest{OperationID: "test-lease-receive-1", SKUID: sku, WarehouseID: wh,
		UnitCost: "3.00", Weights: []string{"10.000"}}
	rec := crateTestRequest(t, a.handleCrateReceive, "POST", "/api/crates/receive", receive, userCtx)
	if rec.Code != 200 {
		t.Fatalf("receive: %d %s", rec.Code, rec.Body.String())
	}
	var receipt map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &receipt); err != nil {
		t.Fatal(err)
	}
	crateID := int64(receipt["crates"].([]any)[0].(map[string]any)["id"].(float64))

	pair := crateTestRequest(t, a.handlePrintPair, "POST", "/api/print/devices",
		map[string]string{"name": "printer-lease-test"}, userCtx)
	if pair.Code != 201 {
		t.Fatalf("pair: %d %s", pair.Code, pair.Body.String())
	}
	var paired map[string]any
	if err := json.Unmarshal(pair.Body.Bytes(), &paired); err != nil {
		t.Fatal(err)
	}
	printerID := int64(paired["id"].(float64))
	printerCtx := context.WithValue(context.Background(), printerCtxKey{}, printerID)

	// Authentication on the cloud side is independent of an end-user session.
	noAuth := crateTestRequest(t, a.requirePrinter(a.handlePrintClaim), "POST",
		"/api/print/jobs/claim", map[string]any{}, context.Background())
	if noAuth.Code != http.StatusUnauthorized {
		t.Fatalf("printer without token: %d", noAuth.Code)
	}

	claimZPL := func() (int64, string) {
		t.Helper()
		req := httptest.NewRequest("POST", "/api/print/jobs/claim?format=zpl", bytes.NewReader([]byte("{}"))).WithContext(printerCtx)
		rr := httptest.NewRecorder()
		a.handlePrintClaim(rr, req)
		if rr.Code != 200 {
			t.Fatalf("claim: %d %s", rr.Code, rr.Body.String())
		}
		if rr.Header().Get("Content-Type") != "application/vnd.zebra-zpl" || !bytes.HasPrefix(rr.Body.Bytes(), []byte("^XA")) {
			t.Fatalf("unexpected ZPL claim result")
		}
		jobID, err := strconv.ParseInt(rr.Header().Get("X-Print-Job-ID"), 10, 64)
		if err != nil {
			t.Fatal(err)
		}
		token := rr.Header().Get("X-Print-Claim")
		if len(token) < 32 {
			t.Fatal("missing claim token")
		}
		var seconds float64
		if err := db.QueryRow("SELECT EXTRACT(EPOCH FROM claim_expires_at - now())::float8 FROM print_jobs WHERE id=$1", jobID).Scan(&seconds); err != nil {
			t.Fatal(err)
		}
		if seconds < 240 {
			t.Fatalf("lease too short: %.1f seconds", seconds)
		}
		return jobID, token
	}
	confirm := func(method string, jobID int64, claimToken string, want int) {
		t.Helper()
		path := fmt.Sprintf("/api/print/jobs/%d/%s", jobID, method)
		req := httptest.NewRequest("POST", path, bytes.NewReader(mustJSON(t, printAckInput{ClaimToken: claimToken}))).WithContext(printerCtx)
		req.SetPathValue("id", strconv.FormatInt(jobID, 10))
		rr := httptest.NewRecorder()
		switch method {
		case "ack":
			a.handlePrintAck(rr, req)
		case "fail":
			a.handlePrintFail(rr, req)
		case "uncertain":
			a.handlePrintUncertain(rr, req)
		default:
			t.Fatalf("unknown method %s", method)
		}
		if rr.Code != want {
			t.Fatalf("%s for job %d returned %d, wanted %d: %s", method, jobID, rr.Code, want, rr.Body.String())
		}
	}

	job1, token1 := claimZPL()
	if _, err := db.Exec("UPDATE print_jobs SET claim_expires_at=now()-interval '1 second' WHERE id=$1", job1); err != nil {
		t.Fatal(err)
	}
	noMore := crateTestRequest(t, a.handlePrintClaim, "POST", "/api/print/jobs/claim", map[string]any{}, printerCtx)
	if noMore.Code != http.StatusNoContent {
		t.Fatalf("lease swept but no duplicate is allowed; got %d", noMore.Code)
	}
	var state string
	if err := db.QueryRow("SELECT status FROM print_jobs WHERE id=$1", job1).Scan(&state); err != nil || state != "UNCERTAIN" {
		t.Fatalf("expired job should remain uncertain: status=%s err=%v", state, err)
	}
	// A real USB send can be confirmed late, with the original claim token.
	confirm("ack", job1, token1, http.StatusOK)
	confirm("ack", job1, token1, http.StatusOK)
	if err := db.QueryRow("SELECT status FROM print_jobs WHERE id=$1", job1).Scan(&state); err != nil || state != "SENT" {
		t.Fatalf("late acknowledgement not reconciled: %s %v", state, err)
	}

	// New revision of the same crate, simulating a later reweigh.
	tx, err := db.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec("UPDATE stock_crates SET revision=revision+1 WHERE id=$1", crateID); err != nil {
		tx.Rollback()
		t.Fatal(err)
	}
	if err = enqueueCrateLabel(context.Background(), tx, crateID, user); err != nil {
		tx.Rollback()
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}

	job2, token2 := claimZPL()
	// If the ESP reboots during USB sending, it explicitly reports uncertainty.
	confirm("uncertain", job2, token2, http.StatusOK)
	confirm("uncertain", job2, token2, http.StatusOK)
	if err := db.QueryRow("SELECT status FROM print_jobs WHERE id=$1", job2).Scan(&state); err != nil || state != "UNCERTAIN" {
		t.Fatalf("uncertain job state=%s err=%v", state, err)
	}
	stillNoAutomaticRetry := crateTestRequest(t, a.handlePrintClaim, "POST", "/api/print/jobs/claim", map[string]any{}, printerCtx)
	if stillNoAutomaticRetry.Code != http.StatusNoContent {
		t.Fatalf("uncertain job must not be automatically claimed again: %d", stillNoAutomaticRetry.Code)
	}
	retryReq := httptest.NewRequest("POST", fmt.Sprintf("/api/print/jobs/%d/retry", job2), bytes.NewReader([]byte("{}"))).WithContext(userCtx)
	retryReq.SetPathValue("id", strconv.FormatInt(job2, 10))
	retryRes := httptest.NewRecorder()
	a.handlePrintRetry(retryRes, retryReq)
	if retryRes.Code != 200 {
		t.Fatalf("manual retry failed %d %s", retryRes.Code, retryRes.Body.String())
	}
	confirm("ack", job2, token2, http.StatusConflict) // previous token invalidated
	retryID, retryToken := claimZPL()
	if retryID != job2 || retryToken == token2 {
		t.Fatal("manual retry must establish a new claim token")
	}
	confirm("ack", retryID, retryToken, http.StatusOK)
}
