package main

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"testing"
)

// Edição de requisição: corrigir item errado e quantidade sem cancelar nem mexer no banco.
func TestMaterialRequestEdit(t *testing.T) {
	a, sku, wh, _ := newStockTestApp(t)
	createTestUser(t, a, "edit-cozinha", "secret", "Cozinha", true)
	createTestUser(t, a, "edit-cozinha-2", "secret", "Cozinha", true)
	createTestUser(t, a, "edit-estoquista", "secret", "Estoquista", true)
	createTestUser(t, a, "edit-gerente", "secret", "Gerente", true)
	cozinha := doLogin(t, a, "edit-cozinha", "secret")
	outra := doLogin(t, a, "edit-cozinha-2", "secret")
	estoquista := doLogin(t, a, "edit-estoquista", "secret")
	gerente := doLogin(t, a, "edit-gerente", "secret")
	ctx := context.Background()
	if _, err := a.createEntry(ctx, movementRequest{OperationID: "edit-seed", SKUID: sku, WarehouseID: wh, Quantity: "100", UnitCost: "5"}, "edit-seed"); err != nil {
		t.Fatal(err)
	}
	var arroz, cat, feijao, macarrao int64
	if err := a.db.QueryRow("SELECT item_id FROM product_skus WHERE id=$1", sku).Scan(&arroz); err != nil {
		t.Fatal(err)
	}
	if err := a.db.QueryRow("SELECT category_id FROM inventory_items WHERE id=$1", arroz).Scan(&cat); err != nil {
		t.Fatal(err)
	}
	if err := a.db.QueryRow("INSERT INTO inventory_items(name,category_id,unit_code) VALUES('Feijão',$1,'UN') RETURNING id", cat).Scan(&feijao); err != nil {
		t.Fatal(err)
	}
	if err := a.db.QueryRow("INSERT INTO inventory_items(name,category_id,unit_code) VALUES('Macarrão',$1,'UN') RETURNING id", cat).Scan(&macarrao); err != nil {
		t.Fatal(err)
	}
	line := func(item int64, q string) map[string]any { return map[string]any{"item_id": item, "quantity": q} }
	create := func(tag string, lines ...map[string]any) string {
		t.Helper()
		body := map[string]any{"operation_id": "edit-" + tag, "unit_name": "Cozinha Central", "delivery_date": "2026-11-20", "production_date": "2026-11-20", "service": "almoco", "notes": "", "lines": lines}
		rr := authReq(a, http.MethodPost, "/api/material-requests", body, cozinha, "")
		if rr.Code != 201 {
			t.Fatalf("create=%d %s", rr.Code, rr.Body.String())
		}
		return "/api/material-requests/" + strconv.FormatInt(int64(materialTestJSON(t, rr.Body.Bytes())["id"].(float64)), 10)
	}
	edit := func(version int64, lines ...map[string]any) map[string]any {
		return map[string]any{"version": version, "unit_name": "Cozinha Central", "delivery_date": "2026-11-20", "production_date": "2026-11-20", "service": "almoco", "notes": "", "lines": lines}
	}
	lineOf := func(d map[string]any, item int64) map[string]any {
		for _, l := range d["lines"].([]any) {
			m := l.(map[string]any)
			if int64(m["item_id"].(float64)) == item {
				return m
			}
		}
		return nil
	}

	// 1. Item errado: tira o feijão, corrige o arroz e inclui o macarrão.
	base := create("a", line(arroz, "10"), line(feijao, "4"))
	d := materialStatus(t, a, base, cozinha)
	if d["version"].(float64) != 1 {
		t.Fatalf("version=%v", d["version"])
	}
	fix := edit(1, line(arroz, "12"), line(macarrao, "3"))
	fix["notes"] = "trocar feijão por macarrão"
	if rr := authReq(a, http.MethodPost, base+"/edit", fix, outra, ""); rr.Code != 403 {
		t.Fatalf("other kitchen edit=%d", rr.Code)
	}
	if rr := authReq(a, http.MethodPost, base+"/edit", fix, estoquista, ""); rr.Code != 403 {
		t.Fatalf("stock clerk edit=%d", rr.Code)
	}
	rr := authReq(a, http.MethodPost, base+"/edit", fix, cozinha, "")
	if rr.Code != 200 {
		t.Fatalf("edit=%d %s", rr.Code, rr.Body.String())
	}
	if res := materialTestJSON(t, rr.Body.Bytes()); res["changed"] != true || res["version"].(float64) != 2 {
		t.Fatalf("edit result %+v", res)
	}
	d = materialStatus(t, a, base, cozinha)
	if len(d["lines"].([]any)) != 2 || lineOf(d, feijao) != nil || lineOf(d, arroz)["quantity"] != "12.000" || lineOf(d, macarrao)["quantity"] != "3.000" || d["notes"] != "trocar feijão por macarrão" {
		t.Fatalf("lines after edit %+v notes=%v", d["lines"], d["notes"])
	}
	ev := d["events"].([]any)[0].(map[string]any)
	if ev["action"] != "EDITADA" || !strings.Contains(ev["details"].(string), "removido Feijão") || !strings.Contains(ev["details"].(string), "Arroz: 10 → 12 KG") || !strings.Contains(ev["details"].(string), "incluído Macarrão (3 UN)") {
		t.Fatalf("event %+v", ev)
	}
	// Reenvio da mesma edição (resposta perdida) não muda nada.
	rr = authReq(a, http.MethodPost, base+"/edit", fix, cozinha, "")
	if rr.Code != 200 || materialTestJSON(t, rr.Body.Bytes())["changed"] != false {
		t.Fatalf("replay=%d %s", rr.Code, rr.Body.String())
	}
	// Tela desatualizada com outra alteração não sobrescreve.
	if rr = authReq(a, http.MethodPost, base+"/edit", edit(1, line(arroz, "1")), cozinha, ""); rr.Code != 409 {
		t.Fatalf("stale version=%d %s", rr.Code, rr.Body.String())
	}
	// Validações.
	if rr = authReq(a, http.MethodPost, base+"/edit", edit(2), cozinha, ""); rr.Code != 400 {
		t.Fatalf("no lines=%d", rr.Code)
	}
	if rr = authReq(a, http.MethodPost, base+"/edit", edit(2, line(arroz, "1"), line(arroz, "2")), cozinha, ""); rr.Code != 400 {
		t.Fatalf("repeated item=%d", rr.Code)
	}

	// 2. Separado e entregue em parte: o entregue limita a edição.
	d = materialStatus(t, a, base, cozinha)
	lineID := int64(lineOf(d, arroz)["id"].(float64))
	rr = authReq(a, http.MethodPost, base+"/reserve", map[string]any{"operation_id": "edit-res-a", "line_id": lineID, "sku_id": sku, "warehouse_id": wh, "quantity": "12"}, estoquista, "")
	if rr.Code != 201 {
		t.Fatalf("reserve=%d %s", rr.Code, rr.Body.String())
	}
	rid := int64(materialTestJSON(t, rr.Body.Bytes())["id"].(float64))
	if rr = authReq(a, http.MethodPost, base+"/deliver", map[string]any{"operation_id": "edit-del-a", "reservation_id": rid, "quantity": "5"}, estoquista, ""); rr.Code != 201 {
		t.Fatalf("deliver=%d %s", rr.Code, rr.Body.String())
	}
	if rr = authReq(a, http.MethodPost, base+"/edit", edit(2, line(macarrao, "3")), cozinha, ""); rr.Code != 409 || !strings.Contains(rr.Body.String(), "já teve entrega") {
		t.Fatalf("remove delivered=%d %s", rr.Code, rr.Body.String())
	}
	if rr = authReq(a, http.MethodPost, base+"/edit", edit(2, line(arroz, "4"), line(macarrao, "3")), cozinha, ""); rr.Code != 409 || !strings.Contains(rr.Body.String(), "abaixo") {
		t.Fatalf("below delivered=%d %s", rr.Code, rr.Body.String())
	}
	moved := edit(2, line(arroz, "12"), line(macarrao, "3"))
	moved["production_date"] = "2026-11-21"
	if rr = authReq(a, http.MethodPost, base+"/edit", moved, cozinha, ""); rr.Code != 409 || !strings.Contains(rr.Body.String(), "data de produção") {
		t.Fatalf("production date after delivery=%d %s", rr.Code, rr.Body.String())
	}
	// Reduz para 6 kg: limite 6 × 1,2 = 7,2; entregue 5 + separado 7 = 12, então 4,8 voltam ao saldo livre.
	// A gerência também pode editar.
	reduce := edit(2, line(arroz, "6"), line(macarrao, "3"))
	reduce["delivery_date"] = "2026-11-19"
	if rr = authReq(a, http.MethodPost, base+"/edit", reduce, gerente, ""); rr.Code != 200 {
		t.Fatalf("reduce=%d %s", rr.Code, rr.Body.String())
	}
	var remaining, state string
	if err := a.db.QueryRow("SELECT remaining_quantity::text,status FROM material_request_reservations WHERE id=$1", rid).Scan(&remaining, &state); err != nil {
		t.Fatal(err)
	}
	if remaining != "2.200" || state != "ATIVA" {
		t.Fatalf("reservation after reduce %s %s", remaining, state)
	}
	d = materialStatus(t, a, base, cozinha)
	if d["delivery_date"] != "2026-11-19" || d["status"] != "ENTREGA_PARCIAL" {
		t.Fatalf("after reduce %v %v", d["delivery_date"], d["status"])
	}
	// Nada disso movimenta estoque: só a entrega de 5 kg saiu.
	assertStock(t, a, sku, wh, "95.000", "5.0000")

	// 3. Item removido libera a separação.
	base2 := create("b", line(arroz, "3"), line(macarrao, "1"))
	d = materialStatus(t, a, base2, cozinha)
	l2 := int64(lineOf(d, arroz)["id"].(float64))
	if rr = authReq(a, http.MethodPost, base2+"/reserve", map[string]any{"operation_id": "edit-res-b", "line_id": l2, "sku_id": sku, "warehouse_id": wh, "quantity": "3"}, estoquista, ""); rr.Code != 201 {
		t.Fatalf("reserve b=%d %s", rr.Code, rr.Body.String())
	}
	if rr = authReq(a, http.MethodPost, base2+"/edit", edit(1, line(macarrao, "1")), cozinha, ""); rr.Code != 200 {
		t.Fatalf("remove reserved=%d %s", rr.Code, rr.Body.String())
	}
	var held int
	if err := a.db.QueryRow("SELECT count(*) FROM material_request_reservations WHERE request_line_id=$1", l2).Scan(&held); err != nil {
		t.Fatal(err)
	}
	d = materialStatus(t, a, base2, cozinha)
	if held != 0 || len(d["lines"].([]any)) != 1 || d["status"] != "PENDENTE" {
		t.Fatalf("after removing reserved line: held=%d status=%v lines=%v", held, d["status"], d["lines"])
	}

	// 4. Cancelada ou recebida não edita.
	if rr = authReq(a, http.MethodPost, base2+"/cancel", map[string]any{}, cozinha, ""); rr.Code != 200 {
		t.Fatalf("cancel=%d", rr.Code)
	}
	if rr = authReq(a, http.MethodPost, base2+"/edit", edit(2, line(macarrao, "2")), cozinha, ""); rr.Code != 409 {
		t.Fatalf("edit cancelled=%d %s", rr.Code, rr.Body.String())
	}
	base3, del3 := materialDeliveredRequest(t, a, sku, wh, "edit-c", "2", cozinha, estoquista)
	if rr = authReq(a, http.MethodPost, base3+"/deliveries/"+strconv.FormatInt(del3, 10)+"/receive", map[string]any{}, cozinha, ""); rr.Code != 200 {
		t.Fatalf("receive=%d %s", rr.Code, rr.Body.String())
	}
	d = materialStatus(t, a, base3, cozinha)
	if d["status"] != "RECEBIDA" {
		t.Fatalf("status=%v", d["status"])
	}
	if rr = authReq(a, http.MethodPost, base3+"/edit", edit(1, line(arroz, "3")), cozinha, ""); rr.Code != 409 {
		t.Fatalf("edit received=%d %s", rr.Code, rr.Body.String())
	}
}
