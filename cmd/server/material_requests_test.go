package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

func materialTestJSON(t *testing.T, rrbody []byte) map[string]any {
	t.Helper()
	var out map[string]any
	if err := json.Unmarshal(rrbody, &out); err != nil {
		t.Fatalf("response JSON: %v %s", err, string(rrbody))
	}
	return out
}
func TestMaterialRequestWorkflowServiceReservationPartialDeliveryAndReceipt(t *testing.T) {
	a, sku, wh, _ := newStockTestApp(t)
	createTestUser(t, a, "material-cozinha", "secret", "Cozinha", true)
	createTestUser(t, a, "material-estoquista", "secret", "Estoquista", true)
	cozinha := doLogin(t, a, "material-cozinha", "secret")
	estoquista := doLogin(t, a, "material-estoquista", "secret")
	ctx := context.Background()
	if _, err := a.createEntry(ctx, movementRequest{OperationID: "material-seed", SKUID: sku, WarehouseID: wh, Quantity: "12", UnitCost: "5"}, "material-seed"); err != nil {
		t.Fatal(err)
	}
	var item int64
	if err := a.db.QueryRow("SELECT item_id FROM product_skus WHERE id=$1", sku).Scan(&item); err != nil {
		t.Fatal(err)
	}
	future := time.Now().In(time.FixedZone("-03", -3*3600)).AddDate(0, 0, 1).Format("2006-01-02")
	body := map[string]any{"operation_id": "material-request-create", "unit_name": "Cozinha Central", "delivery_date": future, "production_date": future, "service": "lanche", "notes": "Lanche", "lines": []map[string]any{{"item_id": item, "quantity": "10"}}}
	created := authReq(a, http.MethodPost, "/api/material-requests", body, cozinha, "")
	if created.Code != 201 {
		t.Fatalf("create=%d %s", created.Code, created.Body.String())
	}
	id := int64(materialTestJSON(t, created.Body.Bytes())["id"].(float64))
	base := "/api/material-requests/" + strconv.FormatInt(id, 10)
	repeated := authReq(a, http.MethodPost, "/api/material-requests", body, cozinha, "")
	if repeated.Code != 200 || int64(materialTestJSON(t, repeated.Body.Bytes())["id"].(float64)) != id {
		t.Fatalf("create replay=%d %s", repeated.Code, repeated.Body.String())
	}
	bad := map[string]any{"operation_id": "material-request-create", "unit_name": "Cozinha Central", "delivery_date": future, "production_date": future, "service": "jantar", "notes": "Lanche", "lines": []map[string]any{{"item_id": item, "quantity": "10"}}}
	if rr := authReq(a, http.MethodPost, "/api/material-requests", bad, cozinha, ""); rr.Code != 409 {
		t.Fatalf("changed replay=%d %s", rr.Code, rr.Body.String())
	}
	if rr := authReq(a, http.MethodPost, "/api/material-requests", body, estoquista, ""); rr.Code != 403 {
		t.Fatalf("estoquista created=%d", rr.Code)
	}
	if rr := authReq(a, http.MethodPost, base+"/reserve", map[string]any{}, cozinha, ""); rr.Code != 403 {
		t.Fatalf("kitchen reserve=%d", rr.Code)
	}
	if rr := authReq(a, http.MethodGet, base, nil, estoquista, ""); rr.Code != 200 {
		t.Fatalf("warehouse sees request=%d", rr.Code)
	}
	if rr := authReq(a, http.MethodGet, "/material-requests", nil, cozinha, ""); rr.Code != 200 || !strings.Contains(rr.Body.String(), "Nova requisição") {
		t.Fatalf("page=%d", rr.Code)
	}
	cozinha = doLogin(t, a, "material-cozinha", "secret")
	detail := authReq(a, http.MethodGet, base, nil, cozinha, "")
	if detail.Code != 200 {
		t.Fatalf("detail=%d %s", detail.Code, detail.Body.String())
	}
	d := materialTestJSON(t, detail.Body.Bytes())
	if d["service"] != "lanche" || d["production_date"] != future {
		t.Fatalf("wrong service/day: %+v", d)
	}
	lines := d["lines"].([]any)
	lineID := int64(lines[0].(map[string]any)["id"].(float64))
	reservation := map[string]any{"operation_id": "material-reserve-one", "line_id": lineID, "sku_id": sku, "warehouse_id": wh, "quantity": "8"}
	reserve := authReq(a, http.MethodPost, base+"/reserve", reservation, estoquista, "")
	if reserve.Code != 201 {
		t.Fatalf("reserve=%d %s", reserve.Code, reserve.Body.String())
	}
	rid := int64(materialTestJSON(t, reserve.Body.Bytes())["id"].(float64))
	if rep := authReq(a, http.MethodPost, base+"/reserve", reservation, estoquista, ""); rep.Code != 200 {
		t.Fatalf("reserve replay=%d %s", rep.Code, rep.Body.String())
	}
	if _, err := a.createExit(ctx, movementRequest{OperationID: "material-outside-exit", SKUID: sku, WarehouseID: wh, Quantity: "5"}, "hash"); !errors.Is(err, errInsufficientStock) {
		t.Fatalf("direct exit spent reserved stock: %v", err)
	}
	assertStock(t, a, sku, wh, "12.000", "5.0000")
	delivery := map[string]any{"operation_id": "material-deliver-one", "reservation_id": rid, "quantity": "4"}
	delivered := authReq(a, http.MethodPost, base+"/deliver", delivery, estoquista, "")
	if delivered.Code != 201 {
		t.Fatalf("deliver=%d %s", delivered.Code, delivered.Body.String())
	}
	delID := int64(materialTestJSON(t, delivered.Body.Bytes())["id"].(float64))
	if rep := authReq(a, http.MethodPost, base+"/deliver", delivery, estoquista, ""); rep.Code != 200 {
		t.Fatalf("delivery replay=%d", rep.Code)
	}
	assertStock(t, a, sku, wh, "8.000", "5.0000")
	var occurred time.Time
	var prod, doc string
	if err := a.db.QueryRow("SELECT m.occurred_at,m.production_date::text,m.document_number FROM movements m JOIN material_request_deliveries d ON d.movement_id=m.id WHERE d.id=$1", delID).Scan(&occurred, &prod, &doc); err != nil {
		t.Fatal(err)
	}
	if prod != future || doc != ("REQ-"+strconv.FormatInt(id, 10)) || time.Since(occurred) > time.Minute {
		t.Fatalf("wrong physical+production dates: %s %s %s", occurred, prod, doc)
	}
	if _, err := a.createExit(ctx, movementRequest{OperationID: "material-outside-exit-2", SKUID: sku, WarehouseID: wh, Quantity: "5"}, "hash"); !errors.Is(err, errInsufficientStock) {
		t.Fatalf("ordinary exit spent 4kg hold: %v", err)
	}
	recvURL := base + "/deliveries/" + strconv.FormatInt(delID, 10) + "/receive"
	if rr := authReq(a, http.MethodPost, recvURL, map[string]any{}, estoquista, ""); rr.Code != 403 {
		t.Fatalf("stock confirmed receipt=%d", rr.Code)
	}
	if rr := authReq(a, http.MethodPost, recvURL, map[string]any{}, cozinha, ""); rr.Code != 200 {
		t.Fatalf("receipt=%d %s", rr.Code, rr.Body.String())
	}
	// Continue with the same reservation, then reserve the pending balance.
	delivery2 := map[string]any{"operation_id": "material-deliver-two", "reservation_id": rid, "quantity": "4"}
	if rr := authReq(a, http.MethodPost, base+"/deliver", delivery2, estoquista, ""); rr.Code != 201 {
		t.Fatalf("second delivery=%d %s", rr.Code, rr.Body.String())
	}
	reserve2 := map[string]any{"operation_id": "material-reserve-two", "line_id": lineID, "sku_id": sku, "warehouse_id": wh, "quantity": "2"}
	res2 := authReq(a, http.MethodPost, base+"/reserve", reserve2, estoquista, "")
	if res2.Code != 201 {
		t.Fatalf("reserve last=%d %s", res2.Code, res2.Body.String())
	}
	rid2 := int64(materialTestJSON(t, res2.Body.Bytes())["id"].(float64))
	if rr := authReq(a, http.MethodPost, base+"/deliver", map[string]any{"operation_id": "material-deliver-three", "reservation_id": rid2, "quantity": "2"}, estoquista, ""); rr.Code != 201 {
		t.Fatalf("last delivery=%d %s", rr.Code, rr.Body.String())
	}
	assertStock(t, a, sku, wh, "2.000", "5.0000")
	d = materialTestJSON(t, authReq(a, http.MethodGet, base, nil, cozinha, "").Body.Bytes())
	if d["status"] != "ENTREGUE" {
		t.Fatalf("expected entregue, got %v", d["status"])
	}
	for _, v := range d["deliveries"].([]any) {
		x := v.(map[string]any)
		idv := int64(x["id"].(float64))
		if rr := authReq(a, http.MethodPost, base+"/deliveries/"+strconv.FormatInt(idv, 10)+"/receive", map[string]any{}, cozinha, ""); rr.Code != 200 {
			t.Fatalf("receive all=%d %s", rr.Code, rr.Body.String())
		}
	}
	d = materialTestJSON(t, authReq(a, http.MethodGet, base, nil, cozinha, "").Body.Bytes())
	if d["status"] != "RECEBIDA" {
		t.Fatalf("final state=%v", d["status"])
	}
	if rr := authReq(a, http.MethodPost, base+"/cancel", map[string]any{}, cozinha, ""); rr.Code != 409 {
		t.Fatalf("received request cancelled=%d", rr.Code)
	}
}

