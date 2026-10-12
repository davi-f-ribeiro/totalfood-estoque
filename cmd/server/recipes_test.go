package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func recipeResponse(t *testing.T, rr *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var out map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &out); err != nil {
		t.Fatalf("invalid json %q: %v", rr.Body.String(), err)
	}
	return out
}

func recipeTestItem(t *testing.T, a *app, name, unit string) int64 {
	t.Helper()
	var id int64
	err := a.db.QueryRow(`INSERT INTO inventory_items (name,category_id,unit_code) SELECT $1,id,$2 FROM categories WHERE name='Alimentos' RETURNING id`, name, unit).Scan(&id)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func recipeTestEntry(t *testing.T, a *app, op string, sku, wh, userID int64, quantity, cost string) {
	t.Helper()
	if _, err := a.createEntry(context.Background(), movementRequest{
		OperationID: op, SKUID: sku, WarehouseID: wh, Quantity: quantity, UnitCost: cost, UserID: userID,
	}, op+"-hash"); err != nil {
		t.Fatalf("entry %s: %v", op, err)
	}
}

func recipeTestCustomer(t *testing.T, a *app, director loginData, name string) int64 {
	t.Helper()
	rr := authReq(a, http.MethodPost, "/api/customers", map[string]any{"name": name}, director, "")
	if rr.Code != http.StatusCreated {
		t.Fatalf("create customer status=%d body=%s", rr.Code, rr.Body.String())
	}
	return jsonID(t, rr.Body.Bytes())
}

func TestRecipeCostUsesStockAverageCost(t *testing.T) {
	a, sku, wh1, wh2 := newStockTestApp(t)
	userID := createTestUser(t, a, "recipe-cost-director", "secret", "Diretor", true)
	director := doLogin(t, a, "recipe-cost-director", "secret")
	// Entram 10 KG a 20 no depósito 1 e 5 KG a 40 no depósito 2. A saída de 5 KG
	// do depósito 1 grava o custo médio daquele depósito (20). Sobram 10 KG com
	// valor 200 + 200 - 100 = 300, então o preço médio do item é 30.
	recipeTestEntry(t, a, "recipe-cost-e1", sku, wh1, userID, "10", "20")
	recipeTestEntry(t, a, "recipe-cost-e2", sku, wh2, userID, "5", "40")
	if _, err := a.createExit(context.Background(), movementRequest{
		OperationID: "recipe-cost-s1", SKUID: sku, WarehouseID: wh1, Quantity: "5", UserID: userID,
	}, "recipe-cost-s1-hash"); err != nil {
		t.Fatalf("exit: %v", err)
	}
	arroz := lookupID(t, a, `SELECT id FROM inventory_items WHERE name='Arroz'`)

	create := authReq(a, http.MethodPost, "/api/recipes", map[string]any{
		"name": "Arroz doce", "portions": "4",
		"lines": []map[string]any{{"item_id": arroz, "quantity": "0,5", "correction_factor": "1,25"}},
	}, director, "")
	if create.Code != http.StatusCreated {
		t.Fatalf("create recipe status=%d body=%s", create.Code, create.Body.String())
	}
	id := jsonID(t, create.Body.Bytes())

	detail := authReq(a, http.MethodGet, "/api/recipes/"+itoa(id), nil, director, "")
	if detail.Code != http.StatusOK {
		t.Fatalf("detail status=%d body=%s", detail.Code, detail.Body.String())
	}
	body := recipeResponse(t, detail)
	// 0,5 KG x 1,25 = 0,625 KG brutos x 30 = 18,75 por receita; / 4 porções = 4,6875.
	if body["incomplete"] != false || body["cost_per_portion"] != "4.6875" {
		t.Fatalf("cost per portion=%v incomplete=%v want 4.6875/false", body["cost_per_portion"], body["incomplete"])
	}
	line := body["lines"].([]any)[0].(map[string]any)
	if line["gross_quantity"] != "0.6250" || line["avg_cost"] != "30.0000" || line["line_cost"] != "18.7500" {
		t.Fatalf("line gross=%v avg=%v cost=%v", line["gross_quantity"], line["avg_cost"], line["line_cost"])
	}
	if body["total_cost"] != "18.7500" {
		t.Fatalf("total=%v want 18.7500", body["total_cost"])
	}

	list := authReq(a, http.MethodGet, "/api/recipes", nil, director, "")
	if list.Code != http.StatusOK || !strings.Contains(list.Body.String(), `"cost_per_portion":"4.6875"`) {
		t.Fatalf("list status=%d body=%s", list.Code, list.Body.String())
	}
}

func TestRecipeWithIngredientWithoutStockIsIncomplete(t *testing.T) {
	a, sku, wh1, _ := newStockTestApp(t)
	userID := createTestUser(t, a, "recipe-incomplete-director", "secret", "Diretor", true)
	director := doLogin(t, a, "recipe-incomplete-director", "secret")
	recipeTestEntry(t, a, "recipe-incomplete-e1", sku, wh1, userID, "10", "20")
	arroz := lookupID(t, a, `SELECT id FROM inventory_items WHERE name='Arroz'`)
	sal := recipeTestItem(t, a, "Sal", "KG")

	create := authReq(a, http.MethodPost, "/api/recipes", map[string]any{
		"name": "Arroz com sal", "portions": "2",
		"lines": []map[string]any{
			{"item_id": arroz, "quantity": "0.5"},
			{"item_id": sal, "quantity": "0.01"},
		},
	}, director, "")
	if create.Code != http.StatusCreated {
		t.Fatalf("create recipe status=%d body=%s", create.Code, create.Body.String())
	}
	body := recipeResponse(t, authReq(a, http.MethodGet, "/api/recipes/"+itoa(jsonID(t, create.Body.Bytes())), nil, director, ""))
	if body["incomplete"] != true || body["cost_per_portion"] != nil {
		t.Fatalf("incomplete=%v cost=%v want true/nil", body["incomplete"], body["cost_per_portion"])
	}
	for _, raw := range body["lines"].([]any) {
		line := raw.(map[string]any)
		switch line["item"] {
		case "Arroz":
			if line["line_cost"] != "10.0000" {
				t.Fatalf("Arroz line cost=%v want 10.0000", line["line_cost"])
			}
		case "Sal":
			if line["avg_cost"] != nil || line["line_cost"] != nil {
				t.Fatalf("Sal must be without cost, got avg=%v cost=%v", line["avg_cost"], line["line_cost"])
			}
		}
	}
}

func TestRecipeEndpointsRequireCostsPermission(t *testing.T) {
	a, _, _, _ := newStockTestApp(t)
	createTestUser(t, a, "recipe-perm-director", "secret", "Diretor", true)
	createTestUser(t, a, "recipe-perm-manager", "secret", "Gerente", true)
	createTestUser(t, a, "recipe-perm-stock", "secret", "Estoquista", true)
	createTestUser(t, a, "recipe-perm-sales", "secret", "Vendas", true)
	director := doLogin(t, a, "recipe-perm-director", "secret")
	manager := doLogin(t, a, "recipe-perm-manager", "secret")
	stock := doLogin(t, a, "recipe-perm-stock", "secret")
	sales := doLogin(t, a, "recipe-perm-sales", "secret")

	if rr := authReq(a, http.MethodGet, "/api/recipes", nil, loginData{}, ""); rr.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous recipes status=%d", rr.Code)
	}
	for name, login := range map[string]loginData{"estoquista": stock, "vendas": sales} {
		for _, path := range []string{"/api/recipes", "/api/customers"} {
			if rr := authReq(a, http.MethodGet, path, nil, login, ""); rr.Code != http.StatusForbidden {
				t.Fatalf("%s GET %s status=%d want 403", name, path, rr.Code)
			}
		}
	}
	for _, path := range []string{"/api/recipes", "/api/customers"} {
		if rr := authReq(a, http.MethodGet, path, nil, manager, ""); rr.Code != http.StatusOK {
			t.Fatalf("manager GET %s status=%d want 200", path, rr.Code)
		}
	}
	if rr := authReq(a, http.MethodPost, "/api/customers", map[string]any{"name": "Cliente Gerente"}, manager, ""); rr.Code != http.StatusForbidden {
		t.Fatalf("manager POST customer status=%d want 403", rr.Code)
	}
	arroz := lookupID(t, a, `SELECT id FROM inventory_items WHERE name='Arroz'`)
	if rr := authReq(a, http.MethodPost, "/api/recipes", map[string]any{
		"name": "Receita Gerente", "portions": "1",
		"lines": []map[string]any{{"item_id": arroz, "quantity": "1"}},
	}, manager, ""); rr.Code != http.StatusForbidden {
		t.Fatalf("manager POST recipe status=%d want 403", rr.Code)
	}
	if rr := authReq(a, http.MethodPost, "/api/customers", map[string]any{"name": "Cliente Diretor"}, director, ""); rr.Code != http.StatusCreated {
		t.Fatalf("director POST customer status=%d body=%s", rr.Code, rr.Body.String())
	}
}

