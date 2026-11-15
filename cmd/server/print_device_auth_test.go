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

// The ESP32 discards a pending confirmation only on final answers (400, 401,
// 409). These checks go through the real router and requirePrinter so that the
// contract the firmware depends on is protected end to end.
func TestPrinterFinalAnswersForPendingConfirmation(t *testing.T) {
	db := openTestDatabase(t)
	if err := runMigrations(context.Background(), db, filepath.Join("..", "..", "migrations")); err != nil {
		t.Fatal(err)
	}
	a := &app{db: db}
	mux := http.NewServeMux()
	a.registerPrintHandlers(mux)

	user := crateTestUser(t, db)
	sku := crateTestSKU(t, db)
	var wh int64
	if err := db.QueryRow("SELECT id FROM warehouses WHERE name='VR'").Scan(&wh); err != nil {
		t.Fatal(err)
	}
	userCtx := context.WithValue(context.Background(), authCtxKey, authUser{ID: user})
	rec := crateTestRequest(t, a.handleCrateReceive, "POST", "/api/crates/receive", crateReceiveRequest{
		OperationID: "test-printer-final-receive", SKUID: sku, WarehouseID: wh, UnitCost: "3.00", Weights: []string{"8.000"},
	}, userCtx)
	if rec.Code != 200 {
		t.Fatalf("receive: %d %s", rec.Code, rec.Body.String())
	}
	pair := crateTestRequest(t, a.handlePrintPair, "POST", "/api/print/devices",
		map[string]string{"name": "printer-final-answers"}, userCtx)
	if pair.Code != 201 {
		t.Fatalf("pair: %d %s", pair.Code, pair.Body.String())
	}
	var paired struct {
		ID    int64  `json:"id"`
		Token string `json:"token"`
	}
	if err := json.Unmarshal(pair.Body.Bytes(), &paired); err != nil {
		t.Fatal(err)
	}

	send := func(method, path, bearer string, body any) *httptest.ResponseRecorder {
		t.Helper()
		var payload []byte
		if body != nil {
			payload = mustJSON(t, body)
		}
		req := httptest.NewRequest(method, path, bytes.NewReader(payload))
		req.Header.Set("Content-Type", "application/json")
		if bearer != "" {
			req.Header.Set("Authorization", "Bearer "+bearer)
		}
		rr := httptest.NewRecorder()
		mux.ServeHTTP(rr, req)
		return rr
	}
	claim := func() (int64, string) {
		t.Helper()
		rr := send("POST", "/api/print/jobs/claim?format=zpl", paired.Token, map[string]any{})
		if rr.Code != http.StatusOK {
			t.Fatalf("claim: %d %s", rr.Code, rr.Body.String())
		}
		id, err := strconv.ParseInt(rr.Header().Get("X-Print-Job-ID"), 10, 64)
		if err != nil || rr.Header().Get("X-Print-Claim") == "" {
			t.Fatalf("claim headers: id=%q err=%v", rr.Header().Get("X-Print-Job-ID"), err)
		}
		return id, rr.Header().Get("X-Print-Claim")
	}
	confirm := func(kind string, jobID int64, token string) int {
		t.Helper()
		return send("POST", fmt.Sprintf("/api/print/jobs/%d/%s", jobID, kind), paired.Token,
			printAckInput{ClaimToken: token}).Code
	}

	// The ESP reports uncertainty and the operator retries: the old claim token
	// can never be confirmed again, so the ESP must see a final 409.
	jobID, oldToken := claim()
	if code := confirm("uncertain", jobID, oldToken); code != http.StatusOK {
		t.Fatalf("uncertain: %d", code)
	}
	retryReq := httptest.NewRequest("POST", fmt.Sprintf("/api/print/jobs/%d/retry", jobID), bytes.NewReader([]byte("{}"))).WithContext(userCtx)
	retryReq.SetPathValue("id", strconv.FormatInt(jobID, 10))
	retryRes := httptest.NewRecorder()
	a.handlePrintRetry(retryRes, retryReq)
	if retryRes.Code != http.StatusOK {
		t.Fatalf("retry: %d %s", retryRes.Code, retryRes.Body.String())
	}
	for _, kind := range []string{"ack", "fail", "uncertain"} {
		if code := confirm(kind, jobID, oldToken); code != http.StatusConflict {
			t.Fatalf("%s with superseded claim token: got %d, want 409", kind, code)
		}
	}

	// A malformed confirmation is also final.
	if code := confirm("ack", jobID, "short"); code != http.StatusBadRequest {
		t.Fatalf("short claim token: got %d, want 400", code)
	}

	newID, newToken := claim()
	if newID != jobID || newToken == oldToken {
		t.Fatalf("retry must produce a new claim for the same job: id=%d", newID)
	}
	if code := confirm("ack", newID, newToken); code != http.StatusOK {
		t.Fatalf("ack with current claim token: %d", code)
	}

	// A revoked device answers 401, which the ESP treats as final.
	revokeReq := httptest.NewRequest("POST", fmt.Sprintf("/api/print/devices/%d/revoke", paired.ID), nil).WithContext(userCtx)
	revokeReq.SetPathValue("id", strconv.FormatInt(paired.ID, 10))
	revokeRes := httptest.NewRecorder()
	a.handlePrintRevoke(revokeRes, revokeReq)
	if revokeRes.Code != http.StatusOK {
		t.Fatalf("revoke: %d %s", revokeRes.Code, revokeRes.Body.String())
	}
	if rr := send("POST", fmt.Sprintf("/api/print/jobs/%d/ack", newID), paired.Token, printAckInput{ClaimToken: newToken}); rr.Code != http.StatusUnauthorized {
		t.Fatalf("revoked device: got %d, want 401", rr.Code)
	}
	if rr := send("POST", "/api/print/jobs/claim?format=zpl", "", map[string]any{}); rr.Code != http.StatusUnauthorized {
		t.Fatalf("missing token: got %d, want 401", rr.Code)
	}

	// A database failure must not look like a revoked token: the ESP keeps the
	// pending confirmation and tries again later.
	broken := openTestDatabase(t)
	broken.Close()
	brokenMux := http.NewServeMux()
	(&app{db: broken}).registerPrintHandlers(brokenMux)
	req := httptest.NewRequest("POST", fmt.Sprintf("/api/print/jobs/%d/ack", newID), bytes.NewReader(mustJSON(t, printAckInput{ClaimToken: newToken})))
	req.Header.Set("Authorization", "Bearer "+paired.Token)
	rr := httptest.NewRecorder()
	brokenMux.ServeHTTP(rr, req)
	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("database failure during printer auth: got %d, want 503", rr.Code)
	}
}