func TestMaterialRequestsReserveConcurrentAndRelease(t *testing.T) {
	a, sku, wh, _ := newStockTestApp(t)
	createTestUser(t, a, "material-cook-concurrent", "secret", "Cozinha", true)
	createTestUser(t, a, "material-warehouse-concurrent", "secret", "Estoquista", true)
	c := doLogin(t, a, "material-cook-concurrent", "secret")
	st := doLogin(t, a, "material-warehouse-concurrent", "secret")
	_, err := a.createEntry(context.Background(), movementRequest{OperationID: "mat-concurrent-in", SKUID: sku, WarehouseID: wh, Quantity: "10", UnitCost: "3"}, "h")
	if err != nil {
		t.Fatal(err)
	}
	var item int64
	if err := a.db.QueryRow("SELECT item_id FROM product_skus WHERE id=$1", sku).Scan(&item); err != nil {
		t.Fatal(err)
	}
	day := "2026-11-15"
	var ids, lines []int64
	for k := 0; k < 2; k++ {
		op := "material-concurrent-" + strconv.Itoa(k)
		req := map[string]any{"operation_id": op, "unit_name": "Cozinha", "delivery_date": day, "production_date": day, "service": "jantar", "notes": "", "lines": []map[string]any{{"item_id": item, "quantity": "7"}}}
		rr := authReq(a, http.MethodPost, "/api/material-requests", req, c, "")
		if rr.Code != 201 {
			t.Fatalf("request %d=%d %s", k, rr.Code, rr.Body.String())
		}
		id := int64(materialTestJSON(t, rr.Body.Bytes())["id"].(float64))
		ids = append(ids, id)
		dd := materialTestJSON(t, authReq(a, http.MethodGet, "/api/material-requests/"+strconv.FormatInt(id, 10), nil, c, "").Body.Bytes())
		lines = append(lines, int64(dd["lines"].([]any)[0].(map[string]any)["id"].(float64)))
	}
	results := make([]int, 2)
	var wg sync.WaitGroup
	for k := 0; k < 2; k++ {
		wg.Add(1)
		go func(k int) {
			defer wg.Done()
			p := "/api/material-requests/" + strconv.FormatInt(ids[k], 10) + "/reserve"
			rr := authReq(a, http.MethodPost, p, map[string]any{"operation_id": "mat-reserve-concurrent-" + strconv.Itoa(k), "line_id": lines[k], "sku_id": sku, "warehouse_id": wh, "quantity": "7"}, st, "")
			results[k] = rr.Code
		}(k)
	}
	wg.Wait()
	if !(results[0] == 201 && results[1] == 409 || results[0] == 409 && results[1] == 201) {
		t.Fatalf("oversold in concurrency: %+v", results)
	}
	var activeID int64
	if err := a.db.QueryRow("SELECT id FROM material_request_reservations WHERE status='ATIVA'").Scan(&activeID); err != nil {
		t.Fatal(err)
	}
	var reqID int64
	if err := a.db.QueryRow("SELECT l.request_id FROM material_request_reservations r JOIN material_request_lines l ON l.id=r.request_line_id WHERE r.id=$1", activeID).Scan(&reqID); err != nil {
		t.Fatal(err)
	}
	url := "/api/material-requests/" + strconv.FormatInt(reqID, 10) + "/reservations/" + strconv.FormatInt(activeID, 10) + "/release"
	if rr := authReq(a, http.MethodPost, url, map[string]any{}, st, ""); rr.Code != 200 {
		t.Fatalf("release=%d", rr.Code)
	}
	if rr := authReq(a, http.MethodPost, url, map[string]any{}, st, ""); rr.Code != 200 {
		t.Fatalf("release repeat=%d", rr.Code)
	}
	if _, err := a.createExit(context.Background(), movementRequest{OperationID: "mat-concurrent-now-free", SKUID: sku, WarehouseID: wh, Quantity: "10"}, "h"); err != nil {
		t.Fatalf("released hold still blocks stock: %v", err)
	}
}