func TestRecipeWritesAreAtomicValidatedAndVersioned(t *testing.T) {
	a, _, _, _ := newStockTestApp(t)
	createTestUser(t, a, "recipe-write-director", "secret", "Diretor", true)
	director := doLogin(t, a, "recipe-write-director", "secret")
	arroz := lookupID(t, a, `SELECT id FROM inventory_items WHERE name='Arroz'`)
	customer := recipeTestCustomer(t, a, director, "Cliente C")

	valid := func(name string) map[string]any {
		return map[string]any{
			"name": name, "portions": "1",
			"lines": []map[string]any{{"item_id": arroz, "quantity": "1"}},
		}
	}

	t.Run("validation messages", func(t *testing.T) {
		cases := map[string]struct {
			mutate func(m map[string]any)
			want   string
		}{
			"factor below one": {func(m map[string]any) {
				m["lines"] = []map[string]any{{"item_id": arroz, "quantity": "1", "correction_factor": "0.9"}}
			}, "fator de correção"},
			"duplicate ingredient": {func(m map[string]any) {
				m["lines"] = []map[string]any{{"item_id": arroz, "quantity": "1"}, {"item_id": arroz, "quantity": "2"}}
			}, "não pode aparecer duas vezes"},
			"no ingredient": {func(m map[string]any) { m["lines"] = []map[string]any{} }, "ao menos um ingrediente"},
			"zero portions": {func(m map[string]any) { m["portions"] = "0" }, "rendimento"},
		}
		for name, tc := range cases {
			body := valid("Receita Inválida")
			tc.mutate(body)
			rr := authReq(a, http.MethodPost, "/api/recipes", body, director, "")
			if rr.Code != http.StatusBadRequest || !strings.Contains(rr.Body.String(), tc.want) {
				t.Fatalf("%s status=%d body=%s want message containing %q", name, rr.Code, rr.Body.String(), tc.want)
			}
		}
	})

	t.Run("invalid ingredient leaves no recipe behind", func(t *testing.T) {
		body := valid("Receita Atômica")
		body["lines"] = []map[string]any{{"item_id": 999999, "quantity": "1"}}
		rr := authReq(a, http.MethodPost, "/api/recipes", body, director, "")
		if rr.Code != http.StatusBadRequest || !strings.Contains(rr.Body.String(), "Ingrediente inválido") {
			t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
		}
		var count int
		if err := a.db.QueryRow(`SELECT count(*) FROM recipes WHERE name='Receita Atômica'`).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != 0 {
			t.Fatalf("failed create left %d recipe rows", count)
		}
	})

	t.Run("duplicate name and stale edit", func(t *testing.T) {
		create := authReq(a, http.MethodPost, "/api/recipes", valid("Feijão"), director, "")
		if create.Code != http.StatusCreated {
			t.Fatalf("create status=%d body=%s", create.Code, create.Body.String())
		}
		id := jsonID(t, create.Body.Bytes())

		dup := authReq(a, http.MethodPost, "/api/recipes", valid("  feijão "), director, "")
		if dup.Code != http.StatusConflict || !strings.Contains(dup.Body.String(), "Já existe uma ficha técnica com este nome.") {
			t.Fatalf("duplicate status=%d body=%s", dup.Code, dup.Body.String())
		}

		first := valid("Feijão tropeiro")
		first["version"] = 1
		if rr := authReq(a, http.MethodPatch, "/api/recipes/"+itoa(id), first, director, ""); rr.Code != http.StatusOK {
			t.Fatalf("first edit status=%d body=%s", rr.Code, rr.Body.String())
		}
		stale := valid("Feijão sobrescrito")
		stale["version"] = 1
		rr := authReq(a, http.MethodPatch, "/api/recipes/"+itoa(id), stale, director, "")
		if rr.Code != http.StatusConflict || !strings.Contains(rr.Body.String(), concurrentEditMessage) {
			t.Fatalf("stale edit status=%d body=%s", rr.Code, rr.Body.String())
		}
		var name string
		var version int64
		if err := a.db.QueryRow(`SELECT name,version FROM recipes WHERE id=$1`, id).Scan(&name, &version); err != nil {
			t.Fatal(err)
		}
		if name != "Feijão tropeiro" || version != 2 {
			t.Fatalf("stale edit changed recipe name=%q version=%d", name, version)
		}

		missing := valid("Fantasma")
		missing["version"] = 1
		if rr := authReq(a, http.MethodPatch, "/api/recipes/999999", missing, director, ""); rr.Code != http.StatusNotFound {
			t.Fatalf("missing recipe status=%d body=%s", rr.Code, rr.Body.String())
		}
	})

	t.Run("customers reject duplicates and stale edits", func(t *testing.T) {
		if rr := authReq(a, http.MethodPost, "/api/customers", map[string]any{"name": " cliente c "}, director, ""); rr.Code != http.StatusConflict || !strings.Contains(rr.Body.String(), "Já existe um cliente com este nome.") {
			t.Fatalf("duplicate customer status=%d body=%s", rr.Code, rr.Body.String())
		}
		first := authReq(a, http.MethodPatch, "/api/customers/"+itoa(customer), map[string]any{"name": "Cliente C Matriz", "active": true, "version": 1}, director, "")
		if first.Code != http.StatusOK {
			t.Fatalf("first customer edit status=%d body=%s", first.Code, first.Body.String())
		}
		stale := authReq(a, http.MethodPatch, "/api/customers/"+itoa(customer), map[string]any{"name": "Cliente C Filial", "active": true, "version": 1}, director, "")
		if stale.Code != http.StatusConflict || !strings.Contains(stale.Body.String(), concurrentEditMessage) {
			t.Fatalf("stale customer edit status=%d body=%s", stale.Code, stale.Body.String())
		}
	})
}

