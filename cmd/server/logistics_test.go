package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func logisticsList(t *testing.T, rr *httptest.ResponseRecorder) []map[string]any {
	t.Helper()
	if rr.Code != http.StatusOK {
		t.Fatalf("list status=%d body=%s", rr.Code, rr.Body.String())
	}
	var out []map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &out); err != nil {
		t.Fatalf("invalid json %q: %v", rr.Body.String(), err)
	}
	return out
}

func logisticsErrorIs(t *testing.T, rr *httptest.ResponseRecorder, status int, fragment string) {
	t.Helper()
	if rr.Code != status || !strings.Contains(rr.Body.String(), fragment) {
		t.Fatalf("want %d with %q, got %d body=%s", status, fragment, rr.Code, rr.Body.String())
	}
}

func TestPlacesValidateKindCoordinatesAndWindow(t *testing.T) {
	a, _, wh, _ := newStockTestApp(t)
	createTestUser(t, a, "places-director", "secret", "Diretor", true)
	director := doLogin(t, a, "places-director", "secret")
	customer := recipeTestCustomer(t, a, director, "Data Center Pecém")

	base := func() map[string]any {
		return map[string]any{"name": "Refeitório DC", "kind": "cliente", "customer_id": customer,
			"address": "Rodovia CE-085, km 40", "city": "Caucaia", "latitude": "-3,543210", "longitude": "-38.812345",
			"window_start": "10:30", "window_end": "11:30"}
	}
	cases := []struct {
		change map[string]any
		msg    string
	}{
		{map[string]any{"name": " "}, "Digite o nome do local."},
		{map[string]any{"kind": "obra"}, "Escolha o tipo do local."},
		{map[string]any{"customer_id": 0}, "Escolha o cliente deste local."},
		{map[string]any{"kind": "deposito"}, "Escolha o depósito deste local."},
		{map[string]any{"address": ""}, "Digite o endereço do local."},
		{map[string]any{"latitude": "-91"}, "Latitude inválida"},
		{map[string]any{"longitude": "-38.1234567"}, "Longitude inválida"},
		{map[string]any{"longitude": ""}, "latitude e longitude juntas"},
		{map[string]any{"window_end": ""}, "início e o fim da janela"},
		{map[string]any{"window_end": "25:00"}, "Horário inválido"},
		{map[string]any{"window_end": "10:00"}, "precisa ser depois do início"},
	}
	for _, tc := range cases {
		body := base()
		for k, v := range tc.change {
			body[k] = v
		}
		logisticsErrorIs(t, authReq(a, http.MethodPost, "/api/places", body, director, ""), http.StatusBadRequest, tc.msg)
	}

	create := authReq(a, http.MethodPost, "/api/places", base(), director, "")
	if create.Code != http.StatusCreated {
		t.Fatalf("create place status=%d body=%s", create.Code, create.Body.String())
	}
	id := jsonID(t, create.Body.Bytes())
	logisticsErrorIs(t, authReq(a, http.MethodPost, "/api/places", base(), director, ""), http.StatusConflict, "Já existe um local com este nome.")

	// Um depósito não guarda cliente, mesmo que o formulário mande um.
	deposit := map[string]any{"name": "Central VR", "kind": "deposito", "warehouse_id": wh, "customer_id": customer, "address": "Rua A, 1"}
	if rr := authReq(a, http.MethodPost, "/api/places", deposit, director, ""); rr.Code != http.StatusCreated {
		t.Fatalf("create deposit place status=%d body=%s", rr.Code, rr.Body.String())
	}

	places := logisticsList(t, authReq(a, http.MethodGet, "/api/places", nil, director, ""))
	if len(places) != 2 {
		t.Fatalf("want 2 places, got %v", places)
	}
	var dc, vr map[string]any
	for _, p := range places {
		switch p["name"] {
		case "Refeitório DC":
			dc = p
		case "Central VR":
			vr = p
		}
	}
	if dc["latitude"] != "-3.543210" || dc["longitude"] != "-38.812345" || dc["window_start"] != "10:30" || dc["customer_name"] != "Data Center Pecém" {
		t.Fatalf("unexpected client place %v", dc)
	}
	if vr["customer_id"] != nil || vr["warehouse_name"] != "VR" {
		t.Fatalf("deposit place must keep only the warehouse: %v", vr)
	}

	// Concorrência: a segunda gravação com a versão antiga é recusada.
	upd := base()
	upd["name"], upd["version"] = "Refeitório Data Center", 1
	if rr := authReq(a, http.MethodPatch, "/api/places/"+itoa(id), upd, director, ""); rr.Code != http.StatusOK {
		t.Fatalf("update place status=%d body=%s", rr.Code, rr.Body.String())
	}
	logisticsErrorIs(t, authReq(a, http.MethodPatch, "/api/places/"+itoa(id), upd, director, ""), http.StatusConflict, "")
	logisticsErrorIs(t, authReq(a, http.MethodPatch, "/api/places/999999", upd, director, ""), http.StatusNotFound, "Local não encontrado.")
}

