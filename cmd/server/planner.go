package main

// Planner de cardápio: cadastros de grupos, plano, solver, aprovação, quantidade
// real, solicitação de compra e previsto x realizado. O cálculo fica em
// planner_engine.go; aqui só se carrega e grava (docs/planner-cardapio-contract.md).

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"math"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"
)

var plannerMealOrder = []string{"cafe", "almoco", "lanche", "jantar"}

func isPlannerMeal(m string) bool {
	for _, x := range plannerMealOrder {
		if x == m {
			return true
		}
	}
	return false
}

const plannerMealSort = `array_position(ARRAY['cafe','almoco','lanche','jantar']::text[], meal)`

func (a *app) registerPlannerHandlers(mux *http.ServeMux) {
	mux.HandleFunc("GET /planner", a.uiPage("planner", "Planejamento de cardápio", "planning", "read"))
	mux.HandleFunc("GET /api/planner/options", a.requireAuth("planning", "read", a.handlePlannerOptions))
	mux.HandleFunc("GET /api/customer-groups", a.requireAuth("planning", "read", a.handleCustomerGroups))
	mux.HandleFunc("POST /api/customer-groups", a.requireAuth("planning", "write", a.handleSaveCustomerGroup))
	mux.HandleFunc("PATCH /api/customer-groups/{id}", a.requireAuth("planning", "write", a.handleSaveCustomerGroup))
	mux.HandleFunc("GET /api/plans", a.requireAuth("planning", "read", a.handlePlans))
	mux.HandleFunc("POST /api/plans", a.requireAuth("planning", "write", a.handleSavePlan))
	mux.HandleFunc("GET /api/plans/{id}", a.requireAuth("planning", "read", a.handlePlan))
	mux.HandleFunc("PATCH /api/plans/{id}", a.requireAuth("planning", "write", a.handleSavePlan))
	mux.HandleFunc("POST /api/plans/{id}/solve", a.requireAuth("planning", "write", a.handleSolvePlan))
	mux.HandleFunc("POST /api/plans/{id}/approve", a.requireAuth("planning", "write", a.handleApprovePlan))
	mux.HandleFunc("PATCH /api/plans/{id}/actuals", a.requireAuth("planning", "write", a.handleSavePlanActual))
	mux.HandleFunc("GET /api/plans/{id}/purchase-requests", a.requireAuth("planning", "read", a.handlePlanPurchaseRequests))
	mux.HandleFunc("POST /api/plans/{id}/purchase-requests", a.requireAuth("planning", "write", a.handleCreatePurchaseRequest))
	mux.HandleFunc("GET /api/plans/{id}/comparison", a.requireAuth("planning", "read", a.handlePlanComparison))
}

func num(v float64, dec int) string {
	p := math.Pow(10, float64(dec))
	r := math.Round(v*p) / p
	if r == 0 {
		r = 0 // evita "-0.00"
	}
	return strconv.FormatFloat(r, 'f', dec, 64)
}

func numPtr(v *float64, dec int) any {
	if v == nil {
		return nil
	}
	return num(*v, dec)
}

func plannerFail(w http.ResponseWriter, status int, msg, logMsg string, err error) {
	if err != nil {
		log.Printf("%s error=%v", logMsg, err)
	}
	writeJSONError(w, status, msg)
}

// ---------- Grupos de clientes ----------

type groupMenuRequest struct {
	Function  string `json:"meal_function"`
	Options   int    `json:"options"`
	PerCapita string `json:"per_capita"`
	Unit      string `json:"per_capita_unit"`
	Adhesion  string `json:"adhesion"`
}

type groupMealRequest struct {
	Meal  string             `json:"meal"`
	Price string             `json:"price"`
	Menu  []groupMenuRequest `json:"menu"`
}

type groupRuleRequest struct {
	Kind     string `json:"kind"`
	WhenTag  string `json:"when_tag"`
	Function string `json:"meal_function"`
	Tag      string `json:"tag"`
	Days     int    `json:"days"`
}

type customerGroupRequest struct {
	Name    string             `json:"name"`
	Active  *bool              `json:"active"`
	Version int64              `json:"version,omitempty"`
	Meals   []groupMealRequest `json:"meals"`
	Rules   []groupRuleRequest `json:"rules"`
}

func percentInRange(v string, allowZero bool) bool {
	if !pricePattern.MatchString(v) {
		return false
	}
	f, err := strconv.ParseFloat(v, 64)
	return err == nil && f <= 100 && (f > 0 || (allowZero && f == 0))
}

func normalizeGroupRequest(req *customerGroupRequest) string {
	req.Name = strings.TrimSpace(req.Name)
	if req.Name == "" {
		return "Digite o nome do grupo."
	}
	if len(req.Meals) == 0 {
		return "Escolha ao menos uma refeição contratada."
	}
	seenMeal := map[string]bool{}
	for i := range req.Meals {
		m := &req.Meals[i]
		m.Meal = normalizeLabel(m.Meal)
		m.Price = normalizeDecimalInput(m.Price)
		if !isPlannerMeal(m.Meal) {
			return "Escolha a refeição: café, almoço, lanche ou jantar."
		}
		if seenMeal[m.Meal] {
			return "Uma refeição não pode aparecer duas vezes no grupo."
		}
		seenMeal[m.Meal] = true
		if !pricePattern.MatchString(m.Price) || !positiveDecimal(m.Price) {
			return "Digite o preço por pessoa da refeição, maior que zero, com até 2 casas decimais."
		}
		seenFn := map[string]bool{}
		for j := range m.Menu {
			f := &m.Menu[j]
			f.Function = normalizeLabel(f.Function)
			f.PerCapita = normalizeDecimalInput(f.PerCapita)
			f.Unit = normalizeLabel(f.Unit)
			f.Adhesion = normalizeDecimalInput(f.Adhesion)
			if f.Adhesion == "" {
				f.Adhesion = "100"
			}
			if f.Options == 0 {
				f.Options = 1
			}
			switch {
			case f.Function == "" || len(f.Function) > 60:
				return "Digite a função de cada linha do cardápio (ex.: proteína), com até 60 caracteres."
			case seenFn[f.Function]:
				return "Uma função não pode aparecer duas vezes na mesma refeição."
			case f.Options < 1 || f.Options > 9:
				return "Escolha de 1 a 9 opções por função."
			case !portionsPattern.MatchString(f.PerCapita) || !positiveDecimal(f.PerCapita):
				return "Digite o per capita do contrato, maior que zero, com até 3 casas decimais."
			case f.Unit != "g" && f.Unit != "ml" && f.Unit != "un":
				return "Escolha a unidade do per capita: g, ml ou un."
			case !percentInRange(f.Adhesion, false):
				return "Digite a adesão entre 0,01 e 100%."
			}
			seenFn[f.Function] = true
		}
	}
	for i := range req.Rules {
		r := &req.Rules[i]
		r.Kind = normalizeLabel(r.Kind)
		r.Function = normalizeLabel(r.Function)
		r.WhenTag = strings.Join(strings.Fields(normalizeLabel(r.WhenTag)), "-")
		r.Tag = strings.Join(strings.Fields(normalizeLabel(r.Tag)), "-")
		switch {
		case r.Kind != "exists_with" && r.Kind != "exists_without" && r.Kind != "none_with" && r.Kind != "no_repeat":
			return "Escolha o tipo de cada regra."
		case r.Function == "":
			return "Escolha a função de cada regra."
		case r.Kind != "no_repeat" && r.Tag == "":
			return "Digite a etiqueta de cada regra."
		case r.Kind == "no_repeat" && (r.Days < 1 || r.Days > 30):
			return "Digite de 1 a 30 dias na regra de não repetir."
		case (r.Tag != "" && !tagPattern.MatchString(r.Tag)) || (r.WhenTag != "" && !tagPattern.MatchString(r.WhenTag)):
			return "Use etiquetas com letras, números e hífen, até 40 caracteres."
		}
		if r.Kind != "no_repeat" {
			r.Days = 0
		}
	}
	return ""
}