func TestRecipePageAndOptionsFollowCostsPermission(t *testing.T) {
	a, _, _, _ := newStockTestApp(t)
	createTestUser(t, a, "recipe-ui-director", "secret", "Diretor", true)
	createTestUser(t, a, "recipe-ui-manager", "secret", "Gerente", true)
	createTestUser(t, a, "recipe-ui-stock", "secret", "Estoquista", true)
	director := doLogin(t, a, "recipe-ui-director", "secret")
	manager := doLogin(t, a, "recipe-ui-manager", "secret")
	stock := doLogin(t, a, "recipe-ui-stock", "secret")

	page := authReq(a, http.MethodGet, "/recipes", nil, director, "")
	if page.Code != http.StatusOK {
		t.Fatalf("director page status=%d", page.Code)
	}
	for _, want := range []string{"Fichas técnicas", `id="recipeNew"`, `href="/recipes"`, `data-can-write="1"`} {
		if !strings.Contains(page.Body.String(), want) {
			t.Fatalf("director page missing %q", want)
		}
	}
	readOnly := authReq(a, http.MethodGet, "/recipes", nil, manager, "")
	if readOnly.Code != http.StatusOK || strings.Contains(readOnly.Body.String(), `id="recipeNew"`) || !strings.Contains(readOnly.Body.String(), `data-can-write="0"`) {
		t.Fatalf("manager page status=%d must be read-only", readOnly.Code)
	}
	denied := authReq(a, http.MethodGet, "/recipes", nil, stock, "")
	if denied.Code != http.StatusSeeOther || denied.Header().Get("Location") != "/no-access" {
		t.Fatalf("stock page status=%d location=%q want 303 /no-access", denied.Code, denied.Header().Get("Location"))
	}

	opts := authReq(a, http.MethodGet, "/api/recipes/options", nil, director, "")
	if opts.Code != http.StatusOK || !strings.Contains(opts.Body.String(), `"Arroz"`) || !strings.Contains(opts.Body.String(), `"KG"`) {
		t.Fatalf("options status=%d body=%s", opts.Code, opts.Body.String())
	}
	if rr := authReq(a, http.MethodGet, "/api/recipes/options", nil, stock, ""); rr.Code != http.StatusForbidden {
		t.Fatalf("stock options status=%d want 403", rr.Code)
	}
}

func TestRecipeTemplateFollowsFrontendContract(t *testing.T) {
	html, err := uiFS.ReadFile("templates/recipes.html")
	if err != nil {
		t.Fatal(err)
	}
	s := string(html)
	for _, bad := range []string{"style=", "onclick=", "<script>"} {
		if strings.Contains(s, bad) {
			t.Fatalf("recipes.html must not contain %q (CSP)", bad)
		}
	}
	if !strings.Contains(s, `{{template "nav" .}}`) || !strings.Contains(s, `{{template "header" .}}`) {
		t.Fatal("recipes.html must use shared header and nav")
	}
}