func TestPlaceCoverageListsActiveCustomersWithoutRoutablePlace(t *testing.T) {
	a, _, _, _ := newStockTestApp(t)
	createTestUser(t, a, "coverage-director", "secret", "Diretor", true)
	director := doLogin(t, a, "coverage-director", "secret")
	withCoord := recipeTestCustomer(t, a, director, "Base Aérea")
	noCoord := recipeTestCustomer(t, a, director, "Obra BS Ville")
	recipeTestCustomer(t, a, director, "Quentinhas")
	inactive := recipeTestCustomer(t, a, director, "Cliente antigo")
	if _, err := a.db.Exec(`UPDATE customers SET active=false WHERE id=$1`, inactive); err != nil {
		t.Fatal(err)
	}
	for _, p := range []map[string]any{
		{"name": "Base Aérea — rancho", "kind": "cliente", "customer_id": withCoord, "address": "Av. Borges de Melo", "latitude": "-3.776", "longitude": "-38.532"},
		{"name": "Obra BS Ville", "kind": "cliente", "customer_id": noCoord, "address": "Rua B, 200"},
	} {
		if rr := authReq(a, http.MethodPost, "/api/places", p, director, ""); rr.Code != http.StatusCreated {
			t.Fatalf("create place status=%d body=%s", rr.Code, rr.Body.String())
		}
	}
	rr := authReq(a, http.MethodGet, "/api/places/coverage", nil, director, "")
	if rr.Code != http.StatusOK {
		t.Fatalf("coverage status=%d body=%s", rr.Code, rr.Body.String())
	}
	var out struct {
		Missing []map[string]any `json:"customers_without_place"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, c := range out.Missing {
		names = append(names, c["name"].(string))
	}
	if strings.Join(names, ",") != "Obra BS Ville,Quentinhas" {
		t.Fatalf("customers without routable place = %v", names)
	}
}

func TestVehiclesNormalizePlateAndRejectDuplicates(t *testing.T) {
	a, _, _, _ := newStockTestApp(t)
	createTestUser(t, a, "vehicles-director", "secret", "Diretor", true)
	director := doLogin(t, a, "vehicles-director", "secret")

	logisticsErrorIs(t, authReq(a, http.MethodPost, "/api/vehicles", map[string]any{"plate": "AB-123", "description": "Fiorino", "capacity_kg": "650"}, director, ""), http.StatusBadRequest, "Placa inválida")
	logisticsErrorIs(t, authReq(a, http.MethodPost, "/api/vehicles", map[string]any{"plate": "ABC1D23", "description": "Fiorino", "capacity_kg": "0"}, director, ""), http.StatusBadRequest, "capacidade em kg")

	body := map[string]any{"plate": "abc-1d23", "description": "Fiorino refrigerada", "capacity_kg": "650,5", "capacity_boxes": 12, "refrigerated": true}
	create := authReq(a, http.MethodPost, "/api/vehicles", body, director, "")
	if create.Code != http.StatusCreated {
		t.Fatalf("create vehicle status=%d body=%s", create.Code, create.Body.String())
	}
	logisticsErrorIs(t, authReq(a, http.MethodPost, "/api/vehicles", map[string]any{"plate": "ABC1D23", "description": "Outra", "capacity_kg": "1"}, director, ""), http.StatusConflict, "Já existe um veículo com esta placa.")

	list := logisticsList(t, authReq(a, http.MethodGet, "/api/vehicles", nil, director, ""))
	if len(list) != 1 || list[0]["plate"] != "ABC1D23" || list[0]["capacity_kg"] != "650.50" || list[0]["capacity_boxes"] != float64(12) || list[0]["refrigerated"] != true {
		t.Fatalf("unexpected vehicles %v", list)
	}
}

func TestDriversValidateLicenseAndFlagExpired(t *testing.T) {
	a, _, _, _ := newStockTestApp(t)
	createTestUser(t, a, "drivers-director", "secret", "Diretor", true)
	userID := createTestUser(t, a, "motorista-joao", "secret", "Estoquista", true)
	director := doLogin(t, a, "drivers-director", "secret")

	logisticsErrorIs(t, authReq(a, http.MethodPost, "/api/drivers", map[string]any{"name": "João", "license_number": "123"}, director, ""), http.StatusBadRequest, "Número da CNH inválido")
	logisticsErrorIs(t, authReq(a, http.MethodPost, "/api/drivers", map[string]any{"name": "João", "license_category": "F"}, director, ""), http.StatusBadRequest, "Categoria da CNH inválida")
	logisticsErrorIs(t, authReq(a, http.MethodPost, "/api/drivers", map[string]any{"name": "João", "license_expires_at": "2026-02-30"}, director, ""), http.StatusBadRequest, "Data de validade da CNH inválida")

	vehicle := authReq(a, http.MethodPost, "/api/vehicles", map[string]any{"plate": "PMX4E21", "description": "Van", "capacity_kg": "1200"}, director, "")
	vehicleID := jsonID(t, vehicle.Body.Bytes())
	body := map[string]any{"name": "João Silva", "phone": "85 99999-0000", "license_number": "01234567890", "license_category": "ab",
		"license_expires_at": "2020-01-31", "user_id": userID, "default_vehicle_id": vehicleID}
	if rr := authReq(a, http.MethodPost, "/api/drivers", body, director, ""); rr.Code != http.StatusCreated {
		t.Fatalf("create driver status=%d body=%s", rr.Code, rr.Body.String())
	}
	other := map[string]any{"name": "Maria", "user_id": userID}
	logisticsErrorIs(t, authReq(a, http.MethodPost, "/api/drivers", other, director, ""), http.StatusConflict, "Este usuário já está ligado a outro motorista.")
	other = map[string]any{"name": "Maria", "license_number": "01234567890"}
	logisticsErrorIs(t, authReq(a, http.MethodPost, "/api/drivers", other, director, ""), http.StatusConflict, "Esta CNH já está cadastrada")

	list := logisticsList(t, authReq(a, http.MethodGet, "/api/drivers", nil, director, ""))
	if len(list) != 1 {
		t.Fatalf("want 1 driver, got %v", list)
	}
	d := list[0]
	if d["license_category"] != "AB" || d["license_expired"] != true || d["user_login"] != "motorista-joao" || d["default_vehicle_plate"] != "PMX4E21" {
		t.Fatalf("unexpected driver %v", d)
	}
}

func TestLogisticsPermissions(t *testing.T) {
	a, _, _, _ := newStockTestApp(t)
	createTestUser(t, a, "log-manager", "secret", "Gerente", true)
	createTestUser(t, a, "log-stock", "secret", "Estoquista", true)
	manager := doLogin(t, a, "log-manager", "secret")
	stock := doLogin(t, a, "log-stock", "secret")

	for _, path := range []string{"/api/places", "/api/places/coverage", "/api/vehicles", "/api/drivers"} {
		if rr := authReq(a, http.MethodGet, path, nil, manager, ""); rr.Code != http.StatusOK {
			t.Fatalf("manager GET %s status=%d", path, rr.Code)
		}
		if rr := authReq(a, http.MethodGet, path, nil, stock, ""); rr.Code != http.StatusForbidden {
			t.Fatalf("stock clerk GET %s status=%d", path, rr.Code)
		}
	}
	// Gerente só consulta: não grava e não vê a lista de logins das opções.
	if rr := authReq(a, http.MethodGet, "/api/logistics/options", nil, manager, ""); rr.Code != http.StatusForbidden {
		t.Fatalf("manager options status=%d", rr.Code)
	}
	if rr := authReq(a, http.MethodPost, "/api/vehicles", map[string]any{"plate": "ABC1234", "description": "Van", "capacity_kg": "1"}, manager, ""); rr.Code != http.StatusForbidden {
		t.Fatalf("manager create vehicle status=%d", rr.Code)
	}
	page := authReq(a, http.MethodGet, "/logistics", nil, manager, "")
	if page.Code != http.StatusOK || !strings.Contains(page.Body.String(), "Locais e frota") || strings.Contains(page.Body.String(), `id="placeDialog"`) {
		t.Fatalf("manager page status=%d, must render read-only", page.Code)
	}
	if rr := authReq(a, http.MethodGet, "/logistics", nil, stock, ""); rr.Code != http.StatusSeeOther {
		t.Fatalf("stock clerk page status=%d", rr.Code)
	}
}

func TestVehicleOwnershipOwnedOrRented(t *testing.T) {
	a, _, _, _ := newStockTestApp(t)
	createTestUser(t, a, "own-director", "secret", "Diretor", true)
	director := doLogin(t, a, "own-director", "secret")
	logisticsErrorIs(t, authReq(a, http.MethodPost, "/api/vehicles", map[string]any{"plate": "ALU1G23", "description": "Van", "capacity_kg": "900", "ownership": "comodato"}, director, ""), http.StatusBadRequest, "próprio ou alugado")
	rented := authReq(a, http.MethodPost, "/api/vehicles", map[string]any{"plate": "ALU1G23", "description": "Van alugada", "capacity_kg": "900", "ownership": "alugada"}, director, "")
	owned := authReq(a, http.MethodPost, "/api/vehicles", map[string]any{"plate": "PRO1P23", "description": "Fiorino", "capacity_kg": "600"}, director, "")
	if rented.Code != http.StatusCreated || owned.Code != http.StatusCreated {
		t.Fatalf("create rented=%d owned=%d", rented.Code, owned.Code)
	}
	got := map[string]string{}
	for _, v := range logisticsList(t, authReq(a, http.MethodGet, "/api/vehicles", nil, director, "")) {
		got[v["plate"].(string)] = v["ownership"].(string)
	}
	if got["ALU1G23"] != "alugada" || got["PRO1P23"] != "propria" {
		t.Fatalf("ownership = %v", got)
	}
	id := jsonID(t, rented.Body.Bytes())
	if rr := authReq(a, http.MethodPatch, "/api/vehicles/"+itoa(id), map[string]any{"plate": "ALU1G23", "description": "Van devolvida", "capacity_kg": "900", "ownership": "propria", "version": 1}, director, ""); rr.Code != http.StatusOK {
		t.Fatalf("update ownership status=%d body=%s", rr.Code, rr.Body.String())
	}
}
