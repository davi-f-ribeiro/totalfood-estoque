package main

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRastroPasswordEncryption(t *testing.T) {
	t.Setenv("RASTROCONNECT_KEY", hex.EncodeToString([]byte("12345678901234567890123456789012")))
	encrypted, err := rastroEncrypt("private-test-password")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encrypted), "private-test-password") {
		t.Fatal("plaintext credential leaked")
	}
	got, err := rastroDecrypt(encrypted)
	if err != nil || got != "private-test-password" {
		t.Fatalf("roundtrip failed: %v", err)
	}
	t.Setenv("RASTROCONNECT_KEY", hex.EncodeToString([]byte("abcdefghijabcdefghijabcdefghijab")))
	if _, err = rastroDecrypt(encrypted); err == nil {
		t.Fatal("wrong encryption key accepted")
	}
	t.Setenv("RASTROCONNECT_KEY", "")
	if _, err = rastroEncrypt("secret"); err == nil {
		t.Fatal("missing key accepted")
	}
}

func TestRastroProviderReadOnlySession(t *testing.T) {
	var loginValidated, monitorValidated bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/acl/login/":
			if r.Method == http.MethodGet {
				http.SetCookie(w, &http.Cookie{Name: "csrftoken", Value: "csrf-123", Path: "/"})
				io.WriteString(w, `<form><input type="hidden" name="csrfmiddlewaretoken" value="csrf-123"><input type="hidden" name="empresa" value=""></form>`)
				return
			}
			_ = r.ParseForm()
			loginValidated = r.Method == http.MethodPost && r.Form.Get("empresa") == "enterprise" && r.Form.Get("login") == "operator" && r.Form.Get("senha") == "pass" && r.Form.Get("csrfmiddlewaretoken") == "csrf-123"
			if !loginValidated {
				http.Error(w, "invalid login", 403)
				return
			}
			http.SetCookie(w, &http.Cookie{Name: "sessionid", Value: "session", Path: "/"})
			http.Redirect(w, r, "/veiculo/painel-monitoramento/", http.StatusSeeOther)
		case "/veiculo/painel-monitoramento/":
			io.WriteString(w, `<script>const token_ajax = 'testing-token';</script>`)
		case "/home/api/monitoramento/":
			cookie, err := r.Cookie("sessionid")
			monitorValidated = err == nil && cookie.Value == "session" && r.Method == http.MethodPost && r.Header.Get("Authorization") == "token testing-token" && r.Header.Get("X-Requested-With") == "XMLHttpRequest"
			raw := r.FormValue("json")
			var args struct {
				Op    string         `json:"op"`
				Kargs map[string]any `json:"kargs"`
			}
			if json.Unmarshal([]byte(raw), &args) != nil || args.Op != "update" {
				monitorValidated = false
			}
			io.WriteString(w, `[{"placa":"ABC-1D23","veiculo_id":100,"eqp_id":200,"veiculo":"Fiorino","latitude":-3.7,"longitude":"-38.5","velocidade":34,"ignition":true,"position_devicetime":"2026-10-16 08:21:00"},{"placa":"invalid","veiculo_id":101}]`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	got, skipped, err := rastroFleet(context.Background(), server.URL, "enterprise", "operator", "pass")
	if err != nil {
		t.Fatal(err)
	}
	if !loginValidated || !monitorValidated {
		t.Fatal("login or monitored endpoint incorrect")
	}
	if len(skipped) != 1 || skipped[0] != "Registro 2: placa inválida" {
		t.Fatalf("unexpected skipped vehicle diagnostics: %v", skipped)
	}
	if len(got) != 1 || got[0].Plate != "ABC1D23" || got[0].RemoteID != "100" || got[0].Latitude == nil || *got[0].Latitude != -3.7 || got[0].Ignition == nil || !*got[0].Ignition {
		t.Fatalf("unexpected vehicle parse: %+v", got)
	}
}

func TestRastroImportPreservesFleet(t *testing.T) {
	a, _, _, _ := newStockTestApp(t)
	createTestUser(t, a, "rastro-director", "secret", "Diretor", true)
	director := doLogin(t, a, "rastro-director", "secret")
	managerID := createTestUser(t, a, "rastro-manager", "secret", "Gerente", true)
	_ = managerID
	manager := doLogin(t, a, "rastro-manager", "secret")
	t.Setenv("RASTROCONNECT_KEY", hex.EncodeToString([]byte("12345678901234567890123456789012")))
	if rr := authReq(a, http.MethodGet, "/api/admin/rastroconnect", nil, manager, ""); rr.Code != http.StatusForbidden {
		t.Fatalf("manager may access credentials: %d", rr.Code)
	}
	initial := authReq(a, http.MethodPut, "/api/admin/rastroconnect", map[string]any{"company": "enterprise", "login": "operator", "password": "test-secret"}, director, "")
	if initial.Code != http.StatusOK {
		t.Fatalf("save credentials: %d %s", initial.Code, initial.Body.String())
	}
	var cipherText []byte
	if err := a.db.QueryRow(`SELECT password_cipher FROM rastroconnect_settings WHERE id=1`).Scan(&cipherText); err != nil || strings.Contains(string(cipherText), "test-secret") {
		t.Fatalf("password stored in plaintext or missing: %v", err)
	}
	cfg := authReq(a, http.MethodGet, "/api/admin/rastroconnect", nil, director, "")
	if strings.Contains(cfg.Body.String(), "test-secret") {
		t.Fatal("password disclosed by settings API")
	}
	original := authReq(a, http.MethodPost, "/api/vehicles", map[string]any{"plate": "ABC1D23", "description": "Original", "capacity_kg": "900", "refrigerated": true}, director, "")
	if original.Code != http.StatusCreated {
		t.Fatalf("original vehicle: %s", original.Body.String())
	}
	input := []rastroVehicle{{Plate: "ABC1D23", RemoteID: "10", Description: "Provider changed", Latitude: pointerFloat(-3.5), Longitude: pointerFloat(-38.4)}, {Plate: "DEF2G34", RemoteID: "11", Description: "Fiorino"}}
	created, linked, err := a.syncRastroVehicles(context.Background(), input)
	if err != nil || created != 1 || linked != 1 {
		t.Fatalf("sync first created=%d linked=%d err=%v", created, linked, err)
	}
	created, linked, err = a.syncRastroVehicles(context.Background(), input)
	if err != nil || created != 0 || linked != 2 {
		t.Fatalf("sync repeated created=%d linked=%d err=%v", created, linked, err)
	}
	var count int
	if err := a.db.QueryRow(`SELECT count(*) FROM vehicles`).Scan(&count); err != nil || count != 2 {
		t.Fatalf("vehicle duplication: %d %v", count, err)
	}
	var desc, cap string
	var refrigerated, active bool
	if err := a.db.QueryRow(`SELECT description,capacity_kg::text,refrigerated,active FROM vehicles WHERE plate='ABC1D23'`).Scan(&desc, &cap, &refrigerated, &active); err != nil || desc != "Original" || cap != "900.00" || !refrigerated || !active {
		t.Fatalf("local attributes overwritten: %v %s %s %v %v", err, desc, cap, refrigerated, active)
	}
	var pendingCapacity bool
	if err := a.db.QueryRow(`SELECT NOT active AND capacity_kg IS NULL FROM vehicles WHERE plate='DEF2G34'`).Scan(&pendingCapacity); err != nil || !pendingCapacity {
		t.Fatalf("import invented capacity: %v", err)
	}
	if _, err := a.db.Exec(`UPDATE vehicles SET active=true WHERE plate='DEF2G34'`); err == nil {
		t.Fatal("database allowed unsafe activation without capacity")
	}
	list := logisticsList(t, authReq(a, http.MethodGet, "/api/vehicles", nil, director, ""))
	if len(list) != 2 {
		t.Fatalf("vehicle list broken: %+v", list)
	}
	telemetry := logisticsList(t, authReq(a, http.MethodGet, "/api/vehicles/telemetry", nil, director, ""))
	if len(telemetry) != 2 {
		t.Fatalf("telemetry list broken: %+v", telemetry)
	}
}
func pointerFloat(v float64) *float64 { return &v }

func TestRastroImportConflictsRollback(t *testing.T) {
	a, _, _, _ := newStockTestApp(t)
	_, _, err := a.syncRastroVehicles(context.Background(), []rastroVehicle{{Plate: "ABC1D23", RemoteID: "10", Description: "Original"}})
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name     string
		input    []rastroVehicle
		expected string
	}{
		{"same plate different remote ID", []rastroVehicle{{Plate: "DEF2G34", RemoteID: "11", Description: "New"}, {Plate: "ABC1D23", RemoteID: "99", Description: "Changed"}}, "Placa ABC1D23 já está vinculada"},
		{"same remote ID different plate", []rastroVehicle{{Plate: "DEF2G34", RemoteID: "11", Description: "New"}, {Plate: "GHI3J45", RemoteID: "10", Description: "Wrong"}}, "Identificador 10 já está vinculado à placa ABC1D23"},
		{"provider plate collision", []rastroVehicle{{Plate: "DEF2G34", RemoteID: "11", Description: "New"}, {Plate: "DEF2G34", RemoteID: "12", Description: "Conflict"}}, "Placa DEF2G34 recebida com identificadores diferentes"},
		{"provider remote collision", []rastroVehicle{{Plate: "DEF2G34", RemoteID: "11", Description: "New"}, {Plate: "GHI3J45", RemoteID: "11", Description: "Conflict"}}, "Identificador 11 recebido para as placas"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, _, err := a.syncRastroVehicles(context.Background(), tc.input)
			var conflict *rastroConflictError
			if !errors.As(err, &conflict) || !strings.Contains(err.Error(), tc.expected) {
				t.Fatalf("wrong conflict: %v", err)
			}
			var count int
			if err := a.db.QueryRow(`SELECT count(*) FROM vehicles`).Scan(&count); err != nil || count != 1 {
				t.Fatalf("partial import despite collision: %v count=%d", err, count)
			}
		})
	}
	created, linked, err := a.syncRastroVehicles(context.Background(), []rastroVehicle{{Plate: "ABC1D23", RemoteID: "10", Description: "Van"}, {Plate: "ABC1D23", RemoteID: "10", Description: "Van"}})
	if err != nil || created != 0 || linked != 1 {
		t.Fatalf("identical duplicate should be harmless: created=%d linked=%d err=%v", created, linked, err)
	}
}

