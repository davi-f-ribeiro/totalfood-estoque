package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"
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

// Diagnóstico de 16/11/2026 (requisição #6, Melão Japonês): item controlado por caixa não
// saía por requisição. A entrega passa a sair de caixas escolhidas, pelo peso atual delas.
func TestMaterialDeliveryFromCrates(t *testing.T) {
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
	// As seis caixas do caso real: 111,8 kg.
	receive := crateReceiveRequest{OperationID: "crate-req-receipt", SKUID: sku, WarehouseID: wh, UnitCost: "2",
		DocumentNumber: "999", SupplierName: "CEASA", Weights: []string{"19.300", "18.900", "22.000", "21.800", "20.900", "8.900"}}
	rr := authReq(a, http.MethodPost, "/api/crates/receive", receive, director, "")
	if rr.Code != 200 {
		t.Fatalf("receive crates=%d %s", rr.Code, rr.Body.String())
	}
	var out struct {
		Crates []crateRow `json:"crates"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &out); err != nil || len(out.Crates) != 6 {
		t.Fatalf("crates=%+v err=%v", out, err)
	}
	ids := make([]int64, 6)
	for i, c := range out.Crates {
		ids[i] = c.ID
	}
	stock := func() string {
		t.Helper()
		var q string
		if err := db.QueryRow(`SELECT COALESCE(sum(CASE WHEN direction='IN' THEN quantity ELSE -quantity END),0)::numeric(14,3)::text FROM movement_lines WHERE sku_id=$1 AND warehouse_id=$2`, sku, wh).Scan(&q); err != nil {
			t.Fatal(err)
		}
		return q
	}
	crate := func(id int64) (string, string) {
		t.Helper()
		var w, st string
		if err := db.QueryRow(`SELECT current_weight::text,status FROM stock_crates WHERE id=$1`, id).Scan(&w, &st); err != nil {
			t.Fatal(err)
		}
		return w, st
	}

	// Pedido de 106 kg, reserva de 102,9 kg (cinco caixas).
	base, line := materialOpenRequest(t, a, sku, "crate", "106", cozinha)
	res := authReq(a, http.MethodPost, base+"/reserve", map[string]any{"operation_id": "crate-req-res", "line_id": line, "sku_id": sku, "warehouse_id": wh, "quantity": "102.9"}, estoquista, "")
	if res.Code != 201 {
		t.Fatalf("reserve=%d %s", res.Code, res.Body.String())
	}
	rid := int64(materialTestJSON(t, res.Body.Bytes())["id"].(float64))
	remaining := func(r int64) string {
		t.Helper()
		var q string
		if err := db.QueryRow(`SELECT remaining_quantity::text FROM material_request_reservations WHERE id=$1`, r).Scan(&q); err != nil {
			t.Fatal(err)
		}
		return q
	}
	deliver := func(r int64, op, qty string, crates ...int64) (int, string) {
		body := map[string]any{"operation_id": op, "reservation_id": r, "quantity": qty}
		if len(crates) > 0 {
			body["crate_ids"] = crates
		}
		rr := authReq(a, http.MethodPost, base+"/deliver", body, estoquista, "")
		return rr.Code, rr.Body.String()
	}
	five := ids[:5]

	// Sem caixa: recusa definitiva e com orientação (antes era erro 500 e "Enviar de novo").
	if code, body := deliver(rid, "crate-req-none", "102.9"); code != 409 || !strings.Contains(body, "controlado por caixa") {
		t.Fatalf("delivery without crates=%d %s", code, body)
	}
	// Caixas que somam menos que a quantidade, e várias caixas com saída parcial: recusadas.
	if code, body := deliver(rid, "crate-req-short", "102.9", ids[0], ids[1], ids[2], ids[3]); code != 409 {
		t.Fatalf("crates below the quantity=%d %s", code, body)
	}
	if code, body := deliver(rid, "crate-req-partial-many", "100", five...); code != 409 {
		t.Fatalf("partial delivery over several crates=%d %s", code, body)
	}
	if code, body := deliver(rid, "crate-req-dup", "38.6", ids[0], ids[0]); code != 400 {
		t.Fatalf("same crate twice in one delivery=%d %s", code, body)
	}
	// Nada mudou com as recusas (rollback completo).
	if w, st := crate(ids[0]); remaining(rid) != "102.900" || stock() != "111.800" || st != "ACTIVE" || !equalCrateWeights(w, "19.3") {
		t.Fatalf("refused deliveries changed state: remaining=%s stock=%s crate=%s %s", remaining(rid), stock(), w, st)
	}

	// Caixa repesada depois da reserva: vale o peso atual, não o antigo.
	if rr := authReq(a, http.MethodPost, fmt.Sprintf("/api/crates/%d/weigh", ids[4]), crateWeighRequest{OperationID: "crate-req-reweigh", Weight: "20.400"}, director, ""); rr.Code != 200 {
		t.Fatalf("reweigh=%d %s", rr.Code, rr.Body.String())
	}
	if code, body := deliver(rid, "crate-req-stale", "102.9", five...); code != 409 {
		t.Fatalf("delivery with the old weight=%d %s", code, body)
	}

	// Cinco caixas inteiras em uma entrega: 102,4 kg pelo peso atual.
	code, body := deliver(rid, "crate-req-five", "102.4", five...)
	if code != 201 {
		t.Fatalf("five crates=%d %s", code, body)
	}
	var first map[string]any
	_ = json.Unmarshal([]byte(body), &first)
	if code, body := deliver(rid, "crate-req-five", "102.4", five...); code != 200 {
		t.Fatalf("delivery replay=%d %s", code, body)
	}
	movement := int64(first["movement_id"].(float64))
	var lines, events, moves int
	if err := db.QueryRow(`SELECT (SELECT count(*) FROM movement_lines WHERE movement_id=$1),(SELECT count(*) FROM stock_crate_events WHERE movement_id=$1),(SELECT count(*) FROM material_request_deliveries)`, movement).Scan(&lines, &events, &moves); err != nil {
		t.Fatal(err)
	}
	if lines != 5 || events != 5 || moves != 1 {
		t.Fatalf("one movement with one line and one crate event per crate: lines=%d events=%d deliveries=%d", lines, events, moves)
	}
	for _, id := range five {
		if w, st := crate(id); st != "EMPTY" || !equalCrateWeights(w, "0") {
			t.Fatalf("crate %d after delivery: %s %s", id, w, st)
		}
	}
	if remaining(rid) != "0.500" || stock() != "8.900" {
		t.Fatalf("after delivery: remaining=%s stock=%s", remaining(rid), stock())
	}
	d := materialStatus(t, a, base, cozinha)
	del := d["deliveries"].([]any)[0].(map[string]any)
	if d["status"] != "ENTREGA_PARCIAL" || del["quantity"] != "102.400" || len(del["crates"].([]any)) != 5 || del["received"] != false {
		t.Fatalf("request after delivery: status=%v delivery=%+v", d["status"], del)
	}
	found := false
	for _, e := range d["events"].([]any) {
		x := e.(map[string]any)
		if x["action"] == "ENTREGUE" && strings.Contains(x["details"].(string), crateCode(ids[0])) {
			found = true
		}
	}
	if !found {
		t.Fatalf("delivery event does not name the crates: %+v", d["events"])
	}

	// Caixa já entregue (vazia) não sai de novo.
	if code, body := deliver(rid, "crate-req-again", "0.5", ids[0]); code != 409 {
		t.Fatalf("emptied crate delivered again=%d %s", code, body)
	}

	// Duas entregas ao mesmo tempo na mesma caixa (8,9 kg): 0,5 de uma reserva e 8,4 de outra.
	// As duas entram, uma depois da outra, e a caixa termina vazia, sem saldo negativo.
	res2 := authReq(a, http.MethodPost, base+"/reserve", map[string]any{"operation_id": "crate-req-res2", "line_id": line, "sku_id": sku, "warehouse_id": wh, "quantity": "8.4"}, estoquista, "")
	if res2.Code != 201 {
		t.Fatalf("second reserve=%d %s", res2.Code, res2.Body.String())
	}
	rid2 := int64(materialTestJSON(t, res2.Body.Bytes())["id"].(float64))
	var wg sync.WaitGroup
	codes := make([]int, 2)
	bodies := make([]string, 2)
	wg.Add(2)
	go func() { defer wg.Done(); codes[0], bodies[0] = deliver(rid, "crate-req-race-a", "0.5", ids[5]) }()
	go func() { defer wg.Done(); codes[1], bodies[1] = deliver(rid2, "crate-req-race-b", "8.4", ids[5]) }()
	wg.Wait()
	// A que pega a caixa primeiro pode ser a de 8,4 kg; então a de 0,5 sai da mesma caixa.
	// Se a de 0,5 for primeiro, a de 8,4 (saída parcial de uma caixa com 8,4) também entra.
	if codes[0] != 201 || codes[1] != 201 {
		t.Fatalf("concurrent deliveries from one crate: %d %s / %d %s", codes[0], bodies[0], codes[1], bodies[1])
	}
	if w, st := crate(ids[5]); st != "EMPTY" || !equalCrateWeights(w, "0") || stock() != "0.000" {
		t.Fatalf("after concurrent deliveries: crate=%s %s stock=%s", w, st, stock())
	}
	if d = materialStatus(t, a, base, cozinha); d["status"] != "ENTREGUE" || len(d["deliveries"].([]any)) != 3 {
		t.Fatalf("request after all deliveries: status=%v deliveries=%d", d["status"], len(d["deliveries"].([]any)))
	}
	// A cozinha vê as entregas e confirma o recebimento.
	for _, v := range d["deliveries"].([]any) {
		idv := int64(v.(map[string]any)["id"].(float64))
		if rr := authReq(a, http.MethodPost, base+"/deliveries/"+strconv.FormatInt(idv, 10)+"/receive", map[string]any{}, cozinha, ""); rr.Code != 200 {
			t.Fatalf("receive=%d %s", rr.Code, rr.Body.String())
		}
	}
	if d = materialStatus(t, a, base, cozinha); d["status"] != "RECEBIDA" {
		t.Fatalf("status after receipt=%v", d["status"])
	}
}