func (a *app) handleCustomerGroups(w http.ResponseWriter, r *http.Request) {
	out, err := loadCustomerGroups(r.Context(), a.db)
	if err != nil {
		plannerFail(w, http.StatusInternalServerError, "Não foi possível carregar os grupos de clientes.", "customer groups failed", err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func loadCustomerGroups(ctx context.Context, q inventoryQueryer) ([]map[string]any, error) {
	out := []map[string]any{}
	byID := map[int64]map[string]any{}
	rows, err := q.QueryContext(ctx, `SELECT id,name,active,version FROM customer_groups ORDER BY active DESC,lower(name),id`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var id, version int64
		var name string
		var active bool
		if err := rows.Scan(&id, &name, &active, &version); err != nil {
			rows.Close()
			return nil, err
		}
		g := map[string]any{"id": id, "name": name, "active": active, "version": version, "meals": []map[string]any{}, "rules": []map[string]any{}}
		byID[id] = g
		out = append(out, g)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	meals := map[[2]any]map[string]any{}
	rows, err = q.QueryContext(ctx, `SELECT group_id,meal,price::text FROM customer_group_meals ORDER BY group_id,`+plannerMealSort)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var gid int64
		var meal, price string
		if err := rows.Scan(&gid, &meal, &price); err != nil {
			rows.Close()
			return nil, err
		}
		m := map[string]any{"meal": meal, "price": price, "menu": []map[string]any{}}
		meals[[2]any{gid, meal}] = m
		byID[gid]["meals"] = append(byID[gid]["meals"].([]map[string]any), m)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	rows, err = q.QueryContext(ctx, `SELECT group_id,meal,meal_function,options,per_capita::text,per_capita_unit,adhesion::text FROM customer_group_menu ORDER BY group_id,meal,sort_order,meal_function`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var gid int64
		var options int
		var meal, fn, perCapita, unit, adhesion string
		if err := rows.Scan(&gid, &meal, &fn, &options, &perCapita, &unit, &adhesion); err != nil {
			rows.Close()
			return nil, err
		}
		m := meals[[2]any{gid, meal}]
		m["menu"] = append(m["menu"].([]map[string]any), map[string]any{"meal_function": fn, "options": options, "per_capita": perCapita, "per_capita_unit": unit, "adhesion": adhesion})
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	rows, err = q.QueryContext(ctx, `SELECT group_id,kind,COALESCE(when_tag,''),meal_function,COALESCE(tag,''),COALESCE(days,0) FROM customer_group_rules ORDER BY group_id,id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var gid int64
		var days int
		var kind, when, fn, tag string
		if err := rows.Scan(&gid, &kind, &when, &fn, &tag, &days); err != nil {
			return nil, err
		}
		byID[gid]["rules"] = append(byID[gid]["rules"].([]map[string]any), map[string]any{"kind": kind, "when_tag": when, "meal_function": fn, "tag": tag, "days": days})
	}
	return out, rows.Err()
}

func (a *app) handleSaveCustomerGroup(w http.ResponseWriter, r *http.Request) {
	var id int64
	if r.Method == http.MethodPatch {
		var ok bool
		if id, ok = pathID(w, r); !ok {
			return
		}
	}
	var req customerGroupRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	if msg := normalizeGroupRequest(&req); msg != "" {
		writeJSONError(w, http.StatusBadRequest, msg)
		return
	}
	if id > 0 && req.Version <= 0 {
		writeJSONError(w, http.StatusBadRequest, "Atualize a página e tente de novo.")
		return
	}
	active := req.Active == nil || *req.Active
	ctx := r.Context()
	const failMsg = "Não foi possível salvar o grupo."
	tx, err := a.db.BeginTx(ctx, nil)
	if err != nil {
		plannerFail(w, http.StatusInternalServerError, failMsg, "group save begin failed", err)
		return
	}
	defer func() { _ = tx.Rollback() }()
	version := int64(1)
	if id == 0 {
		err = tx.QueryRowContext(ctx, `INSERT INTO customer_groups (name,active) VALUES ($1,$2) RETURNING id`, req.Name, active).Scan(&id)
	} else {
		err = tx.QueryRowContext(ctx, `UPDATE customer_groups SET name=$1,active=$2,version=version+1 WHERE id=$3 AND version=$4 RETURNING version`, req.Name, active, id, req.Version).Scan(&version)
		if errors.Is(err, sql.ErrNoRows) {
			var exists bool
			if qerr := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM customer_groups WHERE id=$1)`, id).Scan(&exists); qerr != nil {
				plannerFail(w, http.StatusInternalServerError, failMsg, "group exists failed", qerr)
				return
			}
			if !exists {
				writeJSONError(w, http.StatusNotFound, "Grupo não encontrado.")
				return
			}
			writeConcurrentEditError(w)
			return
		}
	}
	if err != nil {
		if strings.Contains(err.Error(), "idx_customer_groups_normalized_name_unique") {
			writeJSONError(w, http.StatusConflict, "Já existe um grupo com este nome.")
			return
		}
		plannerFail(w, http.StatusBadRequest, failMsg, "group save failed", err)
		return
	}
	for _, stmt := range []string{`DELETE FROM customer_group_meals WHERE group_id=$1`, `DELETE FROM customer_group_rules WHERE group_id=$1`} {
		if _, err := tx.ExecContext(ctx, stmt, id); err != nil {
			plannerFail(w, http.StatusInternalServerError, failMsg, "group clear failed", err)
			return
		}
	}
	for _, m := range req.Meals {
		if _, err := tx.ExecContext(ctx, `INSERT INTO customer_group_meals (group_id,meal,price) VALUES ($1,$2,$3)`, id, m.Meal, m.Price); err != nil {
			plannerFail(w, http.StatusInternalServerError, failMsg, "group meal failed", err)
			return
		}
		for n, f := range m.Menu {
			if _, err := tx.ExecContext(ctx, `INSERT INTO customer_group_menu (group_id,meal,meal_function,sort_order,options,per_capita,per_capita_unit,adhesion) VALUES ($1,$2,$3,$4,$5,$6,$7,$8)`,
				id, m.Meal, f.Function, n, f.Options, f.PerCapita, f.Unit, f.Adhesion); err != nil {
				plannerFail(w, http.StatusInternalServerError, failMsg, "group menu failed", err)
				return
			}
		}
	}
	for _, rule := range req.Rules {
		var days any
		if rule.Kind == "no_repeat" {
			days = rule.Days
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO customer_group_rules (group_id,kind,when_tag,meal_function,tag,days) VALUES ($1,$2,NULLIF($3,''),$4,NULLIF($5,''),$6)`,
			id, rule.Kind, rule.WhenTag, rule.Function, rule.Tag, days); err != nil {
			plannerFail(w, http.StatusInternalServerError, failMsg, "group rule failed", err)
			return
		}
	}
	if err := tx.Commit(); err != nil {
		plannerFail(w, http.StatusInternalServerError, failMsg, "group commit failed", err)
		return
	}
	status := http.StatusOK
	if r.Method == http.MethodPost {
		status = http.StatusCreated
	}
	writeJSON(w, status, map[string]any{"id": id, "version": version})
}

// ---------- Opções da tela ----------

func (a *app) handlePlannerOptions(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	groups, err := loadCustomerGroups(ctx, a.db)
	if err != nil {
		plannerFail(w, http.StatusInternalServerError, "Não foi possível carregar as opções do planner.", "planner options groups failed", err)
		return
	}
	rows, err := a.db.QueryContext(ctx, `SELECT id,name,active,COALESCE(meal_function,''),COALESCE(portion_size::text,''),COALESCE(portion_unit,''),array_to_string(tags,',') FROM recipes ORDER BY lower(name),id`)
	if err != nil {
		plannerFail(w, http.StatusInternalServerError, "Não foi possível carregar as opções do planner.", "planner options recipes failed", err)
		return
	}
	defer rows.Close()
	recipes := []map[string]any{}
	functions, tags := map[string]bool{}, map[string]bool{}
	for rows.Next() {
		var id int64
		var name string
		var active bool
		var f recipePlanningFields
		if err := rows.Scan(&id, &name, &active, &f.Function, &f.Size, &f.Unit, &f.Tags); err != nil {
			plannerFail(w, http.StatusInternalServerError, "Não foi possível carregar as opções do planner.", "planner options scan failed", err)
			return
		}
		row := map[string]any{"id": id, "name": name, "active": active}
		f.addTo(row)
		recipes = append(recipes, row)
		if f.Function != "" {
			functions[f.Function] = true
		}
		for _, t := range row["tags"].([]string) {
			tags[t] = true
		}
	}
	if err := rows.Err(); err != nil {
		plannerFail(w, http.StatusInternalServerError, "Não foi possível carregar as opções do planner.", "planner options rows failed", err)
		return
	}
	for _, g := range groups {
		for _, m := range g["meals"].([]map[string]any) {
			for _, f := range m["menu"].([]map[string]any) {
				functions[f["meal_function"].(string)] = true
			}
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"groups": groups, "recipes": recipes, "functions": sortedKeys(functions), "tags": sortedKeys(tags)})
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// ---------- Carga do plano para o motor ----------

type planHeader struct {
	ID                               int64
	Name, Start, End, Status         string
	Target, DailyTol, ScaleTol       float64
	TargetText, DailyText, ScaleText string
	Version                          int64
	ApprovedAt                       sql.NullTime
}

type planMealExtra struct {
	Actual        sql.NullInt64
	ActualVersion int64
}

type plannerItemInfo struct {
	Name, Unit string
	Balance    float64
}

type loadedPlan struct {
	header planHeader
	in     *planInput
	extra  map[planMealKey]planMealExtra
	items  map[int64]plannerItemInfo
}

var errPlanNotFound = errors.New("plan not found")

func periodDays(start, end string) ([]string, error) {
	s, err := time.Parse("2006-01-02", start)
	if err != nil {
		return nil, err
	}
	e, err := time.Parse("2006-01-02", end)
	if err != nil {
		return nil, err
	}
	out := []string{}
	for d := s; !d.After(e); d = d.AddDate(0, 0, 1) {
		out = append(out, d.Format("2006-01-02"))
	}
	return out, nil
}

// plannerItemCostSQL usa o preço médio canônico (recipeCostCTE) e, sem ele, o
// último preço de compra (última entrada com preço). Devolve também o saldo.
const plannerItemCostSQL = recipeCostCTE + `, last_purchase AS (
    SELECT DISTINCT ON (ml.item_id) ml.item_id, ml.unit_cost
    FROM movement_lines ml
    JOIN movements m ON m.id=ml.movement_id
    WHERE m.movement_type='ENTRADA' AND ml.direction='IN' AND ml.unit_cost > 0
      AND ml.item_id IN (SELECT item_id FROM recipe_lines)
    ORDER BY ml.item_id, m.occurred_at DESC, ml.id DESC
)
SELECT i.id,i.name,i.unit_code,
       CASE WHEN c.qty > 0 AND c.value > 0 THEN (c.value/c.qty)::float8 END,
       lp.unit_cost::float8,
       COALESCE(c.qty,0)::float8
FROM inventory_items i
LEFT JOIN item_cost c ON c.item_id=i.id
LEFT JOIN last_purchase lp ON lp.item_id=i.id
WHERE i.id IN (SELECT item_id FROM recipe_lines)`

func loadPlan(ctx context.Context, q inventoryQueryer, id int64) (*loadedPlan, error) {
	lp := &loadedPlan{extra: map[planMealKey]planMealExtra{}, items: map[int64]plannerItemInfo{}}
	h := &lp.header
	err := q.QueryRowContext(ctx, `SELECT id,name,start_date::text,end_date::text,status,target_cmv::float8,daily_tolerance::float8,scale_tolerance::float8,
target_cmv::text,daily_tolerance::text,scale_tolerance::text,version,approved_at FROM menu_plans WHERE id=$1`, id).
		Scan(&h.ID, &h.Name, &h.Start, &h.End, &h.Status, &h.Target, &h.DailyTol, &h.ScaleTol, &h.TargetText, &h.DailyText, &h.ScaleText, &h.Version, &h.ApprovedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, errPlanNotFound
	}
	if err != nil {
		return nil, err
	}
	days, err := periodDays(h.Start, h.End)
	if err != nil {
		return nil, err
	}
	in := &planInput{Target: h.Target, DailyTol: h.DailyTol, ScaleTol: h.ScaleTol, Days: days,
		Recipes: map[int64]*planRecipe{}, Costs: map[int64]planItemCost{}, ItemNames: map[int64]string{},
		GroupNames: map[int64]string{}, Menu: map[planMenuKey]planMenu{}}
	lp.in = in

	rows, err := q.QueryContext(ctx, `SELECT pm.group_id,g.name,pm.day::text,pm.meal,pm.people,COALESCE(gm.price::float8,0),gm.price IS NOT NULL,pm.actual_people,pm.actual_version
FROM menu_plan_meals pm
JOIN customer_groups g ON g.id=pm.group_id
LEFT JOIN customer_group_meals gm ON gm.group_id=pm.group_id AND gm.meal=pm.meal
WHERE pm.plan_id=$1
ORDER BY pm.day,pm.group_id,array_position(ARRAY['cafe','almoco','lanche','jantar']::text[], pm.meal)`, id)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var m planMeal
		var name string
		var people int64
		var ex planMealExtra
		if err := rows.Scan(&m.Group, &name, &m.Day, &m.Meal, &people, &m.Price, &m.HasPrice, &ex.Actual, &ex.ActualVersion); err != nil {
			rows.Close()
			return nil, err
		}
		m.People = float64(people)
		in.GroupNames[m.Group] = name
		in.Meals = append(in.Meals, m)
		lp.extra[m.planMealKey] = ex
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}

	rows, err = q.QueryContext(ctx, `SELECT group_id,day::text,meal,meal_function,option_no,COALESCE(recipe_id,0),adhesion::float8,locked
FROM menu_plan_positions WHERE plan_id=$1
ORDER BY day,group_id,array_position(ARRAY['cafe','almoco','lanche','jantar']::text[], meal),meal_function,option_no`, id)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var p planPosition
		if err := rows.Scan(&p.Group, &p.Day, &p.Meal, &p.Function, &p.Option, &p.Recipe, &p.Adhesion, &p.Locked); err != nil {
			rows.Close()
			return nil, err
		}
		in.Positions = append(in.Positions, p)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}

	rows, err = q.QueryContext(ctx, `SELECT group_id,meal,meal_function,per_capita::float8,per_capita_unit FROM customer_group_menu
WHERE group_id IN (SELECT group_id FROM menu_plan_meals WHERE plan_id=$1)`, id)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var k planMenuKey
		var m planMenu
		if err := rows.Scan(&k.Group, &k.Meal, &k.Function, &m.PerCapita, &m.Unit); err != nil {
			rows.Close()
			return nil, err
		}
		in.Menu[k] = m
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}

	rows, err = q.QueryContext(ctx, `SELECT group_id,kind,COALESCE(when_tag,''),meal_function,COALESCE(tag,''),COALESCE(days,0) FROM customer_group_rules
WHERE group_id IN (SELECT group_id FROM menu_plan_meals WHERE plan_id=$1) ORDER BY id`, id)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var rule planRule
		if err := rows.Scan(&rule.Group, &rule.Kind, &rule.WhenTag, &rule.Function, &rule.Tag, &rule.Days); err != nil {
			rows.Close()
			return nil, err
		}
		in.Rules = append(in.Rules, rule)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}

	rows, err = q.QueryContext(ctx, `SELECT id,name,active,portions::float8,COALESCE(meal_function,''),COALESCE(portion_size::float8,0),COALESCE(portion_unit,''),array_to_string(tags,',') FROM recipes`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var rec planRecipe
		var tags string
		if err := rows.Scan(&rec.ID, &rec.Name, &rec.Active, &rec.Yield, &rec.Function, &rec.PortionSize, &rec.PortionUnit, &tags); err != nil {
			rows.Close()
			return nil, err
		}
		rec.Tags = map[string]bool{}
		for _, t := range strings.Split(tags, ",") {
			if t != "" {
				rec.Tags[t] = true
			}
		}
		r := rec
		in.Recipes[rec.ID] = &r
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	rows, err = q.QueryContext(ctx, `SELECT recipe_id,item_id,(quantity*correction_factor)::float8 FROM recipe_lines ORDER BY recipe_id,item_id`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var rid int64
		var l planRecipeLine
		if err := rows.Scan(&rid, &l.ItemID, &l.Gross); err != nil {
			rows.Close()
			return nil, err
		}
		if rec := in.Recipes[rid]; rec != nil {
			rec.Lines = append(rec.Lines, l)
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}

	rows, err = q.QueryContext(ctx, plannerItemCostSQL, int64(0))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		var info plannerItemInfo
		var avg, last sql.NullFloat64
		if err := rows.Scan(&id, &info.Name, &info.Unit, &avg, &last, &info.Balance); err != nil {
			return nil, err
		}
		lp.items[id] = info
		in.ItemNames[id] = info.Name
		switch {
		case avg.Valid:
			in.Costs[id] = planItemCost{Cost: avg.Float64, Source: "estoque"}
		case last.Valid:
			in.Costs[id] = planItemCost{Cost: last.Float64, Source: "ultima_compra"}
		}
	}
	return lp, rows.Err()
}

func (a *app) canSeeCosts(r *http.Request) bool {
	ok, err := a.hasPermission(r.Context(), userFromContext(r.Context()).ID, "costs", "read")
	return err == nil && ok
}

// planJSON monta a resposta do plano. Sem a permissão de custos, valores em
// dinheiro e CMV não são enviados; quantidades sim.
func planJSON(lp *loadedPlan, ev planEvaluation, costs bool) map[string]any {
	in, h := lp.in, lp.header
	recipeName := func(id int64) any {
		if r := in.Recipes[id]; r != nil {
			return r.Name
		}
		return nil
	}
	groups := []map[string]any{}
	seen := map[int64]bool{}
	for _, m := range in.Meals {
		if !seen[m.Group] {
			seen[m.Group] = true
			groups = append(groups, map[string]any{"id": m.Group, "name": in.GroupNames[m.Group]})
		}
	}
	meals := []map[string]any{}
	for _, m := range in.Meals {
		ex := lp.extra[m.planMealKey]
		row := map[string]any{"group_id": m.Group, "day": m.Day, "meal": m.Meal, "people": int64(m.People), "actual_people": nil, "actual_version": ex.ActualVersion}
		if ex.Actual.Valid {
			row["actual_people"] = ex.Actual.Int64
		}
		if costs {
			row["price"] = num(m.Price, 2)
		}
		meals = append(meals, row)
	}
	positions := []map[string]any{}
	for i, p := range in.Positions {
		row := map[string]any{"group_id": p.Group, "day": p.Day, "meal": p.Meal, "meal_function": p.Function, "option": p.Option,
			"recipe_id": p.Recipe, "recipe": recipeName(p.Recipe), "adhesion": num(p.Adhesion, 2), "locked": p.Locked}
		if len(ev.Positions) == len(in.Positions) {
			pr := ev.Positions[i]
			row["portions"] = num(pr.Portions, 3)
			row["produced"] = num(pr.Produced, 3)
			row["unit"] = pr.Unit
			if costs {
				row["cost"] = num(pr.Cost, 2)
			}
		}
		positions = append(positions, row)
	}
	violations := []map[string]any{}
	for _, v := range ev.Violations {
		violations = append(violations, map[string]any{"group_id": v.Group, "day": v.Day, "meal": v.Meal, "meal_function": v.Function, "message": v.Message})
	}
	out := map[string]any{
		"plan": map[string]any{"id": h.ID, "name": h.Name, "start_date": h.Start, "end_date": h.End, "status": h.Status, "version": h.Version,
			"target_cmv": h.TargetText, "daily_tolerance": h.DailyText, "scale_tolerance": h.ScaleText},
		"days": in.Days, "groups": groups, "meals": meals, "positions": positions,
		"blocked": ev.Blocked, "violations": violations, "show_costs": costs,
	}
	if ev.Blocked == nil {
		out["blocked"] = []string{}
	}
	if len(ev.Blocked) > 0 {
		return out
	}
	lots := []map[string]any{}
	for _, l := range ev.Lots {
		by := []map[string]any{}
		for _, g := range l.ByGroup {
			by = append(by, map[string]any{"group_id": g.Group, "group": in.GroupNames[g.Group], "produced": num(g.Produced, 3)})
		}
		lots = append(lots, map[string]any{"day": l.Day, "recipe_id": l.Recipe, "recipe": recipeName(l.Recipe), "unit": l.Unit, "produced": num(l.Produced, 3), "by_group": by})
	}
	out["lots"] = lots
	items := []map[string]any{}
	for _, it := range ev.Items {
		info := lp.items[it.ItemID]
		row := map[string]any{"item_id": it.ItemID, "item": info.Name, "unit": info.Unit, "quantity": num(it.Total, 3), "balance": num(info.Balance, 3)}
		if costs {
			row["unit_cost"] = num(it.Unit, 4)
			row["cost"] = num(it.Cost, 2)
			row["cost_source"] = it.Source
		}
		items = append(items, row)
	}
	out["items"] = items
	if costs {
		gr := []map[string]any{}
		for _, g := range ev.Groups {
			days := []map[string]any{}
			for _, d := range g.Days {
				days = append(days, map[string]any{"day": d.Day, "revenue": num(d.Revenue, 2), "cost": num(d.Cost, 2), "cmv": numPtr(d.CMV, 2)})
			}
			gr = append(gr, map[string]any{"group_id": g.Group, "group": in.GroupNames[g.Group], "revenue": num(g.Revenue, 2), "cost": num(g.Cost, 2), "cmv": numPtr(g.CMV, 2), "days": days})
		}
		mr := []map[string]any{}
		for _, m := range ev.Meals {
			mr = append(mr, map[string]any{"group_id": m.Group, "day": m.Day, "meal": m.Meal, "revenue": num(m.Revenue, 2), "cost": num(m.Cost, 2), "cmv": numPtr(m.CMV, 2)})
		}
		out["group_results"] = gr
		out["meal_results"] = mr
		out["summary"] = map[string]any{"revenue": num(ev.Revenue, 2), "cost": num(ev.Cost, 2), "cmv": numPtr(ev.CMV, 2), "margin": num(ev.Revenue-ev.Cost, 2)}
	}
	return out
}

func (a *app) loadPlanOrFail(w http.ResponseWriter, r *http.Request, q inventoryQueryer, id int64) *loadedPlan {
	lp, err := loadPlan(r.Context(), q, id)
	if errors.Is(err, errPlanNotFound) {
		writeJSONError(w, http.StatusNotFound, "Plano não encontrado.")
		return nil
	}
	if err != nil {
		plannerFail(w, http.StatusInternalServerError, "Não foi possível carregar o plano.", fmt.Sprintf("plan load failed id=%d", id), err)
		return nil
	}
	return lp
}

// ---------- Planos ----------

func (a *app) handlePlans(w http.ResponseWriter, r *http.Request) {
	rows, err := a.db.QueryContext(r.Context(), `SELECT id,name,start_date::text,end_date::text,status,target_cmv::text,version FROM menu_plans ORDER BY start_date DESC,id DESC LIMIT 200`)
	if err != nil {
		plannerFail(w, http.StatusInternalServerError, "Não foi possível carregar os planos.", "plans list failed", err)
		return
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var id, version int64
		var name, start, end, status, target string
		if err := rows.Scan(&id, &name, &start, &end, &status, &target, &version); err != nil {
			plannerFail(w, http.StatusInternalServerError, "Não foi possível carregar os planos.", "plans scan failed", err)
			return
		}
		out = append(out, map[string]any{"id": id, "name": name, "start_date": start, "end_date": end, "status": status, "target_cmv": target, "version": version})
	}
	if err := rows.Err(); err != nil {
		plannerFail(w, http.StatusInternalServerError, "Não foi possível carregar os planos.", "plans rows failed", err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (a *app) handlePlan(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	lp := a.loadPlanOrFail(w, r, a.db, id)
	if lp == nil {
		return
	}
	writeJSON(w, http.StatusOK, planJSON(lp, evaluatePlan(lp.in), a.canSeeCosts(r)))
}

type planMealRequest struct {
	GroupID int64  `json:"group_id"`
	Day     string `json:"day"`
	Meal    string `json:"meal"`
	People  int    `json:"people"`
}

type planPositionRequest struct {
	GroupID  int64  `json:"group_id"`
	Day      string `json:"day"`
	Meal     string `json:"meal"`
	Function string `json:"meal_function"`
	Option   int    `json:"option"`
	RecipeID int64  `json:"recipe_id"`
	Locked   bool   `json:"locked"`
	Adhesion string `json:"adhesion"`
}

type planRequest struct {
	Name           string                `json:"name"`
	StartDate      string                `json:"start_date"`
	EndDate        string                `json:"end_date"`
	TargetCMV      string                `json:"target_cmv"`
	DailyTolerance string                `json:"daily_tolerance"`
	ScaleTolerance string                `json:"scale_tolerance"`
	Meals          []planMealRequest     `json:"meals"`
	Positions      []planPositionRequest `json:"positions"`
	Version        int64                 `json:"version,omitempty"`
}

func planRequestProblem(req *planRequest, creating bool) string {
	req.Name = strings.TrimSpace(req.Name)
	req.TargetCMV = normalizeDecimalInput(req.TargetCMV)
	req.DailyTolerance = normalizeDecimalInput(req.DailyTolerance)
	req.ScaleTolerance = normalizeDecimalInput(req.ScaleTolerance)
	if req.DailyTolerance == "" {
		req.DailyTolerance = "5"
	}
	if req.ScaleTolerance == "" {
		req.ScaleTolerance = "3"
	}
	if req.Name == "" {
		return "Digite o nome do plano."
	}
	days, err := periodDays(req.StartDate, req.EndDate)
	if err != nil || len(days) == 0 {
		return "Escolha a data inicial e a final do período, com a final igual ou depois da inicial."
	}
	if len(days) > 63 {
		return "Escolha um período de até 63 dias."
	}
	if !percentInRange(req.TargetCMV, false) {
		return "Digite o CMV alvo entre 0,01 e 100%, com até 2 casas decimais."
	}
	if !percentInRange(req.DailyTolerance, true) || !percentInRange(req.ScaleTolerance, true) {
		return "Digite as tolerâncias entre 0 e 100%, com até 2 casas decimais."
	}
	if creating && len(req.Meals) == 0 {
		return "Informe ao menos uma refeição com a quantidade prevista."
	}
	inPeriod := map[string]bool{}
	for _, d := range days {
		inPeriod[d] = true
	}
	seen := map[planMealKey]bool{}
	for i := range req.Meals {
		m := &req.Meals[i]
		m.Meal = normalizeLabel(m.Meal)
		k := planMealKey{m.GroupID, m.Day, m.Meal}
		switch {
		case m.GroupID <= 0:
			return "Escolha o grupo de cada refeição."
		case !inPeriod[m.Day]:
			return "Cada refeição precisa ter um dia dentro do período do plano."
		case !isPlannerMeal(m.Meal):
			return "Escolha a refeição: café, almoço, lanche ou jantar."
		case m.People < 0 || m.People > 1000000:
			return "Digite a quantidade prevista de refeições entre 0 e 1.000.000."
		case seen[k]:
			return "A mesma refeição do mesmo grupo e dia aparece duas vezes."
		}
		seen[k] = true
	}
	for i := range req.Positions {
		p := &req.Positions[i]
		p.Meal = normalizeLabel(p.Meal)
		p.Function = normalizeLabel(p.Function)
		p.Adhesion = normalizeDecimalInput(p.Adhesion)
		if p.Locked && p.RecipeID <= 0 {
			return "Escolha a ficha antes de travar a posição."
		}
		if p.Adhesion != "" && !percentInRange(p.Adhesion, false) {
			return "Digite a adesão entre 0,01 e 100%."
		}
	}
	return ""
}

// syncPlanPositions cria as posições que o modelo de refeição do grupo pede e
// remove as que o modelo não tem mais. Posições existentes mantêm a escolha.
func syncPlanPositions(ctx context.Context, tx *sql.Tx, planID int64) error {
	if _, err := tx.ExecContext(ctx, `DELETE FROM menu_plan_positions p WHERE p.plan_id=$1 AND NOT EXISTS (
    SELECT 1 FROM customer_group_menu gm
    WHERE gm.group_id=p.group_id AND gm.meal=p.meal AND gm.meal_function=p.meal_function AND p.option_no<=gm.options)`, planID); err != nil {
		return err
	}
	_, err := tx.ExecContext(ctx, `INSERT INTO menu_plan_positions (plan_id,group_id,day,meal,meal_function,option_no,adhesion)
SELECT pm.plan_id,pm.group_id,pm.day,pm.meal,gm.meal_function,o.n,gm.adhesion
FROM menu_plan_meals pm
JOIN customer_group_menu gm ON gm.group_id=pm.group_id AND gm.meal=pm.meal
CROSS JOIN LATERAL generate_series(1, gm.options) AS o(n)
WHERE pm.plan_id=$1
ON CONFLICT DO NOTHING`, planID)
	return err
}

func (a *app) handleSavePlan(w http.ResponseWriter, r *http.Request) {
	creating := r.Method == http.MethodPost
	var id int64
	if !creating {
		var ok bool
		if id, ok = pathID(w, r); !ok {
			return
		}
	}
	var req planRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	if msg := planRequestProblem(&req, creating); msg != "" {
		writeJSONError(w, http.StatusBadRequest, msg)
		return
	}
	if !creating && req.Version <= 0 {
		writeJSONError(w, http.StatusBadRequest, "Atualize a página e tente de novo.")
		return
	}
	ctx := r.Context()
	const failMsg = "Não foi possível salvar o plano."
	tx, err := a.db.BeginTx(ctx, nil)
	if err != nil {
		plannerFail(w, http.StatusInternalServerError, failMsg, "plan save begin failed", err)
		return
	}
	defer func() { _ = tx.Rollback() }()
	version := int64(1)
	if creating {
		err = tx.QueryRowContext(ctx, `INSERT INTO menu_plans (name,start_date,end_date,target_cmv,daily_tolerance,scale_tolerance,created_by)
VALUES ($1,$2::date,$3::date,$4,$5,$6,$7) RETURNING id`, req.Name, req.StartDate, req.EndDate, req.TargetCMV, req.DailyTolerance, req.ScaleTolerance, userFromContext(ctx).ID).Scan(&id)
		if err != nil {
			plannerFail(w, http.StatusBadRequest, failMsg, "plan create failed", err)
			return
		}
	} else {
		var status string
		err = tx.QueryRowContext(ctx, `SELECT status FROM menu_plans WHERE id=$1 FOR UPDATE`, id).Scan(&status)
		if errors.Is(err, sql.ErrNoRows) {
			writeJSONError(w, http.StatusNotFound, "Plano não encontrado.")
			return
		}
		if err != nil {
			plannerFail(w, http.StatusInternalServerError, failMsg, "plan lock failed", err)
			return
		}
		if status != "draft" {
			writeJSONError(w, http.StatusConflict, "Plano aprovado não muda. Para alterar, crie um novo plano.")
			return
		}
		err = tx.QueryRowContext(ctx, `UPDATE menu_plans SET name=$1,start_date=$2::date,end_date=$3::date,target_cmv=$4,daily_tolerance=$5,scale_tolerance=$6,version=version+1
WHERE id=$7 AND version=$8 RETURNING version`, req.Name, req.StartDate, req.EndDate, req.TargetCMV, req.DailyTolerance, req.ScaleTolerance, id, req.Version).Scan(&version)
		if errors.Is(err, sql.ErrNoRows) {
			writeConcurrentEditError(w)
			return
		}
		if err != nil {
			plannerFail(w, http.StatusBadRequest, failMsg, "plan update failed", err)
			return
		}
	}
	if creating || req.Meals != nil {
		if err := replacePlanMeals(ctx, tx, id, req.Meals); err != nil {
			if errors.Is(err, errPlanMealNotContracted) {
				writeJSONError(w, http.StatusBadRequest, "Um grupo não contratou uma das refeições do plano. Ajuste o grupo ou a refeição.")
				return
			}
			plannerFail(w, http.StatusBadRequest, failMsg, "plan meals failed", err)
			return
		}
	} else if _, err := tx.ExecContext(ctx, `DELETE FROM menu_plan_meals WHERE plan_id=$1 AND (day < (SELECT start_date FROM menu_plans WHERE id=$1) OR day > (SELECT end_date FROM menu_plans WHERE id=$1))`, id); err != nil {
		plannerFail(w, http.StatusInternalServerError, failMsg, "plan trim failed", err)
		return
	}
	if err := syncPlanPositions(ctx, tx, id); err != nil {
		plannerFail(w, http.StatusInternalServerError, failMsg, "plan positions sync failed", err)
		return
	}
	if msg, err := updatePlanPositions(ctx, tx, id, req.Positions); msg != "" || err != nil {
		if msg == "" {
			msg = failMsg
		}
		plannerFail(w, http.StatusBadRequest, msg, "plan positions failed", err)
		return
	}
	if err := tx.Commit(); err != nil {
		plannerFail(w, http.StatusInternalServerError, failMsg, "plan commit failed", err)
		return
	}
	status := http.StatusOK
	if creating {
		status = http.StatusCreated
	}
	writeJSON(w, status, map[string]any{"id": id, "version": version})
}

var errPlanMealNotContracted = errors.New("meal not contracted by group")

func replacePlanMeals(ctx context.Context, tx *sql.Tx, id int64, meals []planMealRequest) error {
	keep := map[planMealKey]bool{}
	for _, m := range meals {
		keep[planMealKey{m.GroupID, m.Day, m.Meal}] = true
	}
	rows, err := tx.QueryContext(ctx, `SELECT group_id,day::text,meal FROM menu_plan_meals WHERE plan_id=$1`, id)
	if err != nil {
		return err
	}
	drop := []planMealKey{}
	for rows.Next() {
		var k planMealKey
		if err := rows.Scan(&k.Group, &k.Day, &k.Meal); err != nil {
			rows.Close()
			return err
		}
		if !keep[k] {
			drop = append(drop, k)
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	for _, k := range drop {
		if _, err := tx.ExecContext(ctx, `DELETE FROM menu_plan_meals WHERE plan_id=$1 AND group_id=$2 AND day=$3::date AND meal=$4`, id, k.Group, k.Day, k.Meal); err != nil {
			return err
		}
	}
	for _, m := range meals {
		if _, err := tx.ExecContext(ctx, `INSERT INTO menu_plan_meals (plan_id,group_id,day,meal,people) VALUES ($1,$2,$3::date,$4,$5)
ON CONFLICT (plan_id,group_id,day,meal) DO UPDATE SET people=EXCLUDED.people`, id, m.GroupID, m.Day, m.Meal, m.People); err != nil {
			return err
		}
	}
	var missing bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM menu_plan_meals pm WHERE pm.plan_id=$1 AND NOT EXISTS (
    SELECT 1 FROM customer_group_meals gm WHERE gm.group_id=pm.group_id AND gm.meal=pm.meal))`, id).Scan(&missing); err != nil {
		return err
	}
	if missing {
		return errPlanMealNotContracted
	}
	return nil
}

// updatePlanPositions grava ficha, trava e adesão das posições enviadas.
// Devolve uma mensagem para a tela quando o pedido é inválido.
func updatePlanPositions(ctx context.Context, tx *sql.Tx, id int64, positions []planPositionRequest) (string, error) {
	for _, p := range positions {
		if p.RecipeID > 0 {
			var fn sql.NullString
			var name string
			err := tx.QueryRowContext(ctx, `SELECT meal_function,name FROM recipes WHERE id=$1`, p.RecipeID).Scan(&fn, &name)
			if errors.Is(err, sql.ErrNoRows) {
				return "Uma das fichas escolhidas não existe mais. Atualize a página.", nil
			}
			if err != nil {
				return "", err
			}
			if !fn.Valid || fn.String != p.Function {
				return fmt.Sprintf("A ficha “%s” não é da função %s. Escolha outra.", name, p.Function), nil
			}
		}
		res, err := tx.ExecContext(ctx, `UPDATE menu_plan_positions SET recipe_id=NULLIF($7,0),locked=$8,adhesion=COALESCE(NULLIF($9,'')::numeric,adhesion)
WHERE plan_id=$1 AND group_id=$2 AND day=$3::date AND meal=$4 AND meal_function=$5 AND option_no=$6`,
			id, p.GroupID, p.Day, p.Meal, p.Function, p.Option, p.RecipeID, p.Locked, p.Adhesion)
		if err != nil {
			return "", err
		}
		if n, _ := res.RowsAffected(); n != 1 {
			return "Uma das posições não existe mais no plano. Atualize a página.", nil
		}
	}
	return "", nil
}

type planVersionRequest struct {
	Version int64 `json:"version"`
}

func (a *app) handleSolvePlan(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	lp := a.loadPlanOrFail(w, r, a.db, id)
	if lp == nil {
		return
	}
	if lp.header.Status != "draft" {
		writeJSONError(w, http.StatusConflict, "Plano aprovado não muda. Para alterar, crie um novo plano.")
		return
	}
	res := solvePlan(lp.in)
	name := func(id int64) any {
		if rec := lp.in.Recipes[id]; rec != nil {
			return rec.Name
		}
		return nil
	}
	changes := []map[string]any{}
	for _, c := range res.Changes {
		p := res.Positions[c.Index]
		changes = append(changes, map[string]any{"group_id": p.Group, "group": lp.in.GroupNames[p.Group], "day": p.Day, "meal": p.Meal,
			"meal_function": p.Function, "option": p.Option, "from_id": c.From, "from": name(c.From), "to_id": c.To, "to": name(c.To), "saving": num(c.Saving, 2)})
	}
	positions := []map[string]any{}
	for _, p := range res.Positions {
		positions = append(positions, map[string]any{"group_id": p.Group, "day": p.Day, "meal": p.Meal, "meal_function": p.Function,
			"option": p.Option, "recipe_id": p.Recipe, "locked": p.Locked, "adhesion": num(p.Adhesion, 2)})
	}
	summary := func(ev planEvaluation) map[string]any {
		gr := []map[string]any{}
		for _, g := range ev.Groups {
			gr = append(gr, map[string]any{"group_id": g.Group, "group": lp.in.GroupNames[g.Group], "revenue": num(g.Revenue, 2), "cost": num(g.Cost, 2), "cmv": numPtr(g.CMV, 2)})
		}
		return map[string]any{"blocked": ev.Blocked, "cmv": numPtr(ev.CMV, 2), "cost": num(ev.Cost, 2), "revenue": num(ev.Revenue, 2), "groups": gr}
	}
	unmet, ignored := res.Unmet, res.Ignored
	if unmet == nil {
		unmet = []string{}
	}
	if ignored == nil {
		ignored = []string{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"version": lp.header.Version, "changes": changes, "positions": positions,
		"before": summary(res.Before), "after": summary(res.After), "unmet": unmet, "ignored": ignored})
}

func (a *app) handleApprovePlan(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var req planVersionRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	ctx := r.Context()
	const failMsg = "Não foi possível aprovar o plano."
	tx, err := a.db.BeginTx(ctx, nil)
	if err != nil {
		plannerFail(w, http.StatusInternalServerError, failMsg, "plan approve begin failed", err)
		return
	}
	defer func() { _ = tx.Rollback() }()
	var status string
	var version int64
	err = tx.QueryRowContext(ctx, `SELECT status,version FROM menu_plans WHERE id=$1 FOR UPDATE`, id).Scan(&status, &version)
	if errors.Is(err, sql.ErrNoRows) {
		writeJSONError(w, http.StatusNotFound, "Plano não encontrado.")
		return
	}
	if err != nil {
		plannerFail(w, http.StatusInternalServerError, failMsg, "plan approve lock failed", err)
		return
	}
	if version != req.Version {
		writeConcurrentEditError(w)
		return
	}
	if status != "draft" {
		writeJSONError(w, http.StatusConflict, "Este plano já foi aprovado.")
		return
	}
	lp := a.loadPlanOrFail(w, r, tx, id)
	if lp == nil {
		return
	}
	ev := evaluatePlan(lp.in)
	if len(ev.Blocked) > 0 {
		writeJSONError(w, http.StatusConflict, "O plano ainda não pode ser aprovado: "+strings.Join(ev.Blocked, " "))
		return
	}
	if len(ev.Violations) > 0 {
		writeJSONError(w, http.StatusConflict, "Corrija as regras quebradas antes de aprovar: "+ev.Violations[0].Message)
		return
	}
	for _, m := range ev.Meals {
		price := m.Revenue / math.Max(m.People, 1)
		for _, pm := range lp.in.Meals {
			if pm.planMealKey == m.planMealKey {
				price = pm.Price
			}
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO menu_plan_snapshot_meals (plan_id,group_id,day,meal,people,price,cost) VALUES ($1,$2,$3::date,$4,$5,$6,$7)`,
			id, m.Group, m.Day, m.Meal, int64(m.People), num(price, 2), num(m.Cost, 4)); err != nil {
			plannerFail(w, http.StatusInternalServerError, failMsg, "plan snapshot meal failed", err)
			return
		}
	}
	for _, it := range ev.Items {
		for d, q := range it.ByDay {
			if q <= 0 {
				continue
			}
			if _, err := tx.ExecContext(ctx, `INSERT INTO menu_plan_snapshot_items (plan_id,item_id,day,quantity,unit_cost,cost_source) VALUES ($1,$2,$3::date,$4,$5,$6)`,
				id, it.ItemID, lp.in.Days[d], num(q, 4), num(it.Unit, 4), it.Source); err != nil {
				plannerFail(w, http.StatusInternalServerError, failMsg, "plan snapshot item failed", err)
				return
			}
		}
	}
	if err := tx.QueryRowContext(ctx, `UPDATE menu_plans SET status='approved',approved_at=now(),approved_by=$2,version=version+1 WHERE id=$1 RETURNING version`, id, userFromContext(ctx).ID).Scan(&version); err != nil {
		plannerFail(w, http.StatusInternalServerError, failMsg, "plan approve update failed", err)
		return
	}
	if err := tx.Commit(); err != nil {
		plannerFail(w, http.StatusInternalServerError, failMsg, "plan approve commit failed", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"id": id, "version": version, "status": "approved"})
}

type planActualRequest struct {
	GroupID int64  `json:"group_id"`
	Day     string `json:"day"`
	Meal    string `json:"meal"`
	People  *int   `json:"people"`
	Version int64  `json:"version"`
}

func (a *app) handleSavePlanActual(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var req planActualRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.People == nil || *req.People < 0 || *req.People > 1000000 {
		writeJSONError(w, http.StatusBadRequest, "Digite a quantidade real de refeições entre 0 e 1.000.000.")
		return
	}
	ctx := r.Context()
	var status string
	err := a.db.QueryRowContext(ctx, `SELECT status FROM menu_plans WHERE id=$1`, id).Scan(&status)
	if errors.Is(err, sql.ErrNoRows) {
		writeJSONError(w, http.StatusNotFound, "Plano não encontrado.")
		return
	}
	if err != nil {
		plannerFail(w, http.StatusInternalServerError, "Não foi possível salvar a quantidade real.", "plan actual status failed", err)
		return
	}
	if status != "approved" {
		writeJSONError(w, http.StatusConflict, "Aprove o plano antes de lançar a quantidade real.")
		return
	}
	var version int64
	err = a.db.QueryRowContext(ctx, `UPDATE menu_plan_meals SET actual_people=$5,actual_version=actual_version+1,actual_by=$7,actual_at=now()
WHERE plan_id=$1 AND group_id=$2 AND day=$3::date AND meal=$4 AND actual_version=$6 RETURNING actual_version`,
		id, req.GroupID, req.Day, normalizeLabel(req.Meal), *req.People, req.Version, userFromContext(ctx).ID).Scan(&version)
	if errors.Is(err, sql.ErrNoRows) {
		var exists bool
		if qerr := a.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM menu_plan_meals WHERE plan_id=$1 AND group_id=$2 AND day=$3::date AND meal=$4)`, id, req.GroupID, req.Day, normalizeLabel(req.Meal)).Scan(&exists); qerr != nil || !exists {
			writeJSONError(w, http.StatusNotFound, "Refeição não encontrada no plano.")
			return
		}
		writeConcurrentEditError(w)
		return
	}
	if err != nil {
		plannerFail(w, http.StatusBadRequest, "Não foi possível salvar a quantidade real.", "plan actual failed", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"version": version})
}

// ---------- Solicitação de compra ----------

type purchaseLine struct {
	ItemID                  int64
	Need, Balance, Quantity float64
	Runout                  string
}

// purchaseLines calcula a compra a partir do retrato do plano aprovado:
// necessidade do período menos o saldo atual, e o dia em que o item acaba.
func purchaseLines(ctx context.Context, q inventoryQueryer, planID int64) ([]purchaseLine, error) {
	rows, err := q.QueryContext(ctx, `WITH need AS (
    SELECT item_id,day,sum(quantity)::float8 AS quantity FROM menu_plan_snapshot_items WHERE plan_id=$1 GROUP BY item_id,day
), balance AS (
    SELECT ml.item_id, COALESCE(sum(CASE WHEN ml.direction='IN' THEN ml.quantity ELSE -ml.quantity END),0)::float8 AS qty
    FROM movement_lines ml WHERE ml.item_id IN (SELECT item_id FROM need) GROUP BY ml.item_id
)
SELECT n.item_id,n.day::text,n.quantity,COALESCE(b.qty,0) FROM need n LEFT JOIN balance b ON b.item_id=n.item_id ORDER BY n.item_id,n.day`, planID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []purchaseLine{}
	var cur *purchaseLine
	for rows.Next() {
		var id int64
		var day string
		var q, bal float64
		if err := rows.Scan(&id, &day, &q, &bal); err != nil {
			return nil, err
		}
		if cur == nil || cur.ItemID != id {
			out = append(out, purchaseLine{ItemID: id, Balance: bal})
			cur = &out[len(out)-1]
		}
		cur.Need += q
		if cur.Runout == "" && cur.Need > math.Max(cur.Balance, 0)+plannerEps {
			cur.Runout = day
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	kept := out[:0]
	for _, l := range out {
		l.Quantity = l.Need - math.Max(l.Balance, 0)
		if l.Quantity > 0.00005 {
			kept = append(kept, l)
		}
	}
	return kept, nil
}

type purchaseRequestBody struct {
	OperationID string `json:"operation_id"`
}

func (a *app) handleCreatePurchaseRequest(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var req purchaseRequestBody
	if !decodeJSON(w, r, &req) {
		return
	}
	req.OperationID = strings.TrimSpace(req.OperationID)
	if req.OperationID == "" || len(req.OperationID) > 100 {
		writeJSONError(w, http.StatusBadRequest, "Atualize a página e tente de novo.")
		return
	}
	ctx := r.Context()
	const failMsg = "Não foi possível emitir a solicitação de compra."
	tx, err := a.db.BeginTx(ctx, nil)
	if err != nil {
		plannerFail(w, http.StatusInternalServerError, failMsg, "purchase begin failed", err)
		return
	}
	defer func() { _ = tx.Rollback() }()
	var status string
	err = tx.QueryRowContext(ctx, `SELECT status FROM menu_plans WHERE id=$1`, id).Scan(&status)
	if errors.Is(err, sql.ErrNoRows) {
		writeJSONError(w, http.StatusNotFound, "Plano não encontrado.")
		return
	}
	if err != nil {
		plannerFail(w, http.StatusInternalServerError, failMsg, "purchase plan failed", err)
		return
	}
	if status != "approved" {
		writeJSONError(w, http.StatusConflict, "Aprove o plano antes de emitir a solicitação de compra.")
		return
	}
	var reqID, existingPlan int64
	err = tx.QueryRowContext(ctx, `INSERT INTO purchase_requests (plan_id,operation_id,created_by) VALUES ($1,$2,$3)
ON CONFLICT (operation_id) DO NOTHING RETURNING id`, id, req.OperationID, userFromContext(ctx).ID).Scan(&reqID)
	if errors.Is(err, sql.ErrNoRows) {
		// Mesmo operation_id: devolve a solicitação já emitida (reenvio seguro).
		if err := tx.QueryRowContext(ctx, `SELECT id,plan_id FROM purchase_requests WHERE operation_id=$1`, req.OperationID).Scan(&reqID, &existingPlan); err != nil {
			plannerFail(w, http.StatusInternalServerError, failMsg, "purchase replay failed", err)
			return
		}
		if existingPlan != id {
			writeJSONError(w, http.StatusConflict, "Este envio já foi usado em outro plano. Atualize a página.")
			return
		}
		_ = tx.Rollback()
		a.writePurchaseRequests(w, r, id, reqID, http.StatusOK)
		return
	}
	if err != nil {
		plannerFail(w, http.StatusInternalServerError, failMsg, "purchase insert failed", err)
		return
	}
	lines, err := purchaseLines(ctx, tx, id)
	if err != nil {
		plannerFail(w, http.StatusInternalServerError, failMsg, "purchase lines failed", err)
		return
	}
	for _, l := range lines {
		if _, err := tx.ExecContext(ctx, `INSERT INTO purchase_request_lines (request_id,item_id,need,balance,quantity,runout_date) VALUES ($1,$2,$3,$4,$5,$6::date)`,
			reqID, l.ItemID, num(l.Need, 4), num(l.Balance, 4), num(l.Quantity, 4), l.Runout); err != nil {
			plannerFail(w, http.StatusInternalServerError, failMsg, "purchase line failed", err)
			return
		}
	}
	if err := tx.Commit(); err != nil {
		plannerFail(w, http.StatusInternalServerError, failMsg, "purchase commit failed", err)
		return
	}
	a.writePurchaseRequests(w, r, id, reqID, http.StatusCreated)
}

func (a *app) handlePlanPurchaseRequests(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	a.writePurchaseRequests(w, r, id, 0, http.StatusOK)
}

// writePurchaseRequests devolve as solicitações do plano (ou só uma, se onlyID > 0).
func (a *app) writePurchaseRequests(w http.ResponseWriter, r *http.Request, planID, onlyID int64, status int) {
	rows, err := a.db.QueryContext(r.Context(), `SELECT pr.id,pr.operation_id,pr.created_at,i.id,i.name,i.unit_code,l.need::text,l.balance::text,l.quantity::text,l.runout_date::text
FROM purchase_requests pr
LEFT JOIN purchase_request_lines l ON l.request_id=pr.id
LEFT JOIN inventory_items i ON i.id=l.item_id
WHERE pr.plan_id=$1 AND ($2::bigint=0 OR pr.id=$2::bigint)
ORDER BY pr.created_at DESC,pr.id DESC,l.runout_date,lower(i.name)`, planID, onlyID)
	if err != nil {
		plannerFail(w, http.StatusInternalServerError, "Não foi possível carregar as solicitações de compra.", "purchase list failed", err)
		return
	}
	defer rows.Close()
	out := []map[string]any{}
	byID := map[int64]map[string]any{}
	for rows.Next() {
		var id int64
		var op string
		var created time.Time
		var itemID sql.NullInt64
		var item, unit, need, balance, qty, runout sql.NullString
		if err := rows.Scan(&id, &op, &created, &itemID, &item, &unit, &need, &balance, &qty, &runout); err != nil {
			plannerFail(w, http.StatusInternalServerError, "Não foi possível carregar as solicitações de compra.", "purchase scan failed", err)
			return
		}
		req := byID[id]
		if req == nil {
			req = map[string]any{"id": id, "operation_id": op, "created_at": created, "lines": []map[string]any{}}
			byID[id] = req
			out = append(out, req)
		}
		if itemID.Valid {
			req["lines"] = append(req["lines"].([]map[string]any), map[string]any{"item_id": itemID.Int64, "item": item.String, "unit": unit.String,
				"need": need.String, "balance": balance.String, "quantity": qty.String, "runout_date": runout.String})
		}
	}
	if err := rows.Err(); err != nil {
		plannerFail(w, http.StatusInternalServerError, "Não foi possível carregar as solicitações de compra.", "purchase rows failed", err)
		return
	}
	if onlyID > 0 && len(out) == 1 {
		writeJSON(w, status, out[0])
		return
	}
	writeJSON(w, status, out)
}

// ---------- Previsto x realizado ----------

func (a *app) handlePlanComparison(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	ctx := r.Context()
	const failMsg = "Não foi possível carregar o previsto x realizado."
	var status, start, end string
	err := a.db.QueryRowContext(ctx, `SELECT status,start_date::text,end_date::text FROM menu_plans WHERE id=$1`, id).Scan(&status, &start, &end)
	if errors.Is(err, sql.ErrNoRows) {
		writeJSONError(w, http.StatusNotFound, "Plano não encontrado.")
		return
	}
	if err != nil {
		plannerFail(w, http.StatusInternalServerError, failMsg, "comparison plan failed", err)
		return
	}
	if status != "approved" {
		writeJSONError(w, http.StatusConflict, "O previsto x realizado começa quando o plano é aprovado.")
		return
	}
	costs := a.canSeeCosts(r)

	// Refeições: previsto do retrato e quantidade real lançada.
	rows, err := a.db.QueryContext(ctx, `SELECT s.group_id,g.name,s.day::text,s.meal,s.people,s.price::float8,s.cost::float8,pm.actual_people
FROM menu_plan_snapshot_meals s
JOIN customer_groups g ON g.id=s.group_id
JOIN menu_plan_meals pm ON pm.plan_id=s.plan_id AND pm.group_id=s.group_id AND pm.day=s.day AND pm.meal=s.meal
WHERE s.plan_id=$1
ORDER BY s.day,s.group_id,array_position(ARRAY['cafe','almoco','lanche','jantar']::text[], s.meal)`, id)
	if err != nil {
		plannerFail(w, http.StatusInternalServerError, failMsg, "comparison meals failed", err)
		return
	}
	meals := []map[string]any{}
	var planRev, planCost, realRev float64
	missing := 0
	for rows.Next() {
		var gid, people int64
		var group, day, meal string
		var price, cost float64
		var actual sql.NullInt64
		if err := rows.Scan(&gid, &group, &day, &meal, &people, &price, &cost, &actual); err != nil {
			rows.Close()
			plannerFail(w, http.StatusInternalServerError, failMsg, "comparison meals scan failed", err)
			return
		}
		row := map[string]any{"group_id": gid, "group": group, "day": day, "meal": meal, "people": people, "actual_people": nil}
		planRev += float64(people) * price
		planCost += cost
		if actual.Valid {
			row["actual_people"] = actual.Int64
			realRev += float64(actual.Int64) * price
			if costs {
				row["actual_revenue"] = num(float64(actual.Int64)*price, 2)
			}
		} else {
			missing++
		}
		if costs {
			row["revenue"] = num(float64(people)*price, 2)
			row["cost"] = num(cost, 2)
		}
		meals = append(meals, row)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		plannerFail(w, http.StatusInternalServerError, failMsg, "comparison meals rows failed", err)
		return
	}

	// Itens: previsto do retrato x saídas do estoque no período (fuso de Fortaleza).
	rows, err = a.db.QueryContext(ctx, `WITH planned AS (
    SELECT item_id,sum(quantity)::float8 AS qty,sum(quantity*unit_cost)::float8 AS value FROM menu_plan_snapshot_items WHERE plan_id=$1 GROUP BY item_id
), actual AS (
    SELECT ml.item_id,sum(ml.quantity)::float8 AS qty,sum(ml.quantity*COALESCE(ml.unit_cost,0))::float8 AS value
    FROM movement_lines ml JOIN movements m ON m.id=ml.movement_id
    WHERE m.movement_type='SAIDA' AND ml.direction='OUT'
      AND (m.occurred_at AT TIME ZONE 'America/Fortaleza')::date BETWEEN $2::date AND $3::date
    GROUP BY ml.item_id
)
SELECT i.id,i.name,i.unit_code,COALESCE(p.qty,0),COALESCE(p.value,0),COALESCE(a.qty,0),COALESCE(a.value,0)
FROM inventory_items i
LEFT JOIN planned p ON p.item_id=i.id
LEFT JOIN actual a ON a.item_id=i.id
WHERE p.item_id IS NOT NULL OR a.item_id IS NOT NULL
ORDER BY lower(i.name),i.id`, id, start, end)
	if err != nil {
		plannerFail(w, http.StatusInternalServerError, failMsg, "comparison items failed", err)
		return
	}
	defer rows.Close()
	items := []map[string]any{}
	var realCost float64
	for rows.Next() {
		var itemID int64
		var name, unit string
		var pq, pv, aq, av float64
		if err := rows.Scan(&itemID, &name, &unit, &pq, &pv, &aq, &av); err != nil {
			plannerFail(w, http.StatusInternalServerError, failMsg, "comparison items scan failed", err)
			return
		}
		realCost += av
		row := map[string]any{"item_id": itemID, "item": name, "unit": unit, "planned_quantity": num(pq, 3), "actual_quantity": num(aq, 3), "diff_quantity": num(aq-pq, 3)}
		if costs {
			row["planned_cost"] = num(pv, 2)
			row["actual_cost"] = num(av, 2)
			row["diff_cost"] = num(av-pv, 2)
		}
		items = append(items, row)
	}
	if err := rows.Err(); err != nil {
		plannerFail(w, http.StatusInternalServerError, failMsg, "comparison items rows failed", err)
		return
	}
	out := map[string]any{"meals": meals, "items": items, "meals_without_actual": missing, "show_costs": costs}
	if costs {
		summary := map[string]any{"planned_revenue": num(planRev, 2), "planned_cost": num(planCost, 2), "planned_cmv": numPtr(cmvOf(planCost, planRev), 2),
			"actual_cost": num(realCost, 2), "actual_revenue": nil, "actual_cmv": nil}
		if missing == 0 {
			summary["actual_revenue"] = num(realRev, 2)
			summary["actual_cmv"] = numPtr(cmvOf(realCost, realRev), 2)
		}
		out["summary"] = summary
	}
	writeJSON(w, http.StatusOK, out)
}
