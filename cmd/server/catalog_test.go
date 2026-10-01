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

	pagedBarcode := authReq(a, http.MethodGet, "/api/catalog/items?paged=1&q=7891000000011", nil, director, "")
	if pagedBarcode.Code != http.StatusOK || !strings.Contains(pagedBarcode.Body.String(), "Feijão Carioca") || !strings.Contains(pagedBarcode.Body.String(), "\"items\"") {
		t.Fatalf("paged catalog barcode search status=%d body=%s", pagedBarcode.Code, pagedBarcode.Body.String())
	}
	options := authReq(a, http.MethodGet, "/api/catalog/options", nil, director, "")
	if options.Code != http.StatusOK || strings.Contains(options.Body.String(), "\"items\"") {
		t.Fatalf("catalog options must not preload parent items status=%d body=%s", options.Code, options.Body.String())
	}

	res, err := a.createEntry(context.Background(), movementRequest{OperationID: "catalog-hist", SKUID: firstSKU, WarehouseID: wh, Quantity: "1", UnitCost: "10"}, "catalog-hist")
	mustMove2(t, res, err)
	editBlocked := authReq(a, http.MethodPatch, "/api/catalog/items/"+itoa(itemID), map[string]any{"name": "Feijão Carioca <b>", "category_id": catID, "unit_code": "UN", "minimum_stock": "3", "notes": "ok", "active": true, "version": crudVersion(t, a, "inventory_items", itemID)}, director, "")
	if editBlocked.Code != http.StatusConflict || !strings.Contains(editBlocked.Body.String(), "Unidade não pode ser alterada") {
		t.Fatalf("unit edit with history status=%d body=%s", editBlocked.Code, editBlocked.Body.String())
	}
	editOK := authReq(a, http.MethodPatch, "/api/catalog/items/"+itoa(itemID), map[string]any{"name": "Feijão Carioca Corrigido", "category_id": catID, "unit_code": "KG", "minimum_stock": "4", "notes": "ok", "active": true, "version": crudVersion(t, a, "inventory_items", itemID)}, director, "")
	if editOK.Code != http.StatusOK {
		t.Fatalf("allowed item edit status=%d body=%s", editOK.Code, editOK.Body.String())
	}
	skuEdit := authReq(a, http.MethodPatch, "/api/catalog/skus/"+itoa(firstSKU), map[string]any{"name": "Feijão Marca A Corrigido", "barcode": "7891000000011", "active": true, "version": crudVersion(t, a, "product_skus", firstSKU)}, director, "")
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


