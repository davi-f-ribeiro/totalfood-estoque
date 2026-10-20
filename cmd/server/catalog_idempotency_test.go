package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"
)

// Contrato de recuperação dos cadastros (item, marca e produto): o mesmo
// operation_id devolve o cadastro original, sem criar outro registro; o mesmo
// operation_id com outros dados é recusado sem alterar nada.

type catalogCreateCase struct {
	kind  string
	path  string
	table string
	body  func(op string) map[string]any
	edit  func(map[string]any)
}

func catalogCreateCases(t *testing.T, a *app, prefix string) []catalogCreateCase {
	t.Helper()
	catID := lookupID(t, a, `SELECT id FROM categories WHERE name='Alimentos'`)
	parent := createCatalogTestItem(t, a, prefix+" Item Base", catID)
	brand := createTestBrand(t, a, prefix+" Marca Base")
	return []catalogCreateCase{
		{
			kind: "item", path: "/api/catalog/items", table: "inventory_items",
			body: func(op string) map[string]any {
				return map[string]any{"operation_id": op, "name": prefix + " Item Novo", "category_id": catID, "unit_code": "KG", "minimum_stock": "2"}
			},
			edit: func(m map[string]any) { m["minimum_stock"] = "9" },
		},
		{
			kind: "marca", path: "/api/catalog/brands", table: "brands",
			body: func(op string) map[string]any {
				return map[string]any{"operation_id": op, "name": prefix + " Marca Nova"}
			},
			edit: func(m map[string]any) { m["name"] = prefix + " Marca Diferente" },
		},
		{
			kind: "produto", path: "/api/catalog/skus", table: "product_skus",
			body: func(op string) map[string]any {
				return map[string]any{"operation_id": op, "item_id": parent, "brand_id": brand, "name": "Original", "content_quantity": "1", "barcode": catalogTestBarcode(prefix)}
			},
			edit: func(m map[string]any) { m["content_quantity"] = "12" },
		},
	}
}

