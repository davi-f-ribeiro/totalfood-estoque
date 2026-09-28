package main

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

func TestFiscalManualFallbackConfirmMultiLine(t *testing.T) {
	a, _, wh, _ := newStockTestApp(t)
	createTestUser(t, a, "director-fiscal-manual", "secret", "Diretor", true)
	createTestUser(t, a, "sales-fiscal-manual", "secret", "Vendas", true)
	director := doLogin(t, a, "director-fiscal-manual", "secret")
	sales := doLogin(t, a, "sales-fiscal-manual", "secret")
	sku1 := createFiscalTestSKU(t, a, director, "Manual Item 1", "Manual SKU 1", "7898000000011")
	sku2 := createFiscalTestSKU(t, a, director, "Manual Item 2", "Manual SKU 2", "7898000000028")
	key := "23260903720882003416650070000391961071250200"
	if k, err := parseFiscalAccessKey(key); err != nil || k.CUF != "23" || k.UF != "CE" || k.Model != "65" {
		t.Fatalf("bad key parse key=%+v err=%v", k, err)
	}
	before, err := getStockSnapshot(context.Background(), a.db, sku1, wh)
	if err != nil || before.Quantity != "0" {
		t.Fatalf("before stock=%+v err=%v", before, err)
	}
	payload := map[string]any{"operation_id": "fiscal-manual-ok", "access_key": key, "warehouse_id": wh, "lines": []map[string]any{{"sku_id": sku1, "quantity": "2", "unit_cost": "10"}, {"sku_id": sku2, "quantity": "3", "unit_cost": "7.5"}}}
	if rr := authReq(a, http.MethodPost, "/api/fiscal/confirm", payload, sales, ""); rr.Code != http.StatusForbidden {
		t.Fatalf("permission status=%d body=%s", rr.Code, rr.Body.String())
	}
	if rr := authReq(a, http.MethodPost, "/api/fiscal/confirm", payload, director, "bad-csrf"); rr.Code != http.StatusForbidden {
		t.Fatalf("csrf status=%d body=%s", rr.Code, rr.Body.String())
	}
	rr := authReq(a, http.MethodPost, "/api/fiscal/confirm", payload, director, "")
	if rr.Code != http.StatusOK {
		t.Fatalf("confirm status=%d body=%s", rr.Code, rr.Body.String())
	}
	s1, _ := getStockSnapshot(context.Background(), a.db, sku1, wh)
	s2, _ := getStockSnapshot(context.Background(), a.db, sku2, wh)
	if s1.Quantity != "2.000" || s1.AvgCost != "10.0000" || s2.Quantity != "3.000" || s2.AvgCost != "7.5000" {
		t.Fatalf("bad stocks s1=%+v s2=%+v", s1, s2)
	}
	retry := authReq(a, http.MethodPost, "/api/fiscal/confirm", payload, director, "")
	if retry.Code != http.StatusOK {
		t.Fatalf("retry status=%d body=%s", retry.Code, retry.Body.String())
	}
	s1retry, _ := getStockSnapshot(context.Background(), a.db, sku1, wh)
	if s1retry.Quantity != "2.000" {
		t.Fatalf("retry duplicated stock=%+v", s1retry)
	}
	dupPayload := map[string]any{"operation_id": "fiscal-manual-dup", "access_key": key, "warehouse_id": wh, "lines": []map[string]any{{"sku_id": sku1, "quantity": "1", "unit_cost": "1"}}}
	dup := authReq(a, http.MethodPost, "/api/fiscal/confirm", dupPayload, director, "")
	if dup.Code != http.StatusConflict || !strings.Contains(dup.Body.String(), "Nota fiscal já confirmada") {
		t.Fatalf("duplicate status=%d body=%s", dup.Code, dup.Body.String())
	}
}

func TestFiscalManualFallbackRollback(t *testing.T) {
	a, _, wh, _ := newStockTestApp(t)
	createTestUser(t, a, "director-fiscal-rollback", "secret", "Diretor", true)
	director := doLogin(t, a, "director-fiscal-rollback", "secret")
	sku1 := createFiscalTestSKU(t, a, director, "Rollback Item 1", "Rollback SKU 1", "7898000001018")
	key := validAccessKeyForTest("2326090372088200341665007000039196107125030")
	payload := map[string]any{"operation_id": "fiscal-manual-rollback", "access_key": key, "warehouse_id": wh, "lines": []map[string]any{{"sku_id": sku1, "quantity": "2", "unit_cost": "10"}, {"sku_id": int64(99999999), "quantity": "3", "unit_cost": "7.5"}}}
	rr := authReq(a, http.MethodPost, "/api/fiscal/confirm", payload, director, "")
	if rr.Code != http.StatusNotFound {
		t.Fatalf("rollback status=%d body=%s", rr.Code, rr.Body.String())
	}
	s1, _ := getStockSnapshot(context.Background(), a.db, sku1, wh)
	if s1.Quantity != "0" {
		t.Fatalf("rollback left stock=%+v", s1)
	}
}

func createFiscalTestSKU(t *testing.T, a *app, login loginData, itemName, skuName, barcode string) int64 {
	t.Helper()
	catID := lookupID(t, a, `SELECT id FROM categories WHERE name='Alimentos'`)
	itemResp := authReq(a, http.MethodPost, "/api/catalog/items", map[string]any{"name": itemName, "category_id": catID, "unit_code": "UN", "minimum_stock": "0"}, login, "")
	if itemResp.Code != http.StatusCreated {
		t.Fatalf("item status=%d body=%s", itemResp.Code, itemResp.Body.String())
	}
	itemID := jsonID(t, itemResp.Body.Bytes())
	skuResp := authReq(a, http.MethodPost, "/api/catalog/skus", map[string]any{"item_id": itemID, "name": skuName, "barcode": barcode}, login, "")
	if skuResp.Code != http.StatusCreated {
		t.Fatalf("sku status=%d body=%s", skuResp.Code, skuResp.Body.String())
	}
	var out map[string]any
	if err := json.Unmarshal(skuResp.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	return int64(out["id"].(float64))
}