func TestCatalogParentManagementReassignAndHistorySnapshot(t *testing.T) {
	a, _, wh, _ := newStockTestApp(t)
	createTestUser(t, a, "director-catalog-parent", "secret", "Diretor", true)
	director := doLogin(t, a, "director-catalog-parent", "secret")
	catID := lookupID(t, a, `SELECT id FROM categories WHERE name='Alimentos'`)

	parentA := authReq(a, http.MethodPost, "/api/catalog/items", map[string]any{"name": "Feijão Teste Pai A", "category_id": catID, "unit_code": "KG", "minimum_stock": "2,5"}, director, "")
	if parentA.Code != http.StatusCreated { t.Fatalf("create parent A status=%d body=%s", parentA.Code, parentA.Body.String()) }
	parentAID := jsonID(t, parentA.Body.Bytes())
	var minimum string
	if err := a.db.QueryRow(`SELECT minimum_stock::text FROM inventory_items WHERE id=$1`, parentAID).Scan(&minimum); err != nil || minimum != "2.500" {
		t.Fatalf("minimum normalization got=%q err=%v", minimum, err)
	}

	parentB := authReq(a, http.MethodPost, "/api/catalog/items", map[string]any{"name": "Feijão Teste Pai B", "category_id": catID, "unit_code": "KG", "minimum_stock": "1"}, director, "")
	parentBID := jsonID(t, parentB.Body.Bytes())
	parentC := authReq(a, http.MethodPost, "/api/catalog/items", map[string]any{"name": "Feijão Teste Pai C", "category_id": catID, "unit_code": "L", "minimum_stock": "1"}, director, "")
	parentCID := jsonID(t, parentC.Body.Bytes())

	skuResp := authReq(a, http.MethodPost, "/api/catalog/skus", map[string]any{"item_id": parentAID, "name": "Feijão Teste Marca", "barcode": "7890000999911"}, director, "")
	if skuResp.Code != http.StatusCreated { t.Fatalf("create sku status=%d body=%s", skuResp.Code, skuResp.Body.String()) }
	skuID := jsonID(t, skuResp.Body.Bytes())
	moveRes, moveErr := a.createEntry(context.Background(), movementRequest{OperationID: "catalog-parent-history", SKUID: skuID, WarehouseID: wh, Quantity: "3", UnitCost: "10"}, "catalog-parent-history")
	mustMove2(t, moveRes, moveErr)

	var snapshot int64
	if err := a.db.QueryRow(`SELECT item_id FROM movement_lines WHERE sku_id=$1 LIMIT 1`, skuID).Scan(&snapshot); err != nil || snapshot != parentAID {
		t.Fatalf("history snapshot got=%d want=%d err=%v", snapshot, parentAID, err)
	}

	move := authReq(a, http.MethodPatch, "/api/catalog/skus/"+itoa(skuID), map[string]any{"item_id": parentBID, "name": "Feijão Teste Marca", "barcode": "7890000999911", "active": true, "version": crudVersion(t, a, "product_skus", skuID)}, director, "")
	if move.Code != http.StatusOK { t.Fatalf("reassign same unit status=%d body=%s", move.Code, move.Body.String()) }
	var currentParent, historicalParent int64
	if err := a.db.QueryRow(`SELECT item_id FROM product_skus WHERE id=$1`, skuID).Scan(&currentParent); err != nil { t.Fatal(err) }
	if err := a.db.QueryRow(`SELECT item_id FROM movement_lines WHERE sku_id=$1 LIMIT 1`, skuID).Scan(&historicalParent); err != nil { t.Fatal(err) }
	if currentParent != parentBID || historicalParent != parentAID {
		t.Fatalf("reassign current=%d history=%d want current=%d history=%d", currentParent, historicalParent, parentBID, parentAID)
	}

	badMove := authReq(a, http.MethodPatch, "/api/catalog/skus/"+itoa(skuID), map[string]any{"item_id": parentCID, "name": "Feijão Teste Marca", "barcode": "7890000999911", "active": true, "version": crudVersion(t, a, "product_skus", skuID)}, director, "")
	if badMove.Code != http.StatusConflict || !strings.Contains(badMove.Body.String(), "mesma unidade") {
		t.Fatalf("cross-unit historical reassign status=%d body=%s", badMove.Code, badMove.Body.String())
	}

	search := authReq(a, http.MethodGet, "/api/catalog/items?q=7890000999911", nil, director, "")
	if search.Code != http.StatusOK || !strings.Contains(search.Body.String(), "Feijão Teste Pai B") {
		t.Fatalf("catalog barcode search status=%d body=%s", search.Code, search.Body.String())
	}

	dup := authReq(a, http.MethodPost, "/api/catalog/items", map[string]any{"name": " feijão teste pai b ", "category_id": catID, "unit_code": "KG", "minimum_stock": "0"}, director, "")
	if dup.Code != http.StatusConflict { t.Fatalf("duplicate normalized parent status=%d body=%s", dup.Code, dup.Body.String()) }
}


func TestCatalogNormalizedNameUniqueAtDatabaseLayer(t *testing.T) {
	a, _, _, _ := newStockTestApp(t)
	createTestUser(t, a, "director-catalog-normalized-unique", "secret", "Diretor", true)
	director := doLogin(t, a, "director-catalog-normalized-unique", "secret")
	catID := lookupID(t, a, "SELECT id FROM categories WHERE name='Alimentos'")

	start := make(chan struct{})
	codes := make([]int, 2)
	names := []string{"  Item Concorrente Final  ", "item concorrente final"}
	var wg sync.WaitGroup
	wg.Add(2)
	for i := range names {
		i := i
		go func() {
			defer wg.Done()
			<-start
			rr := authReq(a, http.MethodPost, "/api/catalog/items", map[string]any{
				"name": names[i],
				"category_id": catID,
				"unit_code": "UN",
				"minimum_stock": "0",
			}, director, "")
			codes[i] = rr.Code
		}()
	}
	close(start)
	wg.Wait()

	created, conflicted := 0, 0
	for _, code := range codes {
		switch code {
		case http.StatusCreated:
			created++
		case http.StatusConflict:
			conflicted++
		default:
			t.Fatalf("unexpected concurrent normalized-name status=%d all=%v", code, codes)
		}
	}
	if created != 1 || conflicted != 1 {
		t.Fatalf("normalized concurrent create must be 1 created/1 conflict, got %v", codes)
	}

	var count int
	if err := a.db.QueryRow(
		"SELECT count(*) FROM inventory_items WHERE lower(btrim(name))=lower(btrim($1))",
		"item concorrente final",
	).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("normalized duplicate count=%d want 1", count)
	}

	_, err := a.db.Exec(
		"INSERT INTO inventory_items (name,category_id,unit_code,minimum_stock,active) VALUES ($1,$2,'UN',0,true)",
		" ITEM CONCORRENTE FINAL ",
		catID,
	)
	if err == nil {
		t.Fatal("database accepted normalized duplicate item name")
	}
	if !strings.Contains(err.Error(), "idx_inventory_items_normalized_name_unique") {
		t.Fatalf("unexpected duplicate database error: %v", err)
	}
}


