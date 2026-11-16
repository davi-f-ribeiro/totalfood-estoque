package main

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"
)

// materialOpenRequest cria uma requisição de `ask` do item do SKU e devolve a URL base e a linha.
func materialOpenRequest(t *testing.T, a *app, sku int64, tag, ask string, cozinha loginData) (string, int64) {
	t.Helper()
	var item int64
	if err := a.db.QueryRow("SELECT item_id FROM product_skus WHERE id=$1", sku).Scan(&item); err != nil {
		t.Fatal(err)
	}
	day := time.Now().In(time.FixedZone("-03", -3*3600)).AddDate(0, 0, 1).Format("2006-01-02")
	body := map[string]any{"operation_id": "vw-create-" + tag, "unit_name": "Cozinha Central", "delivery_date": day, "production_date": day, "service": "almoco", "notes": "", "lines": []map[string]any{{"item_id": item, "quantity": ask}}}
	created := authReq(a, http.MethodPost, "/api/material-requests", body, cozinha, "")
	if created.Code != 201 {
		t.Fatalf("create=%d %s", created.Code, created.Body.String())
	}
	base := "/api/material-requests/" + strconv.FormatInt(int64(materialTestJSON(t, created.Body.Bytes())["id"].(float64)), 10)
	d := materialStatus(t, a, base, cozinha)
	return base, int64(d["lines"].([]any)[0].(map[string]any)["id"].(float64))
}

