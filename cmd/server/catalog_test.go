package main

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"testing"
)

func TestCatalogItemAndSKUFlow(t *testing.T) {
	a, _, wh, _ := newStockTestApp(t)
	createTestUser(t, a, "director-catalog", "secret", "Diretor", true)
	createTestUser(t, a, "sales-catalog", "secret", "Vendas", true)
	director := doLogin(t, a, "director-catalog", "secret")
	sales := doLogin(t, a, "sales-catalog", "secret")

	catID := lookupID(t, a, `SELECT id FROM categories WHERE name='Alimentos'`)
	itemResp := authReq(a, http.MethodPost, "/api/catalog/items", map[string]any{"name": "Feijão Carioca <b>", "category_id": catID, "unit_code": "KG", "minimum_stock": "3", "notes": "uso <script>alert(1)</script>"}, director, "")
	if itemResp.Code != http.StatusCreated {
		t.Fatalf("create item status=%d body=%s", itemResp.Code, itemResp.Body.String())
	}
	itemID := jsonID(t, itemResp.Body.Bytes())
	skuResp := authReq(a, http.MethodPost, "/api/catalog/skus", map[string]any{"item_id": itemID, "name": "Feijão Marca A", "barcode": "7891000000011"}, director, "")
	if skuResp.Code != http.StatusCreated {
		t.Fatalf("create sku status=%d body=%s", skuResp.Code, skuResp.Body.String())
	}
	firstSKU := jsonID(t, skuResp.Body.Bytes())
	secondResp := authReq(a, http.MethodPost, "/api/catalog/skus", map[string]any{"item_id": itemID, "name": "Feijão Marca B", "barcode": "7891000000028"}, director, "")
	if secondResp.Code != http.StatusCreated {
		t.Fatalf("create second sku status=%d body=%s", secondResp.Code, secondResp.Body.String())
	}
	dupResp := authReq(a, http.MethodPost, "/api/catalog/skus", map[string]any{"item_id": itemID, "name": "Duplicado", "barcode": "7891000000011"}, director, "")
	if dupResp.Code != http.StatusConflict || !strings.Contains(dupResp.Body.String(), "Este código de barras já está cadastrado.") {
		t.Fatalf("duplicate barcode status=%d body=%s", dupResp.Code, dupResp.Body.String())
	}
	badFK := authReq(a, http.MethodPost, "/api/catalog/skus", map[string]any{"item_id": 999999, "name": "Sem item", "barcode": "7891000000998"}, director, "")
	if badFK.Code != http.StatusBadRequest {
		t.Fatalf("invalid fk status=%d body=%s", badFK.Code, badFK.Body.String())
	}
	if rr := authReq(a, http.MethodPost, "/api/catalog/items", map[string]any{"name": "Negado", "category_id": catID, "unit_code": "KG", "minimum_stock": "0"}, sales, ""); rr.Code != http.StatusForbidden {
		t.Fatalf("user without permission status=%d", rr.Code)
	}
	if rr := authReq(a, http.MethodPost, "/api/catalog/items", map[string]any{"name": "Sem CSRF", "category_id": catID, "unit_code": "KG", "minimum_stock": "0"}, director, "bad-csrf"); rr.Code != http.StatusForbidden {
		t.Fatalf("csrf status=%d", rr.Code)
	}
	lookup := authReq(a, http.MethodGet, "/api/products/lookup?q=7891000000011", nil, director, "")
	if lookup.Code != http.StatusOK || !strings.Contains(lookup.Body.String(), "Feijão Marca A") {
		t.Fatalf("lookup by barcode status=%d body=%s", lookup.Code, lookup.Body.String())
	}
	lookupItem := authReq(a, http.MethodGet, "/api/products/lookup?q=Carioca", nil, director, "")
	if lookupItem.Code != http.StatusOK || !strings.Contains(lookupItem.Body.String(), "Feijão Marca B") {
		t.Fatalf("lookup by item status=%d body=%s", lookupItem.Code, lookupItem.Body.String())
	}

	res, err := a.createEntry(context.Background(), movementRequest{OperationID: "catalog-hist", SKUID: firstSKU, WarehouseID: wh, Quantity: "1", UnitCost: "10"}, "catalog-hist")
	mustMove2(t, res, err)
	editBlocked := authReq(a, http.MethodPatch, "/api/catalog/items/"+itoa(itemID), map[string]any{"name": "Feijão Carioca", "category_id": catID, "unit_code": "UN", "minimum_stock": "3", "notes": "ok", "active": true}, director, "")
	if editBlocked.Code != http.StatusConflict || !strings.Contains(editBlocked.Body.String(), "Unidade não pode ser alterada") {
		t.Fatalf("unit edit with history status=%d body=%s", editBlocked.Code, editBlocked.Body.String())
	}
	editOK := authReq(a, http.MethodPatch, "/api/catalog/items/"+itoa(itemID), map[string]any{"name": "Feijão Carioca Corrigido", "category_id": catID, "unit_code": "KG", "minimum_stock": "4", "notes": "ok", "active": true}, director, "")
	if editOK.Code != http.StatusOK {
		t.Fatalf("allowed item edit status=%d body=%s", editOK.Code, editOK.Body.String())
	}
	skuEdit := authReq(a, http.MethodPatch, "/api/catalog/skus/"+itoa(firstSKU), map[string]any{"name": "Feijão Marca A Corrigido", "barcode": "7891000000011", "active": true}, director, "")
	if skuEdit.Code != http.StatusOK {
		t.Fatalf("sku edit status=%d body=%s", skuEdit.Code, skuEdit.Body.String())
	}
}

func TestCatalogPageEscapesData(t *testing.T) {
	a, _, _, _ := newStockTestApp(t)
	createTestUser(t, a, "director-catalog-ui", "secret", "Diretor", true)
	login := doLogin(t, a, "director-catalog-ui", "secret")
	catID := lookupID(t, a, `SELECT id FROM categories WHERE name='Alimentos'`)
	authReq(a, http.MethodPost, "/api/catalog/items", map[string]any{"name": "<script>alert(1)</script>", "category_id": catID, "unit_code": "KG", "minimum_stock": "0", "notes": "<img src=x>"}, login, "")
	rr := authReq(a, http.MethodGet, "/catalog", nil, login, "")
	if rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), "Catálogo") {
		t.Fatalf("catalog page status=%d body=%s", rr.Code, rr.Body.String())
	}
}

func lookupID(t *testing.T, a *app, q string) int64 {
	t.Helper()
	var id int64
	if err := a.db.QueryRow(q).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

func jsonID(t *testing.T, b []byte) int64 {
	t.Helper()
	var out map[string]any
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatal(err)
	}
	id, ok := out["id"].(float64)
	if !ok || id <= 0 {
		t.Fatalf("missing id in %s", string(b))
	}
	return int64(id)
}

func itoa(v int64) string { return strconv.FormatInt(v, 10) }
