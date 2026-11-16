package main

import (
	"context"
	"net/http"
	"strconv"
	"sync"
	"testing"
	"time"
)

// materialDeliveredRequest cria uma requisição, reserva e entrega `qty` do SKU de teste.
// Devolve a URL base da requisição e o id da entrega.
func materialDeliveredRequest(t *testing.T, a *app, sku, wh int64, tag, qty string, cozinha, estoquista loginData) (string, int64) {
	t.Helper()
	var item int64
	if err := a.db.QueryRow("SELECT item_id FROM product_skus WHERE id=$1", sku).Scan(&item); err != nil {
		t.Fatal(err)
	}
	day := time.Now().In(time.FixedZone("-03", -3*3600)).AddDate(0, 0, 1).Format("2006-01-02")
	body := map[string]any{"operation_id": "partial-create-" + tag, "unit_name": "Cozinha Central", "delivery_date": day, "production_date": day, "service": "almoco", "notes": "", "lines": []map[string]any{{"item_id": item, "quantity": qty}}}
	created := authReq(a, http.MethodPost, "/api/material-requests", body, cozinha, "")
	if created.Code != 201 {
		t.Fatalf("create=%d %s", created.Code, created.Body.String())
	}
	id := int64(materialTestJSON(t, created.Body.Bytes())["id"].(float64))
	base := "/api/material-requests/" + strconv.FormatInt(id, 10)
	d := materialTestJSON(t, authReq(a, http.MethodGet, base, nil, cozinha, "").Body.Bytes())
	lineID := int64(d["lines"].([]any)[0].(map[string]any)["id"].(float64))
	res := authReq(a, http.MethodPost, base+"/reserve", map[string]any{"operation_id": "partial-reserve-" + tag, "line_id": lineID, "sku_id": sku, "warehouse_id": wh, "quantity": qty}, estoquista, "")
	if res.Code != 201 {
		t.Fatalf("reserve=%d %s", res.Code, res.Body.String())
	}
	rid := int64(materialTestJSON(t, res.Body.Bytes())["id"].(float64))
	del := authReq(a, http.MethodPost, base+"/deliver", map[string]any{"operation_id": "partial-deliver-" + tag, "reservation_id": rid, "quantity": qty}, estoquista, "")
	if del.Code != 201 {
		t.Fatalf("deliver=%d %s", del.Code, del.Body.String())
	}
	return base, int64(materialTestJSON(t, del.Body.Bytes())["id"].(float64))
}

func materialStatus(t *testing.T, a *app, base string, who loginData) map[string]any {
	t.Helper()
	rr := authReq(a, http.MethodGet, base, nil, who, "")
	if rr.Code != 200 {
		t.Fatalf("detail=%d %s", rr.Code, rr.Body.String())
	}
	return materialTestJSON(t, rr.Body.Bytes())
}