func TestRastroTelemetryShowsSourceAndSyncSeparately(t *testing.T) {
	a, _, _, _ := newStockTestApp(t)
	createTestUser(t, a, "rastro-clock-director", "secret", "Diretor", true)
	director := doLogin(t, a, "rastro-clock-director", "secret")
	_, _, err := a.syncRastroVehicles(context.Background(), []rastroVehicle{{Plate: "ABC1D23", RemoteID: "10", Description: "Van", SourceTime: "2026-01-02 01:01:00"}})
	if err != nil {
		t.Fatal(err)
	}
	output := logisticsList(t, authReq(a, http.MethodGet, "/api/vehicles/telemetry", nil, director, ""))
	if len(output) != 1 || output[0]["source_time"] != "2026-01-02 01:01:00" || output[0]["synced_at"] == nil {
		t.Fatalf("source clock conflated with query time: %+v", output)
	}
	ui, err := uiFS.ReadFile("static/logistics.js")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(ui), "Date.now()-new Date(gps.synced_at)") || !strings.Contains(string(ui), "GPS informado:") || !strings.Contains(string(ui), "Consultado em:") {
		t.Fatal("UI conflates sync time with source clock")
	}
}

func TestRastroRejectsNonFiniteCoordinates(t *testing.T) {
	for _, value := range []string{"NaN", "Inf", "-Inf"} {
		v, ok := parseRastroVehicle(map[string]any{"placa": "ABC1D23", "veiculo_id": float64(10), "latitude": value, "longitude": "-38.5", "velocidade": value})
		if !ok || v.Latitude != nil || v.Longitude != nil || v.Speed != nil {
			t.Fatalf("invalid numeric telemetry accepted %s: %+v", value, v)
		}
	}
}
