package main

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"
)

// Testes de API do planner, com banco (rodam no CI).

func plannerBody(t *testing.T, raw []byte) map[string]any {
	t.Helper()
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("invalid json %q: %v", raw, err)
	}
	return out
}

func plannerGet(t *testing.T, a *app, path string, ld loginData) map[string]any {
	t.Helper()
	rr := authReq(a, http.MethodGet, path, nil, ld, "")
	if rr.Code != http.StatusOK {
		t.Fatalf("GET %s status=%d body=%s", path, rr.Code, rr.Body.String())
	}
	return plannerBody(t, rr.Body.Bytes())
}

func TestPlannerEndToEndFromPremiseToPurchaseAndComparison(t *testing.T) {
	a, sku, wh1, _ := newStockTestApp(t)
	userID := createTestUser(t, a, "planner-director", "secret", "Diretor", true)
	director := doLogin(t, a, "planner-director", "secret")
	arroz := lookupID(t, a, `SELECT id FROM inventory_items WHERE name='Arroz'`)
	acafrao := recipeTestItem(t, a, "Açafrão", "KG")

	// Arroz entra a R$ 6 e sai todo: sem preço médio, o planner usa a última compra.
	recipeTestEntry(t, a, "planner-e1", sku, wh1, userID, "10", "6")
	if _, err := a.createExit(context.Background(), movementRequest{OperationID: "planner-s1", SKUID: sku, WarehouseID: wh1, Quantity: "10", UserID: userID}, "planner-s1-hash"); err != nil {
		t.Fatalf("exit: %v", err)
	}

	// Ficha: 10 porções de 200 g prontos usam 0,8 KG de arroz cru.
	mkRecipe := func(name string, lines []map[string]any, tags []string) int64 {
		rr := authReq(a, http.MethodPost, "/api/recipes", map[string]any{
			"name": name, "portions": "10", "lines": lines,
			"meal_function": " Base ", "portion_size": "200", "portion_unit": "g", "tags": tags,
		}, director, "")
		if rr.Code != http.StatusCreated {
			t.Fatalf("create recipe status=%d body=%s", rr.Code, rr.Body.String())
		}
		return jsonID(t, rr.Body.Bytes())
	}
	branco := mkRecipe("Arroz branco", []map[string]any{{"item_id": arroz, "quantity": "0,8"}}, []string{"Arroz Simples", "arroz simples"})
	amarelo := mkRecipe("Arroz amarelo", []map[string]any{{"item_id": arroz, "quantity": "0,8"}, {"item_id": acafrao, "quantity": "0,01"}}, nil)
	rec := plannerGet(t, a, "/api/recipes/"+itoa(branco), director)
	if rec["meal_function"] != "base" || rec["portion_size"] != "200.000" || rec["portion_unit"] != "g" || len(rec["tags"].([]any)) != 1 || rec["tags"].([]any)[0] != "arroz-simples" {
		t.Fatalf("planning fields = %v %v %v %v", rec["meal_function"], rec["portion_size"], rec["portion_unit"], rec["tags"])
	}

	// Grupo com almoço a R$ 20 por pessoa e per capita de 200 g de base.
	group := map[string]any{
		"name": "Data Center",
		"meals": []map[string]any{{"meal": "almoco", "price": "20,00", "menu": []map[string]any{
			{"meal_function": "base", "options": 1, "per_capita": "200", "per_capita_unit": "g"},
		}}},
		"rules": []map[string]any{{"kind": "exists_without", "when_tag": "feijoada", "meal_function": "feijão", "tag": "feijao-preto"}},
	}
	rr := authReq(a, http.MethodPost, "/api/customer-groups", group, director, "")
	if rr.Code != http.StatusCreated {
		t.Fatalf("create group status=%d body=%s", rr.Code, rr.Body.String())
	}
	groupID := jsonID(t, rr.Body.Bytes())
	if dup := authReq(a, http.MethodPost, "/api/customer-groups", group, director, ""); dup.Code != http.StatusConflict {
		t.Fatalf("duplicate group status=%d", dup.Code)
	}
	groups := authReq(a, http.MethodGet, "/api/customer-groups", nil, director, "")
	if !strings.Contains(groups.Body.String(), `"per_capita":"200.000"`) || !strings.Contains(groups.Body.String(), `"tag":"feijao-preto"`) {
		t.Fatalf("groups body=%s", groups.Body.String())
	}

	// Plano de hoje e amanhã (Fortaleza), 900 almoços por dia.
	fortaleza := time.FixedZone("-03", -3*3600)
	day1 := time.Now().In(fortaleza).Format("2006-01-02")
	day2 := time.Now().In(fortaleza).AddDate(0, 0, 1).Format("2006-01-02")
	rr = authReq(a, http.MethodPost, "/api/plans", map[string]any{
		"name": "Semana DC", "start_date": day1, "end_date": day2, "target_cmv": "40",
		"meals": []map[string]any{{"group_id": groupID, "day": day1, "meal": "almoco", "people": 900}, {"group_id": groupID, "day": day2, "meal": "almoco", "people": 900}},
	}, director, "")
	if rr.Code != http.StatusCreated {
		t.Fatalf("create plan status=%d body=%s", rr.Code, rr.Body.String())
	}
	planID := jsonID(t, rr.Body.Bytes())
	planPath := "/api/plans/" + itoa(planID)
	plan := plannerGet(t, a, planPath, director)
	if len(plan["positions"].([]any)) != 2 || !strings.Contains(strings.Join(toStrings(plan["blocked"]), " "), "Faltam 2 prato(s)") {
		t.Fatalf("new plan positions=%v blocked=%v", plan["positions"], plan["blocked"])
	}

	// Ficha com item sem custo e sem compra bloqueia o cálculo, sem números parciais.
	pos := func(day string, recipe int64, locked bool) map[string]any {
		return map[string]any{"group_id": groupID, "day": day, "meal": "almoco", "meal_function": "base", "option": 1, "recipe_id": recipe, "locked": locked}
	}
	version := int64(plan["plan"].(map[string]any)["version"].(float64))
	rr = authReq(a, http.MethodPatch, planPath, map[string]any{"name": "Semana DC", "start_date": day1, "end_date": day2, "target_cmv": "40", "version": version,
		"positions": []map[string]any{pos(day1, amarelo, false)}}, director, "")
	if rr.Code != http.StatusOK {
		t.Fatalf("patch plan status=%d body=%s", rr.Code, rr.Body.String())
	}
	version++
	plan = plannerGet(t, a, planPath, director)
	if !strings.Contains(strings.Join(toStrings(plan["blocked"]), " "), "Açafrão") || plan["summary"] != nil {
		t.Fatalf("blocked=%v summary=%v", plan["blocked"], plan["summary"])
	}
	if stale := authReq(a, http.MethodPatch, planPath, map[string]any{"name": "X", "start_date": day1, "end_date": day2, "target_cmv": "40", "version": version - 1}, director, ""); stale.Code != http.StatusConflict {
		t.Fatalf("stale patch status=%d", stale.Code)
	}

	// O solver troca a ficha sem custo e preenche o dia vazio; a sugestão não grava nada.
	rr = authReq(a, http.MethodPost, planPath+"/solve", map[string]any{}, director, "")
	if rr.Code != http.StatusOK {
		t.Fatalf("solve status=%d body=%s", rr.Code, rr.Body.String())
	}
	sol := plannerBody(t, rr.Body.Bytes())
	if len(sol["changes"].([]any)) != 2 || !strings.Contains(rr.Body.String(), `"ignored":["Arroz amarelo"]`) {
		t.Fatalf("solve body=%s", rr.Body.String())
	}
	if p := plannerGet(t, a, planPath, director); p["positions"].([]any)[0].(map[string]any)["recipe_id"].(float64) != float64(amarelo) {
		t.Fatal("solve gravou o plano")
	}
	rr = authReq(a, http.MethodPatch, planPath, map[string]any{"name": "Semana DC", "start_date": day1, "end_date": day2, "target_cmv": "40", "version": version,
		"positions": []map[string]any{pos(day1, branco, true), pos(day2, branco, false)}}, director, "")
	if rr.Code != http.StatusOK {
		t.Fatalf("accept status=%d body=%s", rr.Code, rr.Body.String())
	}
	version++

	// 900 × 200 g = 180 kg prontos por dia; 90 receitas × 0,8 KG × R$ 6 = R$ 432 por dia.
	plan = plannerGet(t, a, planPath, director)
	sum := plan["summary"].(map[string]any)
	if sum["cost"] != "864.00" || sum["revenue"] != "36000.00" || sum["cmv"] != "2.40" {
		t.Fatalf("summary=%v blocked=%v", sum, plan["blocked"])
	}
	item := plan["items"].([]any)[0].(map[string]any)
	if item["quantity"] != "144.000" || item["cost_source"] != "ultima_compra" || item["unit_cost"] != "6.0000" {
		t.Fatalf("item=%v", item)
	}
	lot := plan["lots"].([]any)[0].(map[string]any)
	if lot["produced"] != "180000.000" || lot["unit"] != "g" {
		t.Fatalf("lot=%v", lot)
	}
	// Ficha de outra função não entra na posição.
	other := authReq(a, http.MethodPost, "/api/recipes", map[string]any{"name": "Frango", "portions": "1", "meal_function": "proteína",
		"lines": []map[string]any{{"item_id": arroz, "quantity": "1"}}}, director, "")
	frango := jsonID(t, other.Body.Bytes())
	if bad := authReq(a, http.MethodPatch, planPath, map[string]any{"name": "Semana DC", "start_date": day1, "end_date": day2, "target_cmv": "40", "version": version,
		"positions": []map[string]any{pos(day2, frango, false)}}, director, ""); bad.Code != http.StatusBadRequest || !strings.Contains(bad.Body.String(), "não é da função base") {
		t.Fatalf("wrong function status=%d body=%s", bad.Code, bad.Body.String())
	}

	// Aprovação grava o retrato; plano aprovado não muda.
	if rr := authReq(a, http.MethodPost, planPath+"/approve", map[string]any{"version": version}, director, ""); rr.Code != http.StatusOK {
		t.Fatalf("approve status=%d body=%s", rr.Code, rr.Body.String())
	}
	if rr := authReq(a, http.MethodPatch, planPath, map[string]any{"name": "Semana DC", "start_date": day1, "end_date": day2, "target_cmv": "40", "version": version + 1}, director, ""); rr.Code != http.StatusConflict {
		t.Fatalf("patch approved status=%d", rr.Code)
	}

	// Compra: precisa de 144 KG, saldo zero, acaba no primeiro dia. Reenvio devolve a mesma.
	rr = authReq(a, http.MethodPost, planPath+"/purchase-requests", map[string]any{"operation_id": "planner-buy-1"}, director, "")
	if rr.Code != http.StatusCreated {
		t.Fatalf("purchase status=%d body=%s", rr.Code, rr.Body.String())
	}
	buy := plannerBody(t, rr.Body.Bytes())
	line := buy["lines"].([]any)[0].(map[string]any)
	if line["need"] != "144.0000" || line["balance"] != "0.0000" || line["quantity"] != "144.0000" || line["runout_date"] != day1 {
		t.Fatalf("purchase line=%v", line)
	}
	again := authReq(a, http.MethodPost, planPath+"/purchase-requests", map[string]any{"operation_id": "planner-buy-1"}, director, "")
	if again.Code != http.StatusOK || jsonID(t, again.Body.Bytes()) != int64(buy["id"].(float64)) {
		t.Fatalf("replay status=%d body=%s", again.Code, again.Body.String())
	}
	if n := lookupID(t, a, `SELECT count(*) FROM purchase_requests`); n != 1 {
		t.Fatalf("purchase requests=%d want 1", n)
	}

	// Quantidade real do primeiro dia; versão própria.
	actual := map[string]any{"group_id": groupID, "day": day1, "meal": "almoco", "people": 850, "version": 0}
	if rr := authReq(a, http.MethodPatch, planPath+"/actuals", actual, director, ""); rr.Code != http.StatusOK {
		t.Fatalf("actual status=%d body=%s", rr.Code, rr.Body.String())
	}
	if rr := authReq(a, http.MethodPatch, planPath+"/actuals", actual, director, ""); rr.Code != http.StatusConflict {
		t.Fatalf("stale actual status=%d", rr.Code)
	}

	// Previsto x realizado: a saída de 10 KG (R$ 60) caiu no período; falta o real do segundo dia.
	cmp := plannerGet(t, a, planPath+"/comparison", director)
	if cmp["meals_without_actual"].(float64) != 1 {
		t.Fatalf("comparison meals=%v", cmp["meals"])
	}
	cs := cmp["summary"].(map[string]any)
	if cs["planned_cost"] != "864.00" || cs["actual_revenue"] != nil || cs["actual_cmv"] != nil {
		t.Fatalf("comparison summary=%v", cs)
	}
	found := false
	for _, it := range cmp["items"].([]any) {
		row := it.(map[string]any)
		if row["item"] == "Arroz" {
			found = true
			if row["planned_quantity"] != "144.000" || row["actual_quantity"] != "10.000" || row["actual_cost"] != "60.00" {
				t.Fatalf("comparison arroz=%v", row)
			}
		}
	}
	if !found {
		t.Fatalf("comparison items=%v", cmp["items"])
	}
}

