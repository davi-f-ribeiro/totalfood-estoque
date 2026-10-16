package main

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

func dispatchTestPlace(t *testing.T, a *app, director loginData, customer int64) int64 {
	t.Helper()
	rr := authReq(a, http.MethodPost, "/api/places", map[string]any{
		"name": "Refeitório Pecém", "kind": "cliente", "customer_id": customer, "address": "Rodovia CE-085, km 40",
	}, director, "")
	if rr.Code != http.StatusCreated {
		t.Fatalf("create place status=%d body=%s", rr.Code, rr.Body.String())
	}
	return jsonID(t, rr.Body.Bytes())
}

func dispatchTestRecipe(t *testing.T, a *app, director loginData, item int64) int64 {
	t.Helper()
	rr := authReq(a, http.MethodPost, "/api/recipes", map[string]any{
		"name": "Almoço executivo", "portions": "1",
		"lines": []map[string]any{{"item_id": item, "quantity": "0,2", "correction_factor": "1"}},
	}, director, "")
	if rr.Code != http.StatusCreated {
		t.Fatalf("create recipe status=%d body=%s", rr.Code, rr.Body.String())
	}
	return jsonID(t, rr.Body.Bytes())
}

func dispatchOrderBody(op string, place, warehouse int64, lines []map[string]any) map[string]any {
	return map[string]any{
		"operation_id": op, "place_id": place, "origin_warehouse_id": warehouse,
		"day": "2026-10-16", "meal": "almoco", "due_at": "11:00", "lines": lines,
	}
}