func TestMaterialRequestCancelReleasesHoldAndOwnership(t *testing.T) {
	a, sku, wh, _ := newStockTestApp(t)
	createTestUser(t, a, "material-owner-a", "secret", "Cozinha", true)
	createTestUser(t, a, "material-owner-b", "secret", "Cozinha", true)
	createTestUser(t, a, "material-fulfiller", "secret", "Estoquista", true)
	owner := doLogin(t, a, "material-owner-a", "secret")
	stranger := doLogin(t, a, "material-owner-b", "secret")
	fulfiller := doLogin(t, a, "material-fulfiller", "secret")
	if _, err := a.createEntry(context.Background(), movementRequest{OperationID: "material-cancel-seed", SKUID: sku, WarehouseID: wh, Quantity: "5", UnitCost: "4"}, "hash"); err != nil {
		t.Fatal(err)
	}
	var item int64
	if err := a.db.QueryRow("SELECT item_id FROM product_skus WHERE id=$1", sku).Scan(&item); err != nil {
		t.Fatal(err)
	}
	body := map[string]any{"operation_id": "material-cancel-new", "unit_name": "Cozinha", "delivery_date": "2026-11-16", "production_date": "2026-11-16", "service": "cafe", "notes": "", "lines": []map[string]any{{"item_id": item, "quantity": "3"}}}
	rr := authReq(a, http.MethodPost, "/api/material-requests", body, owner, "")
	if rr.Code != 201 {
		t.Fatalf("create: %d %s", rr.Code, rr.Body.String())
	}
	id := int64(materialTestJSON(t, rr.Body.Bytes())["id"].(float64))
	base := "/api/material-requests/" + strconv.FormatInt(id, 10)
	if rr = authReq(a, http.MethodGet, base, nil, stranger, ""); rr.Code != 403 {
		t.Fatalf("read other's request %d", rr.Code)
	}
	if rr = authReq(a, http.MethodPost, base+"/cancel", map[string]any{}, stranger, ""); rr.Code != 403 {
		t.Fatalf("cancel other's request %d", rr.Code)
	}
	d := materialTestJSON(t, authReq(a, http.MethodGet, base, nil, owner, "").Body.Bytes())
	lineID := int64(d["lines"].([]any)[0].(map[string]any)["id"].(float64))
	rr = authReq(a, http.MethodPost, base+"/reserve", map[string]any{"operation_id": "material-cancel-reserve", "line_id": lineID, "sku_id": sku, "warehouse_id": wh, "quantity": "3"}, fulfiller, "")
	if rr.Code != 201 {
		t.Fatalf("reserve %d %s", rr.Code, rr.Body.String())
	}
	free := authReq(a, http.MethodGet, "/api/material-requests/options/"+strconv.FormatInt(item, 10), nil, fulfiller, "")
	if free.Code != 200 {
		t.Fatalf("stock options %d %s", free.Code, free.Body.String())
	}
	var opts []map[string]any
	if err := json.Unmarshal(free.Body.Bytes(), &opts); err != nil {
		t.Fatal(err)
	}
	if len(opts) != 1 || opts[0]["available"] != "2.000" {
		t.Fatalf("available stock after hold: %+v", opts)
	}
	if rr = authReq(a, http.MethodPost, base+"/cancel", map[string]any{}, owner, ""); rr.Code != 200 {
		t.Fatalf("cancel %d %s", rr.Code, rr.Body.String())
	}
	if rr = authReq(a, http.MethodPost, base+"/cancel", map[string]any{}, owner, ""); rr.Code != 200 {
		t.Fatalf("cancel replay %d", rr.Code)
	}
	if _, err := a.createExit(context.Background(), movementRequest{OperationID: "material-cancel-free-exit", SKUID: sku, WarehouseID: wh, Quantity: "5"}, "hash"); err != nil {
		t.Fatalf("cancel didn't release hold: %v", err)
	}
	d = materialTestJSON(t, authReq(a, http.MethodGet, base, nil, owner, "").Body.Bytes())
	if d["status"] != "CANCELADA" {
		t.Fatalf("state=%v", d["status"])
	}
}