func toStrings(v any) []string {
	out := []string{}
	for _, x := range v.([]any) {
		out = append(out, x.(string))
	}
	return out
}

func TestPlannerPermissionsAndPage(t *testing.T) {
	a, _, _, _ := newStockTestApp(t)
	createTestUser(t, a, "planner-perm-director", "secret", "Diretor", true)
	createTestUser(t, a, "planner-perm-manager", "secret", "Gerente", true)
	createTestUser(t, a, "planner-perm-stock", "secret", "Estoquista", true)
	director := doLogin(t, a, "planner-perm-director", "secret")
	manager := doLogin(t, a, "planner-perm-manager", "secret")
	stock := doLogin(t, a, "planner-perm-stock", "secret")

	for _, path := range []string{"/api/plans", "/api/customer-groups", "/api/planner/options"} {
		if rr := authReq(a, http.MethodGet, path, nil, manager, ""); rr.Code != http.StatusOK {
			t.Fatalf("manager GET %s status=%d", path, rr.Code)
		}
		if rr := authReq(a, http.MethodGet, path, nil, stock, ""); rr.Code != http.StatusForbidden {
			t.Fatalf("stock GET %s status=%d want 403", path, rr.Code)
		}
	}
	if rr := authReq(a, http.MethodPost, "/api/customer-groups", map[string]any{"name": "G"}, manager, ""); rr.Code != http.StatusForbidden {
		t.Fatalf("manager POST group status=%d want 403", rr.Code)
	}
	if rr := authReq(a, http.MethodPost, "/api/customer-groups", map[string]any{"name": "G", "meals": []map[string]any{{"meal": "almoco", "price": "0"}}}, director, ""); rr.Code != http.StatusBadRequest || !strings.Contains(rr.Body.String(), "preço por pessoa") {
		t.Fatalf("invalid group status=%d body=%s", rr.Code, rr.Body.String())
	}

	page := authReq(a, http.MethodGet, "/planner", nil, director, "")
	if page.Code != http.StatusOK || !strings.Contains(page.Body.String(), "Planejamento de cardápio") || !strings.Contains(page.Body.String(), `data-can-write="1"`) || !strings.Contains(page.Body.String(), `href="/planner"`) {
		t.Fatalf("director page status=%d", page.Code)
	}
	if ro := authReq(a, http.MethodGet, "/planner", nil, manager, ""); ro.Code != http.StatusOK || !strings.Contains(ro.Body.String(), `data-can-write="0"`) {
		t.Fatalf("manager page status=%d", ro.Code)
	}
	if denied := authReq(a, http.MethodGet, "/planner", nil, stock, ""); denied.Code != http.StatusSeeOther || denied.Header().Get("Location") != "/no-access" {
		t.Fatalf("stock page status=%d location=%q", denied.Code, denied.Header().Get("Location"))
	}
}

func TestPlannerTemplateFollowsFrontendContract(t *testing.T) {
	html, err := uiFS.ReadFile("templates/planner.html")
	if err != nil {
		t.Fatal(err)
	}
	s := string(html)
	for _, bad := range []string{"style=", "onclick=", "<script>"} {
		if strings.Contains(s, bad) {
			t.Fatalf("planner.html must not contain %q (CSP)", bad)
		}
	}
	if !strings.Contains(s, `{{template "nav" .}}`) || !strings.Contains(s, `{{template "header" .}}`) {
		t.Fatal("planner.html must use shared header and nav")
	}
}