func createCatalogTestItem(t *testing.T, a *app, name string, catID int64) int64 {
	t.Helper()
	var id int64
	if err := a.db.QueryRow(`INSERT INTO inventory_items (name,category_id,unit_code,minimum_stock) VALUES ($1,$2,'KG',0) RETURNING id`, name, catID).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

func catalogTestBarcode(seed string) string {
	h := 0
	for _, r := range seed {
		h = (h*31 + int(r)) % 1000000000
	}
	return fmt.Sprintf("789%010d", h)
}

func catalogRowsByOperation(t *testing.T, a *app, table, op string) int {
	t.Helper()
	queries := map[string]string{
		"inventory_items": `SELECT count(*) FROM inventory_items WHERE operation_id=$1`,
		"brands":          `SELECT count(*) FROM brands WHERE operation_id=$1`,
		"product_skus":    `SELECT count(*) FROM product_skus WHERE operation_id=$1`,
	}
	var n int
	if err := a.db.QueryRow(queries[table], op).Scan(&n); err != nil {
		t.Fatalf("count %s by operation: %v", table, err)
	}
	return n
}

type catalogCreateResponse struct {
	ID       int64 `json:"id"`
	Version  int64 `json:"version"`
	Replayed bool  `json:"replayed"`
}

func decodeCatalogCreate(t *testing.T, raw []byte) catalogCreateResponse {
	t.Helper()
	var out catalogCreateResponse
	if err := json.Unmarshal(raw, &out); err != nil || out.ID <= 0 {
		t.Fatalf("catalog create response=%s err=%v", raw, err)
	}
	return out
}

func TestCatalogCreateReplaysSameOperationWithoutDuplicating(t *testing.T) {
	a, _, _, _ := newStockTestApp(t)
	createTestUser(t, a, "director-catalog-replay", "secret", "Diretor", true)
	director := doLogin(t, a, "director-catalog-replay", "secret")

	for _, tc := range catalogCreateCases(t, a, "Replay") {
		t.Run(tc.kind, func(t *testing.T) {
			op := "catalog-replay-" + tc.kind
			first := authReq(a, http.MethodPost, tc.path, tc.body(op), director, "")
			if first.Code != http.StatusCreated {
				t.Fatalf("first create status=%d body=%s", first.Code, first.Body.String())
			}
			created := decodeCatalogCreate(t, first.Body.Bytes())
			if created.Replayed {
				t.Fatal("first create must not be marked as replayed")
			}

			// A resposta se perdeu e o operador envia de novo: devolve o cadastro original.
			again := authReq(a, http.MethodPost, tc.path, tc.body(op), director, "")
			if again.Code != http.StatusOK {
				t.Fatalf("replay status=%d body=%s", again.Code, again.Body.String())
			}
			replayed := decodeCatalogCreate(t, again.Body.Bytes())
			if replayed.ID != created.ID || !replayed.Replayed || replayed.Version != created.Version {
				t.Fatalf("replay=%+v want id=%d version=%d replayed", replayed, created.ID, created.Version)
			}
			if n := catalogRowsByOperation(t, a, tc.table, op); n != 1 {
				t.Fatalf("rows for operation=%d want 1", n)
			}
		})
	}
}

func TestCatalogCreateRejectsSameOperationWithOtherData(t *testing.T) {
	a, _, _, _ := newStockTestApp(t)
	createTestUser(t, a, "director-catalog-conflict", "secret", "Diretor", true)
	director := doLogin(t, a, "director-catalog-conflict", "secret")

	for _, tc := range catalogCreateCases(t, a, "Conflito") {
		t.Run(tc.kind, func(t *testing.T) {
			op := "catalog-conflict-" + tc.kind
			original := tc.body(op)
			if rr := authReq(a, http.MethodPost, tc.path, original, director, ""); rr.Code != http.StatusCreated {
				t.Fatalf("create status=%d body=%s", rr.Code, rr.Body.String())
			}
			changed := tc.body(op)
			tc.edit(changed)
			rr := authReq(a, http.MethodPost, tc.path, changed, director, "")
			if rr.Code != http.StatusConflict || !strings.Contains(rr.Body.String(), "já foi enviado com outros dados") {
				t.Fatalf("conflict status=%d body=%s", rr.Code, rr.Body.String())
			}
			if strings.Contains(rr.Body.String(), "operation_id") {
				t.Fatalf("conflict message must not expose technical terms: %s", rr.Body.String())
			}
			if n := catalogRowsByOperation(t, a, tc.table, op); n != 1 {
				t.Fatalf("rows for operation=%d want 1", n)
			}
		})
	}
	var minimum string
	if err := a.db.QueryRow(`SELECT minimum_stock::text FROM inventory_items WHERE operation_id='catalog-conflict-item'`).Scan(&minimum); err != nil || minimum != "2.000" {
		t.Fatalf("conflicting replay changed the original item minimum=%q err=%v", minimum, err)
	}
}

func TestCatalogCreateOperationBelongsToItsAuthor(t *testing.T) {
	a, _, _, _ := newStockTestApp(t)
	createTestUser(t, a, "director-catalog-owner-a", "secret", "Diretor", true)
	createTestUser(t, a, "director-catalog-owner-b", "secret", "Diretor", true)
	first := doLogin(t, a, "director-catalog-owner-a", "secret")
	second := doLogin(t, a, "director-catalog-owner-b", "secret")

	body := map[string]any{"operation_id": "catalog-owner-brand", "name": "Marca do Autor"}
	if rr := authReq(a, http.MethodPost, "/api/catalog/brands", body, first, ""); rr.Code != http.StatusCreated {
		t.Fatalf("create status=%d body=%s", rr.Code, rr.Body.String())
	}
	rr := authReq(a, http.MethodPost, "/api/catalog/brands", body, second, "")
	if rr.Code != http.StatusConflict {
		t.Fatalf("other user replay status=%d body=%s", rr.Code, rr.Body.String())
	}
}

func TestCatalogCreateRequiresOperationID(t *testing.T) {
	a, _, _, _ := newStockTestApp(t)
	createTestUser(t, a, "director-catalog-missing-op", "secret", "Diretor", true)
	director := doLogin(t, a, "director-catalog-missing-op", "secret")

	for _, tc := range catalogCreateCases(t, a, "Sem envio") {
		t.Run(tc.kind, func(t *testing.T) {
			body := tc.body("  ")
			rr := authReq(a, http.MethodPost, tc.path, body, director, "")
			if rr.Code != http.StatusBadRequest || !strings.Contains(rr.Body.String(), "Não foi possível identificar este envio") {
				t.Fatalf("missing operation status=%d body=%s", rr.Code, rr.Body.String())
			}
		})
	}
}

func TestCatalogCreateConcurrentSameOperationCreatesOnce(t *testing.T) {
	a, _, _, _ := newStockTestApp(t)
	createTestUser(t, a, "director-catalog-race", "secret", "Diretor", true)
	director := doLogin(t, a, "director-catalog-race", "secret")

	for _, tc := range catalogCreateCases(t, a, "Simultaneo") {
		t.Run(tc.kind, func(t *testing.T) {
			op := "catalog-race-" + tc.kind
			const workers = 6
			start := make(chan struct{})
			codes := make([]int, workers)
			ids := make([]int64, workers)
			var wg sync.WaitGroup
			for i := 0; i < workers; i++ {
				wg.Add(1)
				go func(i int) {
					defer wg.Done()
					<-start
					rr := authReq(a, http.MethodPost, tc.path, tc.body(op), director, "")
					codes[i] = rr.Code
					var out catalogCreateResponse
					_ = json.Unmarshal(rr.Body.Bytes(), &out)
					ids[i] = out.ID
				}(i)
			}
			close(start)
			wg.Wait()
			created := 0
			for i, code := range codes {
				switch code {
				case http.StatusCreated:
					created++
				case http.StatusOK:
				default:
					t.Fatalf("concurrent create status=%d all=%v", code, codes)
				}
				if ids[i] != ids[0] {
					t.Fatalf("concurrent create returned different records %v", ids)
				}
			}
			if created != 1 {
				t.Fatalf("concurrent create must create exactly once, statuses=%v", codes)
			}
			if n := catalogRowsByOperation(t, a, tc.table, op); n != 1 {
				t.Fatalf("rows for operation=%d want 1", n)
			}
		})
	}
}

func TestCatalogCreateKeepsDomainUniquenessAcrossOperations(t *testing.T) {
	a, _, _, _ := newStockTestApp(t)
	createTestUser(t, a, "director-catalog-unique-ops", "secret", "Diretor", true)
	director := doLogin(t, a, "director-catalog-unique-ops", "secret")
	catID := lookupID(t, a, `SELECT id FROM categories WHERE name='Alimentos'`)
	parent := createCatalogTestItem(t, a, "Unico Item Pai", catID)
	brand := createTestBrand(t, a, "Unico Marca")

	// Mesmo item com produtos diferentes continua permitido.
	sku := func(op, barcode, content string) *httpResponse {
		rr := authReq(a, http.MethodPost, "/api/catalog/skus", map[string]any{"operation_id": op, "item_id": parent, "brand_id": brand, "name": "Original", "content_quantity": content, "barcode": barcode}, director, "")
		return &httpResponse{rr.Code, rr.Body.String()}
	}
	if r := sku("unique-sku-1", "7890000777001", "1"); r.code != http.StatusCreated {
		t.Fatalf("first sku status=%d body=%s", r.code, r.body)
	}
	if r := sku("unique-sku-2", "7890000777002", "12"); r.code != http.StatusCreated {
		t.Fatalf("second sku of the same item status=%d body=%s", r.code, r.body)
	}
	// Código de barras já cadastrado por outra operação continua bloqueado.
	if r := sku("unique-sku-3", "7890000777001", "6"); r.code != http.StatusConflict || !strings.Contains(r.body, "código de barras") {
		t.Fatalf("barcode from another operation status=%d body=%s", r.code, r.body)
	}
	// Nome de item e de marca continuam únicos entre operações diferentes.
	if rr := authReq(a, http.MethodPost, "/api/catalog/items", map[string]any{"operation_id": "unique-item-dup", "name": " unico item pai ", "category_id": catID, "unit_code": "KG", "minimum_stock": "0"}, director, ""); rr.Code != http.StatusConflict {
		t.Fatalf("duplicate item name status=%d body=%s", rr.Code, rr.Body.String())
	}
	if rr := authReq(a, http.MethodPost, "/api/catalog/brands", map[string]any{"operation_id": "unique-brand-dup", "name": "UNICO MARCA"}, director, ""); rr.Code != http.StatusConflict {
		t.Fatalf("duplicate brand name status=%d body=%s", rr.Code, rr.Body.String())
	}
	// Uma recusa de domínio não consome o envio: corrigido, o mesmo envio grava.
	if r := sku("unique-sku-3", "7890000777003", "6"); r.code != http.StatusCreated {
		t.Fatalf("corrected sku after domain refusal status=%d body=%s", r.code, r.body)
	}
}

func TestCatalogCreateLegacyRowsAndEditsUnaffected(t *testing.T) {
	a, _, _, _ := newStockTestApp(t)
	createTestUser(t, a, "director-catalog-legacy", "secret", "Diretor", true)
	director := doLogin(t, a, "director-catalog-legacy", "secret")
	catID := lookupID(t, a, `SELECT id FROM categories WHERE name='Alimentos'`)

	legacy := createCatalogTestItem(t, a, "Legado Sem Envio", catID)
	var op *string
	if err := a.db.QueryRow(`SELECT operation_id FROM inventory_items WHERE id=$1`, legacy).Scan(&op); err != nil || op != nil {
		t.Fatalf("legacy row operation=%v err=%v", op, err)
	}
	rr := authReq(a, http.MethodPatch, fmt.Sprintf("/api/catalog/items/%d", legacy), map[string]any{"name": "Legado Editado", "category_id": catID, "unit_code": "KG", "minimum_stock": "1", "version": crudVersion(t, a, "inventory_items", legacy)}, director, "")
	if rr.Code != http.StatusOK {
		t.Fatalf("edit legacy item status=%d body=%s", rr.Code, rr.Body.String())
	}

	created := authReq(a, http.MethodPost, "/api/catalog/items", map[string]any{"operation_id": "legacy-then-edit", "name": "Criado Pelo Envio", "category_id": catID, "unit_code": "KG", "minimum_stock": "1"}, director, "")
	if created.Code != http.StatusCreated {
		t.Fatalf("create status=%d body=%s", created.Code, created.Body.String())
	}
	id := jsonID(t, created.Body.Bytes())
	if rr := authReq(a, http.MethodPatch, fmt.Sprintf("/api/catalog/items/%d", id), map[string]any{"name": "Criado E Editado", "category_id": catID, "unit_code": "KG", "minimum_stock": "3", "version": int64(1)}, director, ""); rr.Code != http.StatusOK {
		t.Fatalf("edit status=%d body=%s", rr.Code, rr.Body.String())
	}
	// O envio original repetido depois da edição devolve o registro, sem desfazer a edição.
	again := authReq(a, http.MethodPost, "/api/catalog/items", map[string]any{"operation_id": "legacy-then-edit", "name": "Criado Pelo Envio", "category_id": catID, "unit_code": "KG", "minimum_stock": "1"}, director, "")
	if again.Code != http.StatusOK {
		t.Fatalf("replay after edit status=%d body=%s", again.Code, again.Body.String())
	}
	out := decodeCatalogCreate(t, again.Body.Bytes())
	var name string
	if err := a.db.QueryRow(`SELECT name FROM inventory_items WHERE id=$1`, id).Scan(&name); err != nil || out.ID != id || out.Version != 2 || name != "Criado E Editado" {
		t.Fatalf("replay after edit out=%+v name=%q err=%v", out, name, err)
	}
	// Edição não aceita identificador de envio: o contrato vale só para a criação.
	if rr := authReq(a, http.MethodPatch, fmt.Sprintf("/api/catalog/items/%d", id), map[string]any{"operation_id": "x", "name": "Criado E Editado", "category_id": catID, "unit_code": "KG", "minimum_stock": "3", "version": int64(2)}, director, ""); rr.Code != http.StatusBadRequest {
		t.Fatalf("edit with operation id status=%d body=%s", rr.Code, rr.Body.String())
	}
}

type httpResponse struct {
	code int
	body string
}
