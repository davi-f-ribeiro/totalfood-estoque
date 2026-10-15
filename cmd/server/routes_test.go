package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func routeVersion(t *testing.T, rr *httptest.ResponseRecorder) int64 {
	t.Helper()
	var out struct {
		Version int64 `json:"version"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &out); err != nil || out.Version <= 0 {
		t.Fatalf("response without version: %d %s", rr.Code, rr.Body.String())
	}
	return out.Version
}

func routeTestCreate(t *testing.T, a *app, director loginData, path string, body map[string]any) int64 {
	t.Helper()
	rr := authReq(a, http.MethodPost, path, body, director, "")
	if rr.Code != http.StatusCreated {
		t.Fatalf("POST %s status=%d body=%s", path, rr.Code, rr.Body.String())
	}
	return jsonID(t, rr.Body.Bytes())
}

func orderStatus(t *testing.T, a *app, id int64) string {
	t.Helper()
	var s string
	if err := a.db.QueryRow(`SELECT status FROM delivery_orders WHERE id=$1`, id).Scan(&s); err != nil {
		t.Fatal(err)
	}
	return s
}

func TestRoutesAssignStopsAndMoveOrdersThroughTheDay(t *testing.T) {
	a, _, wh, _ := newStockTestApp(t)
	createTestUser(t, a, "route-director", "secret", "Diretor", true)
	director := doLogin(t, a, "route-director", "secret")
	customer := recipeTestCustomer(t, a, director, "Cliente Rota")
	place := dispatchTestPlace(t, a, director, customer)
	arroz := lookupID(t, a, `SELECT id FROM inventory_items WHERE name='Arroz'`)
	line := []map[string]any{{"item_id": arroz, "quantity": "2"}}

	van := routeTestCreate(t, a, director, "/api/vehicles", map[string]any{"plate": "PMX4E21", "description": "Van", "capacity_kg": "1200"})
	truck := routeTestCreate(t, a, director, "/api/vehicles", map[string]any{"plate": "QWE1J23", "description": "Caminhão baú", "capacity_kg": "4000"})
	joao := routeTestCreate(t, a, director, "/api/drivers", map[string]any{"name": "João", "license_expires_at": "2030-01-01"})
	maria := routeTestCreate(t, a, director, "/api/drivers", map[string]any{"name": "Maria", "license_expires_at": "2030-01-01"})
	expired := routeTestCreate(t, a, director, "/api/drivers", map[string]any{"name": "Pedro", "license_expires_at": "2026-01-01"})

	o1 := routeTestCreate(t, a, director, "/api/delivery-orders", dispatchOrderBody("route-o1", place, wh, line))
	o2 := routeTestCreate(t, a, director, "/api/delivery-orders", dispatchOrderBody("route-o2", place, wh, line))
	other := dispatchOrderBody("route-other-day", place, wh, line)
	other["day"] = "2026-10-17"
	otherDay := routeTestCreate(t, a, director, "/api/delivery-orders", other)

	logisticsErrorIs(t, authReq(a, http.MethodPost, "/api/routes", map[string]any{"day": "2026-10-16", "vehicle_id": van, "driver_id": expired}, director, ""), http.StatusBadRequest, "CNH deste motorista estará vencida")
	route := routeTestCreate(t, a, director, "/api/routes", map[string]any{"day": "2026-10-16", "vehicle_id": van, "driver_id": joao})
	logisticsErrorIs(t, authReq(a, http.MethodPost, "/api/routes", map[string]any{"day": "2026-10-16", "vehicle_id": van, "driver_id": maria}, director, ""), http.StatusConflict, "veículo já tem uma rota aberta")

	stops := "/api/routes/" + itoa(route) + "/stops"
	status := "/api/routes/" + itoa(route) + "/status"
	logisticsErrorIs(t, authReq(a, http.MethodPut, stops, map[string]any{"order_ids": []int64{o1, otherDay}, "version": 1}, director, ""), http.StatusBadRequest, "é de outro dia")
	logisticsErrorIs(t, authReq(a, http.MethodPut, stops, map[string]any{"order_ids": []int64{o1, o1}, "version": 1}, director, ""), http.StatusBadRequest, "aparece uma vez só")
	logisticsErrorIs(t, authReq(a, http.MethodPut, stops, map[string]any{"order_ids": []int64{o1}, "version": 7}, director, ""), http.StatusConflict, "alterado por outra pessoa")
	logisticsErrorIs(t, authReq(a, http.MethodPost, status, map[string]any{"status": "liberado", "version": 1}, director, ""), http.StatusBadRequest, "ao menos uma entrega")

	set := authReq(a, http.MethodPut, stops, map[string]any{"order_ids": []int64{o2, o1}, "version": 1}, director, "")
	if set.Code != http.StatusOK {
		t.Fatalf("set stops status=%d body=%s", set.Code, set.Body.String())
	}
	version := routeVersion(t, set)

	// Outra rota aberta não pode levar a mesma entrega.
	second := routeTestCreate(t, a, director, "/api/routes", map[string]any{"day": "2026-10-16", "vehicle_id": truck, "driver_id": maria})
	logisticsErrorIs(t, authReq(a, http.MethodPut, "/api/routes/"+itoa(second)+"/stops", map[string]any{"order_ids": []int64{o1}, "version": 1}, director, ""), http.StatusConflict, "já está na rota")

	// O romaneio sai na ordem definida, com o que entregar em cada parada.
	detail := authReq(a, http.MethodGet, "/api/routes/"+itoa(route), nil, director, "")
	var romaneio struct {
		Stops []struct {
			Sequence int64            `json:"sequence"`
			OrderID  int64            `json:"order_id"`
			Lines    []map[string]any `json:"lines"`
		} `json:"stops"`
	}
	if err := json.Unmarshal(detail.Body.Bytes(), &romaneio); err != nil || len(romaneio.Stops) != 2 {
		t.Fatalf("detail status=%d body=%s", detail.Code, detail.Body.String())
	}
	if romaneio.Stops[0].OrderID != o2 || romaneio.Stops[1].OrderID != o1 || len(romaneio.Stops[0].Lines) != 1 {
		t.Fatalf("stops out of order or without lines: %+v", romaneio.Stops)
	}

	rel := authReq(a, http.MethodPost, status, map[string]any{"status": "liberado", "version": version}, director, "")
	if rel.Code != http.StatusOK || orderStatus(t, a, o1) != "programado" || orderStatus(t, a, o2) != "programado" {
		t.Fatalf("release status=%d body=%s o1=%s", rel.Code, rel.Body.String(), orderStatus(t, a, o1))
	}
	logisticsErrorIs(t, authReq(a, http.MethodPut, stops, map[string]any{"order_ids": []int64{o1}, "version": routeVersion(t, rel)}, director, ""), http.StatusConflict, "rota em rascunho")

	start := authReq(a, http.MethodPost, status, map[string]any{"status": "em_andamento", "version": routeVersion(t, rel)}, director, "")
	if start.Code != http.StatusOK || orderStatus(t, a, o1) != "despachado" {
		t.Fatalf("start status=%d body=%s o1=%s", start.Code, start.Body.String(), orderStatus(t, a, o1))
	}
	version = routeVersion(t, start)
	logisticsErrorIs(t, authReq(a, http.MethodPost, status, map[string]any{"status": "concluido", "version": version}, director, ""), http.StatusConflict, "Ainda há 2 entrega(s) em aberto")

	event := func(order int64, op, st string) {
		t.Helper()
		rr := authReq(a, http.MethodPost, "/api/delivery-orders/"+itoa(order)+"/events", map[string]any{"operation_id": op, "status": st}, director, "")
		if rr.Code != http.StatusCreated {
			t.Fatalf("event %s on %d status=%d body=%s", st, order, rr.Code, rr.Body.String())
		}
	}
	event(o1, "o1-rota", "em_rota")
	event(o1, "o1-chegou", "chegou")
	event(o1, "o1-entregue", "entregue")
	event(o2, "o2-rota", "em_rota")
	event(o2, "o2-falhou", "falhou")

	done := authReq(a, http.MethodPost, status, map[string]any{"status": "concluido", "version": version}, director, "")
	if done.Code != http.StatusOK {
		t.Fatalf("complete status=%d body=%s", done.Code, done.Body.String())
	}

	// A entrega que falhou volta numa rota nova, e a van fica livre de novo no dia.
	retry := routeTestCreate(t, a, director, "/api/routes", map[string]any{"day": "2026-10-16", "vehicle_id": van, "driver_id": joao})
	rr := authReq(a, http.MethodPut, "/api/routes/"+itoa(retry)+"/stops", map[string]any{"order_ids": []int64{o2}, "version": 1}, director, "")
	if rr.Code != http.StatusOK {
		t.Fatalf("failed order must be routable again, status=%d body=%s", rr.Code, rr.Body.String())
	}
	logisticsErrorIs(t, authReq(a, http.MethodPut, "/api/routes/"+itoa(retry)+"/stops", map[string]any{"order_ids": []int64{o1}, "version": 2}, director, ""), http.StatusBadRequest, "está entregue")

	var events int
	if err := a.db.QueryRow(`SELECT count(*) FROM delivery_events WHERE operation_id LIKE 'route:%'`).Scan(&events); err != nil || events != 4 {
		t.Fatalf("route events=%d err=%v, want 4 (two on release, two on start)", events, err)
	}
	list := logisticsList(t, authReq(a, http.MethodGet, "/api/routes?day=2026-10-16", nil, director, ""))
	if len(list) != 3 || !strings.Contains(list[0]["vehicle_ownership"].(string), "propria") {
		t.Fatalf("route list = %v", list)
	}
}

func TestRoutePermissions(t *testing.T) {
	a, _, _, _ := newStockTestApp(t)
	createTestUser(t, a, "route-manager", "secret", "Gerente", true)
	createTestUser(t, a, "route-stock", "secret", "Estoquista", true)
	manager := doLogin(t, a, "route-manager", "secret")
	stock := doLogin(t, a, "route-stock", "secret")

	if rr := authReq(a, http.MethodGet, "/api/routes", nil, manager, ""); rr.Code != http.StatusOK {
		t.Fatalf("manager list status=%d", rr.Code)
	}
	if rr := authReq(a, http.MethodPost, "/api/routes", map[string]any{"day": "2026-10-16", "vehicle_id": 1, "driver_id": 1}, manager, ""); rr.Code != http.StatusForbidden {
		t.Fatalf("manager create status=%d, want 403", rr.Code)
	}
	if rr := authReq(a, http.MethodGet, "/api/routes", nil, stock, ""); rr.Code != http.StatusForbidden {
		t.Fatalf("stock clerk list status=%d, want 403", rr.Code)
	}
}

func TestTwoRoutesCannotClaimTheSameOrderConcurrently(t *testing.T) {
	a, _, wh, _ := newStockTestApp(t)
	createTestUser(t, a, "route-race", "secret", "Diretor", true)
	director := doLogin(t, a, "route-race", "secret")
	customer := recipeTestCustomer(t, a, director, "Cliente Corrida")
	place := dispatchTestPlace(t, a, director, customer)
	arroz := lookupID(t, a, `SELECT id FROM inventory_items WHERE name='Arroz'`)
	order := routeTestCreate(t, a, director, "/api/delivery-orders", dispatchOrderBody("race-order", place, wh, []map[string]any{{"item_id": arroz, "quantity": "1"}}))
	var routes [2]int64
	for i, plate := range []string{"RAC1E01", "RAC1E02"} {
		vehicle := routeTestCreate(t, a, director, "/api/vehicles", map[string]any{"plate": plate, "description": "Van", "capacity_kg": "800"})
		driver := routeTestCreate(t, a, director, "/api/drivers", map[string]any{"name": "Motorista " + plate})
		routes[i] = routeTestCreate(t, a, director, "/api/routes", map[string]any{"day": "2026-10-16", "vehicle_id": vehicle, "driver_id": driver})
	}

	codes := make(chan int, 2)
	start := make(chan struct{})
	for _, route := range routes {
		go func(route int64) {
			<-start
			codes <- authReq(a, http.MethodPut, "/api/routes/"+itoa(route)+"/stops", map[string]any{"order_ids": []int64{order}, "version": 1}, director, "").Code
		}(route)
	}
	close(start)
	got := map[int]int{<-codes: 1}
	got[<-codes]++
	if got[http.StatusOK] != 1 || got[http.StatusConflict] != 1 {
		t.Fatalf("want one 200 and one 409, got %v", got)
	}
	var stops int
	if err := a.db.QueryRow(`SELECT count(*) FROM route_stops WHERE order_id=$1`, order).Scan(&stops); err != nil || stops != 1 {
		t.Fatalf("order in %d routes, want 1 (err=%v)", stops, err)
	}
}