func TestDeliveryOrderCreateReplayAndLineRules(t *testing.T) {
	a, _, wh, _ := newStockTestApp(t)
	createTestUser(t, a, "dispatch-director", "secret", "Diretor", true)
	director := doLogin(t, a, "dispatch-director", "secret")
	customer := recipeTestCustomer(t, a, director, "Data Center Pecém")
	place := dispatchTestPlace(t, a, director, customer)
	arroz := lookupID(t, a, `SELECT id FROM inventory_items WHERE name='Arroz'`)
	recipe := dispatchTestRecipe(t, a, director, arroz)

	good := dispatchOrderBody("dispatch-order-1", place, wh, []map[string]any{
		{"recipe_id": recipe, "portions": 40},
		{"item_id": arroz, "quantity": "3,5"},
	})
	created := authReq(a, http.MethodPost, "/api/delivery-orders", good, director, "")
	if created.Code != http.StatusCreated {
		t.Fatalf("create status=%d body=%s", created.Code, created.Body.String())
	}
	id := jsonID(t, created.Body.Bytes())

	replay := authReq(a, http.MethodPost, "/api/delivery-orders", good, director, "")
	if replay.Code != http.StatusOK || !strings.Contains(replay.Body.String(), `"replayed":true`) || jsonID(t, replay.Body.Bytes()) != id {
		t.Fatalf("replay must return the same order, got %d body=%s", replay.Code, replay.Body.String())
	}

	detail := authReq(a, http.MethodGet, "/api/delivery-orders/"+itoa(id), nil, director, "")
	if detail.Code != http.StatusOK {
		t.Fatalf("detail status=%d body=%s", detail.Code, detail.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(detail.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body["status"] != "criado" || len(body["lines"].([]any)) != 2 || len(body["tracking_token"].(string)) < 32 {
		t.Fatalf("detail must be criado with two lines and a tracking token: %v", body)
	}

	cases := []struct {
		name string
		body map[string]any
		want string
	}{
		{"mixed line", dispatchOrderBody("dispatch-bad-mixed", place, wh, []map[string]any{{"recipe_id": recipe, "portions": 1, "item_id": arroz, "quantity": "1"}}), "não os dois"},
		{"meal without portions", dispatchOrderBody("dispatch-bad-portions", place, wh, []map[string]any{{"recipe_id": recipe}}), "quantidade de porções"},
		{"zero quantity", dispatchOrderBody("dispatch-bad-zero", place, wh, []map[string]any{{"item_id": arroz, "quantity": "0"}}), "maior que zero"},
		{"empty line", dispatchOrderBody("dispatch-bad-empty", place, wh, []map[string]any{{}}), "Linha 1 sem insumo nem refeição"},
		{"bad meal", func() map[string]any {
			b := dispatchOrderBody("dispatch-bad-meal", place, wh, good["lines"].([]map[string]any))
			b["meal"] = "ceia"
			return b
		}(), "Refeição inválida"},
		{"no lines", dispatchOrderBody("dispatch-bad-none", place, wh, nil), "ao menos um insumo"},
	}
	for _, tc := range cases {
		rr := authReq(a, http.MethodPost, "/api/delivery-orders", tc.body, director, "")
		logisticsErrorIs(t, rr, http.StatusBadRequest, tc.want)
	}
}

func TestDeliveryEventsFollowTransitionsAndReplayByOperation(t *testing.T) {
	a, _, wh, _ := newStockTestApp(t)
	createTestUser(t, a, "dispatch-events", "secret", "Diretor", true)
	director := doLogin(t, a, "dispatch-events", "secret")
	customer := recipeTestCustomer(t, a, director, "Cliente Eventos")
	place := dispatchTestPlace(t, a, director, customer)
	arroz := lookupID(t, a, `SELECT id FROM inventory_items WHERE name='Arroz'`)
	created := authReq(a, http.MethodPost, "/api/delivery-orders", dispatchOrderBody("dispatch-events-1", place, wh, []map[string]any{{"item_id": arroz, "quantity": "2"}}), director, "")
	if created.Code != http.StatusCreated {
		t.Fatalf("create status=%d body=%s", created.Code, created.Body.String())
	}
	id := jsonID(t, created.Body.Bytes())
	path := "/api/delivery-orders/" + itoa(id) + "/events"

	// Pular de criado para entregue não pode: o pedido ainda nem saiu.
	logisticsErrorIs(t, authReq(a, http.MethodPost, path, map[string]any{"operation_id": "ev-skip", "status": "entregue"}, director, ""), http.StatusConflict, "Não dá para mudar de criado para entregue")

	first := authReq(a, http.MethodPost, path, map[string]any{"operation_id": "ev-programado", "status": "programado"}, director, "")
	if first.Code != http.StatusCreated {
		t.Fatalf("programado status=%d body=%s", first.Code, first.Body.String())
	}
	// Reenvio do mesmo evento pelo celular: não grava de novo.
	again := authReq(a, http.MethodPost, path, map[string]any{"operation_id": "ev-programado", "status": "programado"}, director, "")
	if again.Code != http.StatusOK || !strings.Contains(again.Body.String(), `"replayed":true`) {
		t.Fatalf("replayed event status=%d body=%s", again.Code, again.Body.String())
	}

	// Coordenada sozinha é recusada; coordenada completa é gravada.
	logisticsErrorIs(t, authReq(a, http.MethodPost, path, map[string]any{"operation_id": "ev-half", "status": "despachado", "latitude": "-3.5"}, director, ""), http.StatusBadRequest, "latitude e longitude juntas")
	ok := authReq(a, http.MethodPost, path, map[string]any{"operation_id": "ev-despachado", "status": "despachado", "latitude": "-3,5432", "longitude": "-38.8123"}, director, "")
	if ok.Code != http.StatusCreated {
		t.Fatalf("despachado status=%d body=%s", ok.Code, ok.Body.String())
	}

	var version int64
	if err := a.db.QueryRow(`SELECT version FROM delivery_orders WHERE id=$1`, id).Scan(&version); err != nil || version != 3 {
		t.Fatalf("version after three accepted events = %d err=%v, want 3", version, err)
	}
	var events int
	if err := a.db.QueryRow(`SELECT count(*) FROM delivery_events WHERE order_id=$1`, id).Scan(&events); err != nil || events != 2 {
		t.Fatalf("events=%d err=%v, want 2 (replay must not add a row)", events, err)
	}
}

func TestDeliveryPermissionsReadForManagerWriteForDirectorOnly(t *testing.T) {
	a, _, _, _ := newStockTestApp(t)
	createTestUser(t, a, "dispatch-manager", "secret", "Gerente", true)
	createTestUser(t, a, "dispatch-stock", "secret", "Estoquista", true)
	manager := doLogin(t, a, "dispatch-manager", "secret")
	stock := doLogin(t, a, "dispatch-stock", "secret")

	if rr := authReq(a, http.MethodGet, "/api/delivery-orders", nil, manager, ""); rr.Code != http.StatusOK {
		t.Fatalf("manager list status=%d", rr.Code)
	}
	if rr := authReq(a, http.MethodPost, "/api/delivery-orders", dispatchOrderBody("dispatch-perm", 1, 1, []map[string]any{{"item_id": 1, "quantity": "1"}}), manager, ""); rr.Code != http.StatusForbidden {
		t.Fatalf("manager create status=%d, want 403", rr.Code)
	}
	if rr := authReq(a, http.MethodGet, "/api/delivery-orders", nil, stock, ""); rr.Code != http.StatusForbidden {
		t.Fatalf("stock clerk list status=%d, want 403", rr.Code)
	}
}
