package main

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

func TestDriverSeesOnlyOwnRouteAndActsOnlyOnItsStops(t *testing.T) {
	a, _, wh, _ := newStockTestApp(t)
	createTestUser(t, a, "drv-director", "secret", "Diretor", true)
	director := doLogin(t, a, "drv-director", "secret")
	ritaUser := createTestUser(t, a, "rita", "secret", "Motorista", true)
	beto := createTestUser(t, a, "beto", "secret", "Motorista", true)
	createTestUser(t, a, "sem-cadastro", "secret", "Motorista", true)
	customer := recipeTestCustomer(t, a, director, "Cliente Motorista")
	place := dispatchTestPlace(t, a, director, customer)
	arroz := lookupID(t, a, `SELECT id FROM inventory_items WHERE name='Arroz'`)
	line := []map[string]any{{"item_id": arroz, "quantity": "1"}}

	van := routeTestCreate(t, a, director, "/api/vehicles", map[string]any{"plate": "DRV1A11", "description": "Van", "capacity_kg": "900"})
	moto := routeTestCreate(t, a, director, "/api/vehicles", map[string]any{"plate": "DRV1A12", "description": "Fiorino", "capacity_kg": "600"})
	rita := routeTestCreate(t, a, director, "/api/drivers", map[string]any{"name": "Rita", "user_id": ritaUser})
	betoDriver := routeTestCreate(t, a, director, "/api/drivers", map[string]any{"name": "Beto", "user_id": beto})
	mine := routeTestCreate(t, a, director, "/api/delivery-orders", dispatchOrderBody("drv-mine", place, wh, line))
	theirs := routeTestCreate(t, a, director, "/api/delivery-orders", dispatchOrderBody("drv-theirs", place, wh, line))

	build := func(vehicle, driver, order int64) int64 {
		route := routeTestCreate(t, a, director, "/api/routes", map[string]any{"day": "2026-10-16", "vehicle_id": vehicle, "driver_id": driver})
		base := "/api/routes/" + itoa(route)
		if rr := authReq(a, http.MethodPut, base+"/stops", map[string]any{"order_ids": []int64{order}, "version": 1}, director, ""); rr.Code != http.StatusOK {
			t.Fatalf("stops status=%d body=%s", rr.Code, rr.Body.String())
		}
		if rr := authReq(a, http.MethodPost, base+"/status", map[string]any{"status": "liberado", "version": 2}, director, ""); rr.Code != http.StatusOK {
			t.Fatalf("release status=%d body=%s", rr.Code, rr.Body.String())
		}
		return route
	}
	myRoute := build(van, rita, mine)
	theirRoute := build(moto, betoDriver, theirs)

	ritaLogin := doLogin(t, a, "rita", "secret")
	betoLogin := doLogin(t, a, "beto", "secret")
	nobody := doLogin(t, a, "sem-cadastro", "secret")

	got := authReq(a, http.MethodGet, "/api/driver/route?day=2026-10-16", nil, ritaLogin, "")
	var view struct {
		Route struct {
			ID      int64 `json:"id"`
			Status  string
			Version int64
			Stops   []struct {
				OrderID int64 `json:"order_id"`
			}
		}
	}
	if err := json.Unmarshal(got.Body.Bytes(), &view); err != nil || view.Route.ID != myRoute || len(view.Route.Stops) != 1 || view.Route.Stops[0].OrderID != mine {
		t.Fatalf("driver route status=%d body=%s", got.Code, got.Body.String())
	}
	if strings.Contains(got.Body.String(), "tracking_token") {
		t.Fatal("driver must not see the confirmation code")
	}
	if rr := authReq(a, http.MethodGet, "/api/driver/route?day=2026-10-17", nil, ritaLogin, ""); rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), `"route":null`) {
		t.Fatalf("other day must be empty, status=%d body=%s", rr.Code, rr.Body.String())
	}
	logisticsErrorIs(t, authReq(a, http.MethodGet, "/api/driver/route", nil, nobody, ""), http.StatusNotFound, "não está ligado a um motorista")

	routeStatus := "/api/driver/routes/" + itoa(myRoute) + "/status"
	logisticsErrorIs(t, authReq(a, http.MethodPost, routeStatus, map[string]any{"status": "em_andamento", "version": view.Route.Version}, betoLogin, ""), http.StatusForbidden, "não é sua")
	logisticsErrorIs(t, authReq(a, http.MethodPost, routeStatus, map[string]any{"status": "cancelado", "version": view.Route.Version}, ritaLogin, ""), http.StatusForbidden, "iniciar ou concluir")
	// Antes de iniciar a rota a entrega ainda não está "em andamento" para o motorista.
	logisticsErrorIs(t, authReq(a, http.MethodPost, "/api/driver/orders/"+itoa(mine)+"/events", map[string]any{"operation_id": "early", "status": "em_rota"}, ritaLogin, ""), http.StatusForbidden, "não está na sua rota em andamento")
	if rr := authReq(a, http.MethodPost, routeStatus, map[string]any{"status": "em_andamento", "version": view.Route.Version}, ritaLogin, ""); rr.Code != http.StatusOK {
		t.Fatalf("driver start status=%d body=%s", rr.Code, rr.Body.String())
	}
	if rr := authReq(a, http.MethodPost, "/api/driver/routes/"+itoa(theirRoute)+"/status", map[string]any{"status": "em_andamento", "version": 3}, betoLogin, ""); rr.Code != http.StatusOK {
		t.Fatalf("beto start status=%d body=%s", rr.Code, rr.Body.String())
	}

	events := "/api/driver/orders/" + itoa(mine) + "/events"
	logisticsErrorIs(t, authReq(a, http.MethodPost, events, map[string]any{"operation_id": "x1", "status": "cancelado"}, ritaLogin, ""), http.StatusForbidden, "saída para a parada")
	logisticsErrorIs(t, authReq(a, http.MethodPost, "/api/driver/orders/"+itoa(theirs)+"/events", map[string]any{"operation_id": "x2", "status": "em_rota"}, ritaLogin, ""), http.StatusForbidden, "não está na sua rota")
	for _, st := range []string{"em_rota", "chegou"} {
		if rr := authReq(a, http.MethodPost, events, map[string]any{"operation_id": "rita-" + st, "status": st, "occurred_at": "2026-10-16T10:05:00-03:00"}, ritaLogin, ""); rr.Code != http.StatusCreated {
			t.Fatalf("%s status=%d body=%s", st, rr.Code, rr.Body.String())
		}
	}
	var tok string
	if err := a.db.QueryRow(`SELECT tracking_token FROM delivery_orders WHERE id=$1`, mine).Scan(&tok); err != nil {
		t.Fatal(err)
	}
	rr := authReq(a, http.MethodPost, "/api/driver/orders/"+itoa(mine)+"/deliver", nil, ritaLogin, "")
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("deliver without form must be 400, got %d", rr.Code)
	}
	ok := deliverReq(a, mine, map[string]string{"operation_id": "rita-done", "kind": "codigo", "code": tok}, nil, ritaLogin)
	if ok.Code != http.StatusForbidden {
		// deliverReq aponta para a rota do despacho; o motorista não tem essa permissão.
		t.Fatalf("driver on dispatch deliver endpoint must be 403, got %d", ok.Code)
	}
	if rr := authReq(a, http.MethodGet, "/api/delivery-orders", nil, ritaLogin, ""); rr.Code != http.StatusForbidden {
		t.Fatalf("driver on dispatch list status=%d, want 403", rr.Code)
	}
	var occurred string
	if err := a.db.QueryRow(`SELECT to_char(occurred_at AT TIME ZONE 'UTC','HH24:MI') FROM delivery_events WHERE operation_id='rita-chegou'`).Scan(&occurred); err != nil || occurred != "13:05" {
		t.Fatalf("offline event must keep the phone time, got %q err=%v", occurred, err)
	}
}