// Critérios da auditoria de recebimento parcial: a cozinha recebe 180 de uma entrega de
// 200, os 20 ficam como divergência, a gerência concilia depois, nada disso gera outra
// saída de estoque, e repetição ou concorrência não confirmam a mesma quantidade duas vezes.
func TestMaterialPartialReceiptDivergenceAndReconciliation(t *testing.T) {
	a, sku, wh, _ := newStockTestApp(t)
	createTestUser(t, a, "partial-cozinha", "secret", "Cozinha", true)
	createTestUser(t, a, "partial-estoquista", "secret", "Estoquista", true)
	createTestUser(t, a, "partial-gerente", "secret", "Gerente", true)
	cozinha := doLogin(t, a, "partial-cozinha", "secret")
	estoquista := doLogin(t, a, "partial-estoquista", "secret")
	gerente := doLogin(t, a, "partial-gerente", "secret")
	ctx := context.Background()
	if _, err := a.createEntry(ctx, movementRequest{OperationID: "partial-seed", SKUID: sku, WarehouseID: wh, Quantity: "300", UnitCost: "5"}, "partial-seed"); err != nil {
		t.Fatal(err)
	}
	base, delID := materialDeliveredRequest(t, a, sku, wh, "a", "200", cozinha, estoquista)
	assertStock(t, a, sku, wh, "100.000", "5.0000")
	var movementsBefore int
	if err := a.db.QueryRow("SELECT count(*) FROM movements").Scan(&movementsBefore); err != nil {
		t.Fatal(err)
	}
	recv := base + "/deliveries/" + strconv.FormatInt(delID, 10) + "/receive"
	recon := base + "/deliveries/" + strconv.FormatInt(delID, 10) + "/reconcile"

	// Validações da conferência.
	if rr := authReq(a, http.MethodPost, recv, map[string]any{"received_quantity": "201", "reason": "sobrou"}, cozinha, ""); rr.Code != 400 {
		t.Fatalf("receipt above delivered=%d %s", rr.Code, rr.Body.String())
	}
	if rr := authReq(a, http.MethodPost, recv, map[string]any{"received_quantity": "180"}, cozinha, ""); rr.Code != 400 {
		t.Fatalf("partial receipt without reason=%d %s", rr.Code, rr.Body.String())
	}
	if rr := authReq(a, http.MethodPost, recon, map[string]any{"operation_id": "partial-early", "quantity": "20"}, gerente, ""); rr.Code != 409 {
		t.Fatalf("reconcile before the kitchen checked=%d %s", rr.Code, rr.Body.String())
	}

	// A cozinha recebe 180 de 200.
	partial := map[string]any{"received_quantity": "180", "reason": "Duas caixas não chegaram"}
	rr := authReq(a, http.MethodPost, recv, partial, cozinha, "")
	if rr.Code != 200 || materialTestJSON(t, rr.Body.Bytes())["pending_divergence"] != "20.000" {
		t.Fatalf("partial receipt=%d %s", rr.Code, rr.Body.String())
	}
	if rr := authReq(a, http.MethodPost, recv, partial, cozinha, ""); rr.Code != 200 {
		t.Fatalf("partial receipt replay=%d %s", rr.Code, rr.Body.String())
	}
	if rr := authReq(a, http.MethodPost, recv, map[string]any{}, cozinha, ""); rr.Code != 200 {
		t.Fatalf("empty replay=%d %s", rr.Code, rr.Body.String())
	}
	if rr := authReq(a, http.MethodPost, recv, map[string]any{"received_quantity": "170", "reason": "outra"}, cozinha, ""); rr.Code != 409 {
		t.Fatalf("second receipt with another quantity=%d %s", rr.Code, rr.Body.String())
	}
	d := materialStatus(t, a, base, cozinha)
	del := d["deliveries"].([]any)[0].(map[string]any)
	if d["status"] != "RECEBIDA_COM_DIVERGENCIA" || del["received_quantity"] != "180.000" || del["pending_divergence"] != "20.000" || del["divergence_reason"] != "Duas caixas não chegaram" {
		t.Fatalf("after partial receipt: status=%v delivery=%+v", d["status"], del)
	}

	// Só Diretor e Gerente conciliam.
	for name, who := range map[string]loginData{"cozinha": cozinha, "estoquista": estoquista} {
		if rr := authReq(a, http.MethodPost, recon, map[string]any{"operation_id": "partial-denied-" + name, "quantity": "20"}, who, ""); rr.Code != 403 {
			t.Fatalf("%s reconciled=%d %s", name, rr.Code, rr.Body.String())
		}
	}
	if rr := authReq(a, http.MethodPost, recon, map[string]any{"operation_id": "partial-too-much", "quantity": "25"}, gerente, ""); rr.Code != 409 {
		t.Fatalf("reconcile above pending=%d %s", rr.Code, rr.Body.String())
	}
	first := map[string]any{"operation_id": "partial-recon-1", "quantity": "5", "notes": "Chegou na segunda viagem"}
	rr = authReq(a, http.MethodPost, recon, first, gerente, "")
	if rr.Code != 201 || materialTestJSON(t, rr.Body.Bytes())["pending_divergence"] != "15.000" {
		t.Fatalf("first reconcile=%d %s", rr.Code, rr.Body.String())
	}
	if rr := authReq(a, http.MethodPost, recon, first, gerente, ""); rr.Code != 200 {
		t.Fatalf("reconcile replay=%d %s", rr.Code, rr.Body.String())
	}
	if rr := authReq(a, http.MethodPost, recon, map[string]any{"operation_id": "partial-recon-1", "quantity": "6"}, gerente, ""); rr.Code != 409 {
		t.Fatalf("reused operation with other data=%d %s", rr.Code, rr.Body.String())
	}
	if d = materialStatus(t, a, base, gerente); d["status"] != "RECEBIDA_COM_DIVERGENCIA" {
		t.Fatalf("status after partial reconcile=%v", d["status"])
	}

	// Concorrência: dois envios dos 15 restantes; só um entra.
	var wg sync.WaitGroup
	codes := make([]int, 2)
	for i := range codes {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			codes[i] = authReq(a, http.MethodPost, recon, map[string]any{"operation_id": "partial-race-" + strconv.Itoa(i), "quantity": "15"}, gerente, "").Code
		}(i)
	}
	wg.Wait()
	if !((codes[0] == 201 && codes[1] == 409) || (codes[0] == 409 && codes[1] == 201)) {
		t.Fatalf("concurrent reconcile codes=%v, want one 201 and one 409", codes)
	}
	d = materialStatus(t, a, base, gerente)
	del = d["deliveries"].([]any)[0].(map[string]any)
	if d["status"] != "RECEBIDA" || del["received_quantity"] != "200.000" || del["pending_divergence"] != "0.000" || len(del["receipts"].([]any)) != 3 {
		t.Fatalf("after reconciliation: status=%v delivery=%+v", d["status"], del)
	}
	seen := map[string]int{}
	for _, e := range d["events"].([]any) {
		seen[e.(map[string]any)["action"].(string)]++
	}
	if seen["DIVERGENCIA"] != 1 || seen["CONCILIADA"] != 2 {
		t.Fatalf("audit events=%v", seen)
	}

	// Conferir e conciliar não mexem no estoque.
	var movementsAfter int
	if err := a.db.QueryRow("SELECT count(*) FROM movements").Scan(&movementsAfter); err != nil {
		t.Fatal(err)
	}
	if movementsAfter != movementsBefore {
		t.Fatalf("receipt created stock movements: before=%d after=%d", movementsBefore, movementsAfter)
	}
	assertStock(t, a, sku, wh, "100.000", "5.0000")

	// Recebimento integral informando a quantidade, e entrega que não chegou (zero).
	baseFull, delFull := materialDeliveredRequest(t, a, sku, wh, "b", "10", cozinha, estoquista)
	if rr := authReq(a, http.MethodPost, baseFull+"/deliveries/"+strconv.FormatInt(delFull, 10)+"/receive", map[string]any{"received_quantity": "10"}, cozinha, ""); rr.Code != 200 {
		t.Fatalf("full receipt=%d %s", rr.Code, rr.Body.String())
	}
	if d = materialStatus(t, a, baseFull, cozinha); d["status"] != "RECEBIDA" {
		t.Fatalf("full receipt status=%v", d["status"])
	}
	baseZero, delZero := materialDeliveredRequest(t, a, sku, wh, "c", "4", cozinha, estoquista)
	if rr := authReq(a, http.MethodPost, baseZero+"/deliveries/"+strconv.FormatInt(delZero, 10)+"/receive", map[string]any{"received_quantity": "0", "reason": "Não chegou"}, cozinha, ""); rr.Code != 200 {
		t.Fatalf("zero receipt=%d %s", rr.Code, rr.Body.String())
	}
	d = materialStatus(t, a, baseZero, cozinha)
	if del = d["deliveries"].([]any)[0].(map[string]any); d["status"] != "RECEBIDA_COM_DIVERGENCIA" || del["pending_divergence"] != "4.000" {
		t.Fatalf("zero receipt: status=%v delivery=%+v", d["status"], del)
	}
}
