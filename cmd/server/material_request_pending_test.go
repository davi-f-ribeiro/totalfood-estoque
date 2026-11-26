package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"testing"
	"time"
)

// A lista mostra, por requisição, o que falta: linhas a separar, entregas sem
// recebimento e entregas com restante a confirmar. Cada passo do fluxo muda
// essas contagens, e o teste confere o estado real no banco a cada passo.
func TestMaterialRequestListPendingCounts(t *testing.T) {
	a, sku, wh, _ := newStockTestApp(t)
	createTestUser(t, a, "pending-cozinha", "secret", "Cozinha", true)
	createTestUser(t, a, "pending-estoquista", "secret", "Estoquista", true)
	cozinha := doLogin(t, a, "pending-cozinha", "secret")
	estoquista := doLogin(t, a, "pending-estoquista", "secret")
	ctx := context.Background()
	if _, err := a.createEntry(ctx, movementRequest{OperationID: "pending-seed", SKUID: sku, WarehouseID: wh, Quantity: "12", UnitCost: "5"}, "pending-seed"); err != nil {
		t.Fatal(err)
	}
	var item int64
	if err := a.db.QueryRow("SELECT item_id FROM product_skus WHERE id=$1", sku).Scan(&item); err != nil {
		t.Fatal(err)
	}
	future := time.Now().In(time.FixedZone("-03", -3*3600)).AddDate(0, 0, 1).Format("2006-01-02")
	created := authReq(a, http.MethodPost, "/api/material-requests", map[string]any{"operation_id": "pending-create", "unit_name": "Cozinha Central", "delivery_date": future, "production_date": future, "service": "lanche", "lines": []map[string]any{{"item_id": item, "quantity": "10"}}}, cozinha, "")
	if created.Code != 201 {
		t.Fatalf("create=%d %s", created.Code, created.Body.String())
	}
	id := int64(materialTestJSON(t, created.Body.Bytes())["id"].(float64))
	sid := strconv.FormatInt(id, 10)
	base := "/api/material-requests/" + sid
	detail := materialTestJSON(t, authReq(a, http.MethodGet, base, nil, cozinha, "").Body.Bytes())
	lineID := int64(detail["lines"].([]any)[0].(map[string]any)["id"].(float64))

	// Conferência por número: o filtro "#id" isola esta requisição dentro do banco de teste.
	pending := func(step string, wantSeparar, wantReceber, wantDivergencia int) {
		t.Helper()
		rr := authReq(a, http.MethodGet, "/api/material-requests/search?q="+url.QueryEscape("#"+sid), nil, cozinha, "")
		if rr.Code != 200 {
			t.Fatalf("%s: search=%d %s", step, rr.Code, rr.Body.String())
		}
		reqs := materialTestJSON(t, rr.Body.Bytes())["requests"].([]any)
		if len(reqs) != 1 {
			t.Fatalf("%s: want 1 request, got %d", step, len(reqs))
		}
		p := reqs[0].(map[string]any)["pending"].(map[string]any)
		got := [3]int{int(p["separar"].(float64)), int(p["receber"].(float64)), int(p["divergencia"].(float64))}
		if want := [3]int{wantSeparar, wantReceber, wantDivergencia}; got != want {
			t.Fatalf("%s: separar/receber/divergencia=%v, want %v", step, got, want)
		}
		// Fonte única: o saldo por linha do detalhe e a contagem da lista saem da mesma conta.
		dl := materialTestJSON(t, authReq(a, http.MethodGet, base, nil, cozinha, "").Body.Bytes())["lines"].([]any)[0].(map[string]any)
		toSeparate, err := strconv.ParseFloat(dl["to_separate"].(string), 64)
		if err != nil {
			t.Fatal(err)
		}
		if (toSeparate > 0) != (wantSeparar > 0) {
			t.Fatalf("%s: detalhe to_separate=%v, mas a lista conta separar=%d", step, toSeparate, wantSeparar)
		}
	}
	mustOK := func(step string, rr *httptest.ResponseRecorder, code int) {
		t.Helper()
		if rr.Code != code {
			t.Fatalf("%s: code=%d want %d: %s", step, rr.Code, code, rr.Body.String())
		}
	}

	pending("pedido novo", 1, 0, 0)

	r1 := authReq(a, http.MethodPost, base+"/reserve", map[string]any{"operation_id": "pending-reserve-1", "line_id": lineID, "sku_id": sku, "warehouse_id": wh, "quantity": "4"}, estoquista, "")
	mustOK("separa 4", r1, 201)
	rid1 := int64(materialTestJSON(t, r1.Body.Bytes())["id"].(float64))
	pending("separou 4 de 10", 1, 0, 0)

	r2 := authReq(a, http.MethodPost, base+"/reserve", map[string]any{"operation_id": "pending-reserve-2", "line_id": lineID, "sku_id": sku, "warehouse_id": wh, "quantity": "6"}, estoquista, "")
	mustOK("separa o restante", r2, 201)
	rid2 := int64(materialTestJSON(t, r2.Body.Bytes())["id"].(float64))
	pending("separou tudo", 0, 0, 0)

	d1 := authReq(a, http.MethodPost, base+"/deliver", map[string]any{"operation_id": "pending-deliver-1", "reservation_id": rid1, "quantity": "4"}, estoquista, "")
	mustOK("entrega 4", d1, 201)
	del1 := int64(materialTestJSON(t, d1.Body.Bytes())["id"].(float64))
	pending("entregou 4, falta confirmar", 0, 1, 0)

	mustOK("recebe 4", authReq(a, http.MethodPost, base+"/deliveries/"+strconv.FormatInt(del1, 10)+"/receive", map[string]any{}, cozinha, ""), 200)
	pending("recebeu a primeira entrega", 0, 0, 0)

	d2 := authReq(a, http.MethodPost, base+"/deliver", map[string]any{"operation_id": "pending-deliver-2", "reservation_id": rid2, "quantity": "6"}, estoquista, "")
	mustOK("entrega 6", d2, 201)
	del2 := int64(materialTestJSON(t, d2.Body.Bytes())["id"].(float64))
	pending("entregou o restante", 0, 1, 0)

	mustOK("recebe 5 de 6", authReq(a, http.MethodPost, base+"/deliveries/"+strconv.FormatInt(del2, 10)+"/receive", map[string]any{"received_quantity": "5", "reason": "Caixa avariada"}, cozinha, ""), 200)
	pending("recebeu com diferença de 1", 0, 0, 1)

	mustOK("concilia o 1 que faltava", authReq(a, http.MethodPost, base+"/deliveries/"+strconv.FormatInt(del2, 10)+"/reconcile", map[string]any{"operation_id": "pending-reconcile", "quantity": "1", "notes": "Chegou depois"}, cozinha, ""), 201)
	pending("diferença conciliada", 0, 0, 0)
}