func TestCatalogParentPaginationKeyset(t *testing.T) {
	a, _, _, _ := newStockTestApp(t)
	createTestUser(t, a, "director-catalog-page", "secret", "Diretor", true)
	director := doLogin(t, a, "director-catalog-page", "secret")
	catID := lookupID(t, a, "SELECT id FROM categories WHERE name='Alimentos'")

	for i := 1; i <= 61; i++ {
		name := fmt.Sprintf("Paginacao Teste %03d", i)
		if _, err := a.db.Exec(`
INSERT INTO inventory_items (name,category_id,unit_code,minimum_stock,active)
VALUES ($1,$2,'UN',0,true)`, name, catID); err != nil {
			t.Fatal(err)
		}
	}

	type page struct {
		Items []struct {
			ID   int64  `json:"id"`
			Name string `json:"name"`
		} `json:"items"`
		HasMore bool   `json:"has_more"`
		Cursor  string `json:"next_cursor"`
	}
	readPage := func(path string) page {
		rr := authReq(a, http.MethodGet, path, nil, director, "")
		if rr.Code != http.StatusOK {
			t.Fatalf("page status=%d body=%s", rr.Code, rr.Body.String())
		}
		var p page
		if err := json.Unmarshal(rr.Body.Bytes(), &p); err != nil {
			t.Fatal(err)
		}
		return p
	}

	p1 := readPage("/api/catalog/items?paged=1&q=Paginacao%20Teste")
	if len(p1.Items) != 30 || !p1.HasMore || p1.Cursor == "" {
		t.Fatalf("page1 len=%d has_more=%v cursor=%q", len(p1.Items), p1.HasMore, p1.Cursor)
	}
	p2 := readPage("/api/catalog/items?paged=1&q=Paginacao%20Teste&cursor=" + p1.Cursor)
	if len(p2.Items) != 30 || !p2.HasMore || p2.Cursor == "" {
		t.Fatalf("page2 len=%d has_more=%v cursor=%q", len(p2.Items), p2.HasMore, p2.Cursor)
	}
	p3 := readPage("/api/catalog/items?paged=1&q=Paginacao%20Teste&cursor=" + p2.Cursor)
	if len(p3.Items) != 1 || p3.HasMore || p3.Cursor != "" {
		t.Fatalf("page3 len=%d has_more=%v cursor=%q", len(p3.Items), p3.HasMore, p3.Cursor)
	}

	seen := map[int64]bool{}
	var names []string
	for _, p := range []page{p1, p2, p3} {
		for _, item := range p.Items {
			if seen[item.ID] {
				t.Fatalf("duplicate item id=%d", item.ID)
			}
			seen[item.ID] = true
			names = append(names, item.Name)
		}
	}
	if len(seen) != 61 {
		t.Fatalf("unique items=%d want=61", len(seen))
	}
	for i, name := range names {
		want := fmt.Sprintf("Paginacao Teste %03d", i+1)
		if name != want {
			t.Fatalf("item[%d]=%q want=%q", i, name, want)
		}
	}

	bad := authReq(a, http.MethodGet, "/api/catalog/items?paged=1&cursor=not-a-cursor", nil, director, "")
	if bad.Code != http.StatusBadRequest {
		t.Fatalf("invalid cursor status=%d body=%s", bad.Code, bad.Body.String())
	}
}
