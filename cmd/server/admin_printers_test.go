package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// Printer configuration lives in Administração; Caixas keeps only operation and
// the print queue. Checked through the real router with session and CSRF.
func TestPrinterConfigurationLivesInAdministration(t *testing.T) {
	a, _, _, _ := newStockTestApp(t)
	server := newHTTPServer("127.0.0.1:0", a)
	page := func(path string, ld loginData) string {
		t.Helper()
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req.AddCookie(ld.Cookie)
		rr := httptest.NewRecorder()
		server.Handler.ServeHTTP(rr, req)
		if rr.Code != http.StatusOK {
			t.Fatalf("GET %s status=%d", path, rr.Code)
		}
		return rr.Body.String()
	}
	profileWith := func(name string, perms ...[2]string) int64 {
		t.Helper()
		var id int64
		if err := a.db.QueryRow(`INSERT INTO profiles(name) VALUES($1) RETURNING id`, name).Scan(&id); err != nil {
			t.Fatal(err)
		}
		for _, p := range perms {
			if _, err := a.db.Exec(`INSERT INTO profile_permissions(profile_id,permission_id)
SELECT $1,id FROM permissions WHERE resource=$2 AND action=$3`, id, p[0], p[1]); err != nil {
				t.Fatal(err)
			}
		}
		return id
	}

	createTestUserWithProfileID(t, a, "printer-admin", "secret", profileWith("Admin de Impressoras", [2]string{"warehouses", "write"}), true)
	createTestUserWithProfileID(t, a, "printer-operator", "secret", profileWith("Recebimento de Caixas", [2]string{"movement_entry", "write"}), true)
	createTestUser(t, a, "printer-director", "secret", "Diretor", true)
	admin := doLogin(t, a, "printer-admin", "secret")
	operator := doLogin(t, a, "printer-operator", "secret")
	director := doLogin(t, a, "printer-director", "secret")

	adminPage := page("/admin", admin)
	for _, want := range []string{`id="adminPrinterForm"`, `id="adminPrintersList"`, `/static/admin_printers.js`} {
		if !strings.Contains(adminPage, want) {
			t.Fatalf("warehouse admin page is missing %s", want)
		}
	}

	cratesDirector := page("/crates", director)
	if strings.Contains(cratesDirector, "cratePrinterForm") || strings.Contains(cratesDirector, "Parear impressora") {
		t.Fatal("Caixas still mixes printer configuration with operation")
	}
	if !strings.Contains(cratesDirector, `id="cratePrintJobs"`) || !strings.Contains(cratesDirector, `href="/admin"`) {
		t.Fatal("Caixas must keep the print queue and point administrators to Administração")
	}
	cratesOperator := page("/crates", operator)
	if !strings.Contains(cratesOperator, `id="cratePrintJobs"`) || strings.Contains(cratesOperator, `href="/admin">Administração</a>.`) {
		t.Fatal("an operator keeps the queue and gets no pointer to Administração")
	}

	// Log in again before the API calls (kept from when each page load issued a new CSRF token).
	admin = doLogin(t, a, "printer-admin", "secret")
	operator = doLogin(t, a, "printer-operator", "secret")

	// Pair, list, revoke and list again; an operator without warehouses write is refused.
	pair := authReq(a, http.MethodPost, "/api/print/devices", map[string]string{"name": "Zebra Admin Test"}, admin, "")
	if pair.Code != http.StatusCreated {
		t.Fatalf("pair status=%d body=%s", pair.Code, pair.Body.String())
	}
	var created struct {
		ID    int64  `json:"id"`
		Token string `json:"token"`
	}
	if err := json.Unmarshal(pair.Body.Bytes(), &created); err != nil || created.Token == "" {
		t.Fatalf("pair response: %v %s", err, pair.Body.String())
	}
	listed := func() map[string]any {
		t.Helper()
		rr := authReq(a, http.MethodGet, "/api/print/devices", nil, admin, "")
		if rr.Code != http.StatusOK {
			t.Fatalf("list status=%d", rr.Code)
		}
		if strings.Contains(rr.Body.String(), created.Token) {
			t.Fatal("device list must never return the access token")
		}
		var out struct {
			Devices []map[string]any `json:"devices"`
		}
		_ = json.Unmarshal(rr.Body.Bytes(), &out)
		for _, d := range out.Devices {
			if d["name"] == "Zebra Admin Test" {
				return d
			}
		}
		t.Fatal("paired printer not listed")
		return nil
	}
	if d := listed(); d["active"] != true {
		t.Fatalf("new printer must be active: %v", d)
	}
	if rr := authReq(a, http.MethodPost, "/api/print/devices/"+itoa(created.ID)+"/revoke", map[string]any{}, operator, ""); rr.Code != http.StatusForbidden {
		t.Fatalf("operator revoke status=%d, want 403", rr.Code)
	}
	if rr := authReq(a, http.MethodGet, "/api/print/devices", nil, operator, ""); rr.Code != http.StatusForbidden {
		t.Fatalf("operator list status=%d, want 403", rr.Code)
	}
	if rr := authReq(a, http.MethodPost, "/api/print/devices/"+itoa(created.ID)+"/revoke", map[string]any{}, admin, ""); rr.Code != http.StatusOK {
		t.Fatalf("revoke status=%d body=%s", rr.Code, rr.Body.String())
	}
	if d := listed(); d["active"] != false {
		t.Fatalf("revoked printer must be listed as inactive: %v", d)
	}
	if rr := authReq(a, http.MethodPost, "/api/print/devices", map[string]string{"name": "Zebra Admin Test"}, admin, ""); rr.Code != http.StatusConflict {
		t.Fatalf("reusing a revoked name status=%d, want 409", rr.Code)
	}
}