func materialActiveReservations(t *testing.T, a *app, base string) int {
	t.Helper()
	var n int
	id := base[strings.LastIndex(base, "/")+1:]
	if err := a.db.QueryRow("SELECT count(*) FROM material_request_reservations res JOIN material_request_lines l ON l.id=res.request_line_id WHERE l.request_id=$1 AND res.status='ATIVA'", id).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// Itens sem peso padrão: a cozinha aceita o que chegou e encerra; o estoque pode entregar
// o peso real, até 20% acima do pedido em KG.
func TestMaterialCloseWithDifferenceAndWeightTolerance(t *testing.T) {
	a, sku, wh, _ := newStockTestApp(t)
	if _, err := a.db.Exec("UPDATE inventory_items SET unit_code='KG' WHERE id=(SELECT item_id FROM product_skus WHERE id=$1)", sku); err != nil {
		t.Fatal(err)
	}
	createTestUser(t, a, "vw-cozinha", "secret", "Cozinha", true)
	createTestUser(t, a, "vw-estoquista", "secret", "Estoquista", true)
	cozinha := doLogin(t, a, "vw-cozinha", "secret")
	estoquista := doLogin(t, a, "vw-estoquista", "secret")
	if _, err := a.createEntry(context.Background(), movementRequest{OperationID: "vw-seed", SKUID: sku, WarehouseID: wh, Quantity: "500", UnitCost: "5"}, "vw-seed"); err != nil {
		t.Fatal(err)
	}

	// Pedido de 106, entregue 104,3: a cozinha aceita e encerra.
	base, line := materialOpenRequest(t, a, sku, "a", "106", cozinha)
	if rr := authReq(a, http.MethodPost, base+"/close", map[string]any{}, cozinha, ""); rr.Code != 409 {
		t.Fatalf("close without delivery=%d %s", rr.Code, rr.Body.String())
	}
	res := authReq(a, http.MethodPost, base+"/reserve", map[string]any{"operation_id": "vw-res-a", "line_id": line, "sku_id": sku, "warehouse_id": wh, "quantity": "106"}, estoquista, "")
	if res.Code != 201 {
		t.Fatalf("reserve=%d %s", res.Code, res.Body.String())
	}
	rid := int64(materialTestJSON(t, res.Body.Bytes())["id"].(float64))
	del := authReq(a, http.MethodPost, base+"/deliver", map[string]any{"operation_id": "vw-del-a", "reservation_id": rid, "quantity": "104.3"}, estoquista, "")
	if del.Code != 201 {
		t.Fatalf("deliver=%d %s", del.Code, del.Body.String())
	}
	delID := int64(materialTestJSON(t, del.Body.Bytes())["id"].(float64))
	if rr := authReq(a, http.MethodPost, base+"/close", map[string]any{}, cozinha, ""); rr.Code != 409 {
		t.Fatalf("close before checking the delivery=%d %s", rr.Code, rr.Body.String())
	}
	if rr := authReq(a, http.MethodPost, base+"/deliveries/"+strconv.FormatInt(delID, 10)+"/receive", map[string]any{}, cozinha, ""); rr.Code != 200 {
		t.Fatalf("receive=%d %s", rr.Code, rr.Body.String())
	}
	if d := materialStatus(t, a, base, cozinha); d["status"] != "ENTREGA_PARCIAL" {
		t.Fatalf("status before close=%v", d["status"])
	}
	if rr := authReq(a, http.MethodPost, base+"/close", map[string]any{}, estoquista, ""); rr.Code != 403 {
		t.Fatalf("estoquista closed=%d %s", rr.Code, rr.Body.String())
	}
	if rr := authReq(a, http.MethodPost, base+"/close", map[string]any{"notes": "Melão veio inteiro"}, cozinha, ""); rr.Code != 200 {
		t.Fatalf("close=%d %s", rr.Code, rr.Body.String())
	}
	if rr := authReq(a, http.MethodPost, base+"/close", map[string]any{}, cozinha, ""); rr.Code != 200 {
		t.Fatalf("close replay=%d %s", rr.Code, rr.Body.String())
	}
	d := materialStatus(t, a, base, cozinha)
	closed := 0
	for _, e := range d["events"].([]any) {
		if e.(map[string]any)["action"] == "ENCERRADA" {
			closed++
		}
	}
	if d["status"] != "RECEBIDA" || d["closed_by"] != "vw-cozinha" || d["close_notes"] != "Melão veio inteiro" || closed != 1 || materialActiveReservations(t, a, base) != 0 {
		t.Fatalf("after close: status=%v closed_by=%v events=%d active reservations=%d", d["status"], d["closed_by"], closed, materialActiveReservations(t, a, base))
	}
	if rr := authReq(a, http.MethodPost, base+"/deliver", map[string]any{"operation_id": "vw-del-a2", "reservation_id": rid, "quantity": "1"}, estoquista, ""); rr.Code != 409 {
		t.Fatalf("delivery after close=%d %s", rr.Code, rr.Body.String())
	}
	assertStock(t, a, sku, wh, "395.700", "5.0000") // só a entrega saiu do estoque

	// Pedido de 106, entregue 108,7: em KG o peso real pode passar do pedido, até 20%.
	base, line = materialOpenRequest(t, a, sku, "b", "106", cozinha)
	res = authReq(a, http.MethodPost, base+"/reserve", map[string]any{"operation_id": "vw-res-b", "line_id": line, "sku_id": sku, "warehouse_id": wh, "quantity": "110"}, estoquista, "")
	if res.Code != 201 {
		t.Fatalf("reserve above the request=%d %s", res.Code, res.Body.String())
	}
	rid = int64(materialTestJSON(t, res.Body.Bytes())["id"].(float64))
	if rr := authReq(a, http.MethodPost, base+"/reserve", map[string]any{"operation_id": "vw-res-b2", "line_id": line, "sku_id": sku, "warehouse_id": wh, "quantity": "20"}, estoquista, ""); rr.Code != 409 {
		t.Fatalf("reserve above the tolerance=%d %s", rr.Code, rr.Body.String())
	}
	del = authReq(a, http.MethodPost, base+"/deliver", map[string]any{"operation_id": "vw-del-b", "reservation_id": rid, "quantity": "108.7"}, estoquista, "")
	if del.Code != 201 {
		t.Fatalf("deliver above the request=%d %s", del.Code, del.Body.String())
	}
	delID = int64(materialTestJSON(t, del.Body.Bytes())["id"].(float64))
	if d = materialStatus(t, a, base, cozinha); d["status"] != "ENTREGUE" {
		t.Fatalf("status after delivering above the request=%v", d["status"])
	}
	if rr := authReq(a, http.MethodPost, base+"/deliveries/"+strconv.FormatInt(delID, 10)+"/receive", map[string]any{}, cozinha, ""); rr.Code != 200 {
		t.Fatalf("receive=%d %s", rr.Code, rr.Body.String())
	}
	if d = materialStatus(t, a, base, cozinha); d["status"] != "RECEBIDA" || materialActiveReservations(t, a, base) != 0 {
		t.Fatalf("after full receipt: status=%v active reservations=%d", d["status"], materialActiveReservations(t, a, base))
	}
	assertStock(t, a, sku, wh, "287.000", "5.0000")

	// Item que não é em KG continua limitado ao pedido.
	if _, err := a.db.Exec("UPDATE inventory_items SET unit_code='UN' WHERE id=(SELECT item_id FROM product_skus WHERE id=$1)", sku); err != nil {
		t.Fatal(err)
	}
	base, line = materialOpenRequest(t, a, sku, "c", "10", cozinha)
	if rr := authReq(a, http.MethodPost, base+"/reserve", map[string]any{"operation_id": "vw-res-c", "line_id": line, "sku_id": sku, "warehouse_id": wh, "quantity": "11"}, estoquista, ""); rr.Code != 409 {
		t.Fatalf("non-KG reserve above the request=%d %s", rr.Code, rr.Body.String())
	}
}

// Item controlado por caixa (hortifruti): a entrega da requisição sai de uma caixa escolhida.
func TestMaterialDeliveryFromCrate(t *testing.T) {
	a, _, _, _ := newStockTestApp(t)
	db := a.db
	createTestUser(t, a, "crate-req-director", "secret", "Diretor", true)
	createTestUser(t, a, "crate-req-cozinha", "secret", "Cozinha", true)
	createTestUser(t, a, "crate-req-estoquista", "secret", "Estoquista", true)
	director := doLogin(t, a, "crate-req-director", "secret")
	cozinha := doLogin(t, a, "crate-req-cozinha", "secret")
	estoquista := doLogin(t, a, "crate-req-estoquista", "secret")
	sku := crateTestSKU(t, db)
	var wh int64
	if err := db.QueryRow(`SELECT id FROM warehouses WHERE name='VR'`).Scan(&wh); err != nil {
		t.Fatal(err)
	}
	receive := crateReceiveRequest{OperationID: "crate-req-receipt", SKUID: sku, WarehouseID: wh, UnitCost: "2",
		DocumentNumber: "999", SupplierName: "CEASA", Weights: []string{"20.000", "22.000"}}
	rr := authReq(a, http.MethodPost, "/api/crates/receive", receive, director, "")
	if rr.Code != 200 {
		t.Fatalf("receive crates=%d %s", rr.Code, rr.Body.String())
	}
	var out struct {
		Crates []crateRow `json:"crates"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &out); err != nil || len(out.Crates) != 2 {
		t.Fatalf("crates=%+v err=%v", out, err)
	}
	first, second := out.Crates[0].ID, out.Crates[1].ID

	base, line := materialOpenRequest(t, a, sku, "crate", "30", cozinha)
	res := authReq(a, http.MethodPost, base+"/reserve", map[string]any{"operation_id": "crate-req-res", "line_id": line, "sku_id": sku, "warehouse_id": wh, "quantity": "30"}, estoquista, "")
	if res.Code != 201 {
		t.Fatalf("reserve=%d %s", res.Code, res.Body.String())
	}
	rid := int64(materialTestJSON(t, res.Body.Bytes())["id"].(float64))
	deliver := func(op, qty string, crate int64) (int, string) {
		body := map[string]any{"operation_id": op, "reservation_id": rid, "quantity": qty}
		if crate > 0 {
			body["crate_id"] = crate
		}
		rr := authReq(a, http.MethodPost, base+"/deliver", body, estoquista, "")
		return rr.Code, rr.Body.String()
	}
	// Sem caixa, a saída é recusada com orientação (antes era erro genérico).
	if code, body := deliver("crate-req-none", "20", 0); code != 409 || !strings.Contains(body, "controlado por caixa") {
		t.Fatalf("delivery without a crate=%d %s", code, body)
	}
	if code, body := deliver("crate-req-over", "21", first); code != 409 {
		t.Fatalf("delivery above the crate weight=%d %s", code, body)
	}
	if code, body := deliver("crate-req-1", "20", first); code != 201 {
		t.Fatalf("whole crate=%d %s", code, body)
	}
	if code, body := deliver("crate-req-1", "20", first); code != 200 {
		t.Fatalf("delivery replay=%d %s", code, body)
	}
	if code, body := deliver("crate-req-2", "10", second); code != 201 {
		t.Fatalf("part of a crate=%d %s", code, body)
	}
	var w1, s1, w2, s2 string
	if err := db.QueryRow(`SELECT current_weight::text,status FROM stock_crates WHERE id=$1`, first).Scan(&w1, &s1); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT current_weight::text,status FROM stock_crates WHERE id=$1`, second).Scan(&w2, &s2); err != nil {
		t.Fatal(err)
	}
	if s1 != "EMPTY" || !equalCrateWeights(w1, "0") || s2 != "ACTIVE" || !equalCrateWeights(w2, "12") {
		t.Fatalf("crates after delivery: %s %s / %s %s", w1, s1, w2, s2)
	}
	var pendingSecond, pendingFirst int
	if err := db.QueryRow(`SELECT count(*) FILTER (WHERE crate_id=$1 AND status='PENDING'),count(*) FILTER (WHERE crate_id=$2 AND status='PENDING') FROM print_jobs`, second, first).Scan(&pendingSecond, &pendingFirst); err != nil {
		t.Fatal(err)
	}
	if pendingSecond != 1 || pendingFirst != 0 {
		t.Fatalf("labels: second crate pending=%d (want 1), emptied crate pending=%d (want 0)", pendingSecond, pendingFirst)
	}
	if d := materialStatus(t, a, base, cozinha); d["status"] != "ENTREGUE" || len(d["deliveries"].([]any)) != 2 {
		t.Fatalf("request after crate deliveries: status=%v deliveries=%v", d["status"], d["deliveries"])
	}
	var stock string
	if err := db.QueryRow(`SELECT COALESCE(sum(CASE WHEN direction='IN' THEN quantity ELSE -quantity END),0)::numeric(14,3)::text FROM movement_lines WHERE sku_id=$1 AND warehouse_id=$2`, sku, wh).Scan(&stock); err != nil || stock != "12.000" {
		t.Fatalf("stock after crate deliveries=%s err=%v", stock, err)
	}
}