func TestDriverDeliversThroughOwnEndpoint(t *testing.T) {
	a, _, wh, _ := newStockTestApp(t)
	createTestUser(t, a, "drv2-director", "secret", "Diretor", true)
	director := doLogin(t, a, "drv2-director", "secret")
	user := createTestUser(t, a, "drv2", "secret", "Motorista", true)
	customer := recipeTestCustomer(t, a, director, "Cliente Entrega Motorista")
	place := dispatchTestPlace(t, a, director, customer)
	arroz := lookupID(t, a, `SELECT id FROM inventory_items WHERE name='Arroz'`)
	van := routeTestCreate(t, a, director, "/api/vehicles", map[string]any{"plate": "DRV2A11", "description": "Van", "capacity_kg": "900"})
	driver := routeTestCreate(t, a, director, "/api/drivers", map[string]any{"name": "Drv2", "user_id": user})
	order := routeTestCreate(t, a, director, "/api/delivery-orders", dispatchOrderBody("drv2-order", place, wh, []map[string]any{{"item_id": arroz, "quantity": "1"}}))
	route := routeTestCreate(t, a, director, "/api/routes", map[string]any{"day": "2026-10-16", "vehicle_id": van, "driver_id": driver})
	base := "/api/routes/" + itoa(route)
	for i, step := range []map[string]any{
		{"path": base + "/stops", "method": http.MethodPut, "body": map[string]any{"order_ids": []int64{order}, "version": 1}},
		{"path": base + "/status", "method": http.MethodPost, "body": map[string]any{"status": "liberado", "version": 2}},
		{"path": base + "/status", "method": http.MethodPost, "body": map[string]any{"status": "em_andamento", "version": 3}},
	} {
		if rr := authReq(a, step["method"].(string), step["path"].(string), step["body"], director, ""); rr.Code != http.StatusOK {
			t.Fatalf("step %d status=%d body=%s", i, rr.Code, rr.Body.String())
		}
	}
	login := doLogin(t, a, "drv2", "secret")
	for _, st := range []string{"em_rota", "chegou"} {
		if rr := authReq(a, http.MethodPost, "/api/driver/orders/"+itoa(order)+"/events", map[string]any{"operation_id": "d2-" + st, "status": st}, login, ""); rr.Code != http.StatusCreated {
			t.Fatalf("%s status=%d", st, rr.Code)
		}
	}
	rr := driverDeliverReq(a, order, map[string]string{"operation_id": "d2-done", "kind": "assinatura", "receiver_name": "Copa"}, tinyPNG(t), login)
	if rr.Code != http.StatusCreated || orderStatus(t, a, order) != "entregue" {
		t.Fatalf("driver deliver status=%d body=%s", rr.Code, rr.Body.String())
	}
	if rr := authReq(a, http.MethodPost, "/api/driver/routes/"+itoa(route)+"/status", map[string]any{"status": "concluido", "version": 4}, login, ""); rr.Code != http.StatusOK {
		t.Fatalf("driver finish status=%d body=%s", rr.Code, rr.Body.String())
	}

	page := authReq(a, http.MethodGet, "/driver", nil, login, "")
	if page.Code != http.StatusOK || !strings.Contains(page.Body.String(), `id="driverRoot"`) || strings.Contains(page.Body.String(), `href="/dispatch"`) {
		t.Fatalf("driver page status=%d", page.Code)
	}
	sw := authReq(a, http.MethodGet, "/driver-sw.js", nil, loginData{}, "")
	if sw.Code != http.StatusOK || !strings.HasPrefix(sw.Header().Get("Content-Type"), "text/javascript") || !strings.Contains(sw.Body.String(), "driver-v1") {
		t.Fatalf("service worker status=%d type=%s", sw.Code, sw.Header().Get("Content-Type"))
	}
	home := authReq(a, http.MethodGet, "/", nil, login, "")
	if home.Code != http.StatusSeeOther || home.Header().Get("Location") != "/driver" {
		t.Fatalf("driver home redirect = %d %s", home.Code, home.Header().Get("Location"))
	}
}
