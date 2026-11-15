package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// migrationsUpTo copies migrations whose name sorts before limit into a temp dir.
func migrationsUpTo(t *testing.T, limit string) string {
	t.Helper()
	src := filepath.Join("..", "..", "migrations")
	dir := t.TempDir()
	entries, err := os.ReadDir(src)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".sql") || e.Name() >= limit {
			continue
		}
		body, err := os.ReadFile(filepath.Join(src, e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, e.Name()), body, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// Production reached 040 with crate_id made nullable by hand and a crate-less
// PENDING job inserted outside the system. 040 must apply there and on a clean
// 039, cancel that job (never print or delete it) and stay idempotent.
func TestMigration040AppliesOnCleanAndHandEditedSchemas(t *testing.T) {
	for _, handEdited := range []bool{false, true} {
		t.Run(fmt.Sprintf("hand_edited=%v", handEdited), func(t *testing.T) {
			db := openTestDatabase(t)
			ctx := context.Background()
			if err := runMigrations(ctx, db, migrationsUpTo(t, "040_")); err != nil {
				t.Fatalf("apply up to 039: %v", err)
			}
			var stray int64
			if handEdited {
				if _, err := db.Exec(`ALTER TABLE print_jobs ALTER COLUMN crate_id DROP NOT NULL`); err != nil {
					t.Fatal(err)
				}
				var device int64
				if err := db.QueryRow(`INSERT INTO print_devices(name,token_hash) VALUES('Zebra manual','hash-manual') RETURNING id`).Scan(&device); err != nil {
					t.Fatal(err)
				}
				if err := db.QueryRow(`INSERT INTO print_jobs(crate_id,revision,device_id,zpl) VALUES(NULL,1,$1,'^XAunknown^XZ') RETURNING id`, device).Scan(&stray); err != nil {
					t.Fatal(err)
				}
			}
			for i := 0; i < 2; i++ {
				if err := runMigrations(ctx, db, filepath.Join("..", "..", "migrations")); err != nil {
					t.Fatalf("apply 040 (run %d): %v", i+1, err)
				}
			}
			if handEdited {
				var status string
				var zpl string
				if err := db.QueryRow(`SELECT status,zpl FROM print_jobs WHERE id=$1`, stray).Scan(&status, &zpl); err != nil {
					t.Fatalf("stray job must be kept: %v", err)
				}
				if status != "CANCELLED" || zpl != "^XAunknown^XZ" {
					t.Fatalf("stray job status=%s zpl=%q, want CANCELLED and untouched ZPL", status, zpl)
				}
			}
			if _, err := db.Exec(`INSERT INTO print_jobs(crate_id,revision,device_id,zpl) VALUES(NULL,1,NULL,'^XA^XZ')`); err == nil {
				t.Fatal("a pending job without crate and without printer must be rejected")
			}
		})
	}
}

func TestTestLabelZPLIsValidAndNeutralizesPrinterName(t *testing.T) {
	z := testLabelZPL("Zebra ^XZ~JR\\ ação", "15/11/2026 10:59", 42)
	if !strings.HasPrefix(z, "^XA") || !strings.HasSuffix(strings.TrimSpace(z), "^XZ") || strings.Count(z, "^XZ") != 1 {
		t.Fatalf("label must be one ^XA..^XZ block:\n%s", z)
	}
	if strings.Contains(z, "~JR") || strings.Contains(z, "\\") {
		t.Fatalf("printer name must not inject ZPL:\n%s", z)
	}
	for _, want := range []string{"ETIQUETA DE TESTE", "ZEBRA XZJR ACAO", "15/11/2026 10:59", "TESTE-42", "^PW480", "^LL480"} {
		if !strings.Contains(z, want) {
			t.Fatalf("label missing %q:\n%s", want, z)
		}
	}
}

func TestPrinterTestLabelEndToEnd(t *testing.T) {
	a, _, _, _ := newStockTestApp(t)
	createTestUser(t, a, "label-test-admin", "secret", "Diretor", true)
	createTestUser(t, a, "label-test-sales", "secret", "Vendas", true)
	admin := doLogin(t, a, "label-test-admin", "secret")
	sales := doLogin(t, a, "label-test-sales", "secret")
	mux := http.NewServeMux()
	a.registerPrintHandlers(mux)

	pair := func(name string) (int64, string) {
		t.Helper()
		rr := authReq(a, http.MethodPost, "/api/print/devices", map[string]string{"name": name}, admin, "")
		if rr.Code != http.StatusCreated {
			t.Fatalf("pair %s: %d %s", name, rr.Code, rr.Body.String())
		}
		var out struct {
			ID    int64  `json:"id"`
			Token string `json:"token"`
		}
		_ = json.Unmarshal(rr.Body.Bytes(), &out)
		return out.ID, out.Token
	}
	device, token := pair("Zebra Teste Alvo")
	_, otherToken := pair("Zebra Teste Outra")
	printer := func(path, bearer string, body any) *httptest.ResponseRecorder {
		t.Helper()
		var payload []byte
		if body != nil {
			payload = mustJSON(t, body)
		}
		req := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(payload))
		req.Header.Set("Authorization", "Bearer "+bearer)
		req.Header.Set("Content-Type", "application/json")
		rr := httptest.NewRecorder()
		mux.ServeHTTP(rr, req)
		return rr
	}
	testURL := "/api/print/devices/" + itoa(device) + "/test"

	if rr := authReq(a, http.MethodPost, testURL, map[string]any{}, sales, ""); rr.Code != http.StatusForbidden {
		t.Fatalf("test without warehouses write: %d", rr.Code)
	}
	if rr := authReq(a, http.MethodPost, testURL, map[string]any{}, admin, ""); rr.Code != http.StatusCreated {
		t.Fatalf("queue test: %d %s", rr.Code, rr.Body.String())
	}
	if rr := authReq(a, http.MethodPost, testURL, map[string]any{}, admin, ""); rr.Code != http.StatusConflict {
		t.Fatalf("second tap while waiting: got %d, want 409", rr.Code)
	}

	// The test is addressed: another printer sees an empty queue.
	if rr := printer("/api/print/jobs/claim?format=zpl", otherToken, map[string]any{}); rr.Code != http.StatusNoContent {
		t.Fatalf("other printer claim: got %d, want 204", rr.Code)
	}
	// A crate-less job is claimed normally (in production this answered 503).
	rr := printer("/api/print/jobs/claim?format=zpl", token, map[string]any{})
	if rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), "ETIQUETA DE TESTE") {
		t.Fatalf("target printer claim: %d %s", rr.Code, rr.Body.String())
	}
	jobID, _ := strconv.ParseInt(rr.Header().Get("X-Print-Job-ID"), 10, 64)
	if rr := authReq(a, http.MethodPost, testURL, map[string]any{}, admin, ""); rr.Code != http.StatusConflict {
		t.Fatalf("tap while claimed: got %d, want 409", rr.Code)
	}
	if code := printer(fmt.Sprintf("/api/print/jobs/%d/ack", jobID), token, printAckInput{ClaimToken: rr.Header().Get("X-Print-Claim")}).Code; code != http.StatusOK {
		t.Fatalf("ack test: %d", code)
	}

	devices := func() map[string]map[string]any {
		t.Helper()
		rr := authReq(a, http.MethodGet, "/api/print/devices", nil, admin, "")
		if rr.Code != http.StatusOK {
			t.Fatalf("list devices: %d", rr.Code)
		}
		var out struct {
			Devices []map[string]any `json:"devices"`
		}
		_ = json.Unmarshal(rr.Body.Bytes(), &out)
		m := map[string]map[string]any{}
		for _, d := range out.Devices {
			m[d["name"].(string)] = d
		}
		return m
	}
	if d := devices()["Zebra Teste Alvo"]; d["last_test_status"] != "SENT" || d["last_test_at"] == "" {
		t.Fatalf("device list must show the sent test: %v", d)
	}
	if _, ok := devices()["Zebra Teste Outra"]["last_test_status"]; ok {
		t.Fatal("a printer never tested must not show a test status")
	}

	// The JSON claim path also handles a job without crate.
	if rr := authReq(a, http.MethodPost, testURL, map[string]any{}, admin, ""); rr.Code != http.StatusCreated {
		t.Fatalf("queue second test: %d", rr.Code)
	}
	rr = printer("/api/print/jobs/claim", token, map[string]any{})
	var job map[string]any
	if rr.Code != http.StatusOK || json.Unmarshal(rr.Body.Bytes(), &job) != nil {
		t.Fatalf("JSON claim: %d %s", rr.Code, rr.Body.String())
	}
	if _, has := job["crate_id"]; has {
		t.Fatalf("test job must not report a crate: %v", job)
	}

	// Caixas keeps listing only crate jobs.
	if rr := authReq(a, http.MethodGet, "/api/print/jobs", nil, admin, ""); rr.Code != http.StatusOK || strings.Contains(rr.Body.String(), "TESTE") {
		t.Fatalf("crates queue list: %d %s", rr.Code, rr.Body.String())
	}

	// Revoking cancels a pending test and blocks new ones.
	other, _ := pair("Zebra Teste Revogada")
	otherURL := "/api/print/devices/" + itoa(other) + "/test"
	if rr := authReq(a, http.MethodPost, otherURL, map[string]any{}, admin, ""); rr.Code != http.StatusCreated {
		t.Fatalf("queue test to revoke: %d", rr.Code)
	}
	if rr := authReq(a, http.MethodPost, "/api/print/devices/"+itoa(other)+"/revoke", map[string]any{}, admin, ""); rr.Code != http.StatusOK {
		t.Fatalf("revoke: %d %s", rr.Code, rr.Body.String())
	}
	var status sql.NullString
	if err := a.db.QueryRow(`SELECT status FROM print_jobs WHERE device_id=$1 ORDER BY id DESC LIMIT 1`, other).Scan(&status); err != nil || status.String != "CANCELLED" {
		t.Fatalf("pending test after revoke: %v %v", status, err)
	}
	if rr := authReq(a, http.MethodPost, otherURL, map[string]any{}, admin, ""); rr.Code != http.StatusConflict {
		t.Fatalf("test on revoked printer: got %d, want 409", rr.Code)
	}
}
