package main

// Motor do planner de cardápio (docs/planner-cardapio-contract.md).
//
// É Go puro: recebe o plano já carregado do banco e devolve a avaliação
// (custo, faturamento, CMV, produção, consumo por item, regras) e a sugestão
// do solver. Não acessa o banco nem grava nada.

import (
	"fmt"
	"math"
	"sort"
	"strings"
)

const (
	plannerEps           = 1e-9
	plannerMaxIterations = 5000
)

var plannerMealNames = map[string]string{"cafe": "Café", "almoco": "Almoço", "lanche": "Lanche", "jantar": "Jantar"}

type planRecipe struct {
	ID          int64
	Name        string
	Function    string
	Yield       float64 // porções que a receita rende
	PortionSize float64 // peso pronto de uma porção (uma pessoa)
	PortionUnit string
	Tags        map[string]bool
	Lines       []planRecipeLine
	Active      bool
}

// planRecipeLine é a quantidade bruta (quantidade × fator de correção) por receita.
type planRecipeLine struct {
	ItemID int64
	Gross  float64
}

type planItemCost struct {
	Cost   float64
	Source string // estoque | ultima_compra
}

type planMealKey struct {
	Group int64
	Day   string
	Meal  string
}

type planMeal struct {
	planMealKey
	People   float64
	Price    float64
	HasPrice bool
}

type planMenuKey struct {
	Group    int64
	Meal     string
	Function string
}

// planMenu é a função do contrato. A adesão da função é repartida entre as
// Options opções (pratos) da função; sem opção informada, conta uma.
type planMenu struct {
	Options   int
	PerCapita float64
	Unit      string
}

type planPosition struct {
	planMealKey
	Function string
	Option   int
	Recipe   int64
	Adhesion float64
	Locked   bool
}

type planRule struct {
	Group    int64
	Kind     string
	WhenTag  string
	Function string
	Tag      string
	Days     int
}

type planInput struct {
	Target, DailyTol, ScaleTol float64
	Days                       []string // todos os dias do período, em ordem
	Meals                      []planMeal
	Positions                  []planPosition
	Recipes                    map[int64]*planRecipe
	Costs                      map[int64]planItemCost // só itens com custo conhecido
	ItemNames                  map[int64]string
	GroupNames                 map[int64]string
	Menu                       map[planMenuKey]planMenu
	Rules                      []planRule
}

type planViolation struct {
	planMealKey
	Function string
	Message  string
}

type planMealResult struct {
	planMealKey
	People, Revenue, Cost float64
	CMV                   *float64
}

type planDayResult struct {
	Day                 string
	Revenue, Cost, Diff float64
	CMV                 *float64
}

type planGroupResult struct {
	Group         int64
	Revenue, Cost float64
	CMV           *float64
	Days          []planDayResult
}

type planPositionResult struct {
	Portions float64 // porções da ficha
	Produced float64 // produto pronto, na unidade do per capita
	Unit     string
	Cost     float64
}

type planItemNeed struct {
	ItemID int64
	Total  float64
	Cost   float64
	Unit   float64 // custo unitário
	Source string
	ByDay  []float64 // alinhado a planInput.Days
}

type planLotGroup struct {
	Group    int64
	Produced float64
}

type planLot struct {
	Day      string
	Recipe   int64
	Unit     string
	Produced float64
	ByGroup  []planLotGroup
}

type planEvaluation struct {
	Blocked    []string
	Violations []planViolation
	Meals      []planMealResult
	Groups     []planGroupResult
	Revenue    float64
	Cost       float64
	CMV        *float64
	Positions  []planPositionResult
	Items      []planItemNeed
	Lots       []planLot
}

type planChange struct {
	Index    int
	From, To int64
	Saving   float64 // economia no custo da posição (negativo = mais caro)
}

type planSolveResult struct {
	Positions []planPosition
	Changes   []planChange
	Before    planEvaluation
	After     planEvaluation
	Unmet     []string
	Ignored   []string
}

type planEngine struct {
	in       *planInput
	dayIdx   map[string]int
	mealIdx  map[planMealKey]int
	mealPos  [][]int
	groupIdx map[int64]int
	groups   []int64
	pos      []planPosition
	posMeal  []int
	posGroup []int
	posDay   []int
	cpp      map[int64]float64 // custo por porção da ficha (só fichas com todos os itens custeados)
	rules    map[int64][]planRule
	maxDays  int

	mealCost        []float64
	dayCost, dayRev [][]float64
	grpCost, grpRev []float64
	costCache       []map[int64]planCostEntry
	share           []map[int64]map[int]int
	shareCost       []map[int64]float64
}

func newPlanEngine(in *planInput) *planEngine {
	e := &planEngine{in: in, dayIdx: map[string]int{}, mealIdx: map[planMealKey]int{}, groupIdx: map[int64]int{}, cpp: map[int64]float64{}, rules: map[int64][]planRule{}}
	for i, d := range in.Days {
		e.dayIdx[d] = i
	}
	for i, m := range in.Meals {
		e.mealIdx[m.planMealKey] = i
		if _, ok := e.groupIdx[m.Group]; !ok {
			e.groupIdx[m.Group] = len(e.groups)
			e.groups = append(e.groups, m.Group)
		}
	}
	sort.Slice(e.groups, func(a, b int) bool { return e.groups[a] < e.groups[b] })
	for i, g := range e.groups {
		e.groupIdx[g] = i
	}
	e.mealPos = make([][]int, len(in.Meals))
	e.costCache = make([]map[int64]planCostEntry, len(in.Positions))
	for i := range e.costCache {
		e.costCache[i] = map[int64]planCostEntry{}
	}
	e.pos = append([]planPosition(nil), in.Positions...)
	for i, p := range e.pos {
		mi := e.mealIdx[p.planMealKey]
		e.mealPos[mi] = append(e.mealPos[mi], i)
		e.posMeal = append(e.posMeal, mi)
		e.posGroup = append(e.posGroup, e.groupIdx[p.Group])
		e.posDay = append(e.posDay, e.dayIdx[p.Day])
	}
	for id, r := range in.Recipes {
		total, ok := 0.0, len(r.Lines) > 0 && r.Yield > 0
		for _, l := range r.Lines {
			c, known := in.Costs[l.ItemID]
			if !known {
				ok = false
				break
			}
			total += l.Gross * c.Cost
		}
		if ok {
			e.cpp[id] = total / r.Yield
		}
	}
	for _, r := range in.Rules {
		e.rules[r.Group] = append(e.rules[r.Group], r)
		if r.Kind == "no_repeat" && r.Days > e.maxDays {
			e.maxDays = r.Days
		}
	}
	if e.maxDays < 1 {
		e.maxDays = 1 // variedade olha o dia anterior
	}
	e.reset()
	return e
}

func (e *planEngine) reset() {
	e.mealCost = make([]float64, len(e.in.Meals))
	e.dayCost = make([][]float64, len(e.groups))
	e.dayRev = make([][]float64, len(e.groups))
	for g := range e.groups {
		e.dayCost[g] = make([]float64, len(e.in.Days))
		e.dayRev[g] = make([]float64, len(e.in.Days))
	}
	e.grpCost = make([]float64, len(e.groups))
	e.grpRev = make([]float64, len(e.groups))
	e.share = make([]map[int64]map[int]int, len(e.in.Days))
	e.shareCost = make([]map[int64]float64, len(e.in.Days))
	for d := range e.share {
		e.share[d] = map[int64]map[int]int{}
		e.shareCost[d] = map[int64]float64{}
	}
	for _, m := range e.in.Meals {
		g, d := e.groupIdx[m.Group], e.dayIdx[m.Day]
		rev := m.People * m.Price
		e.dayRev[g][d] += rev
		e.grpRev[g] += rev
	}
	for i, p := range e.pos {
		if p.Recipe != 0 {
			e.add(i, p.Recipe, 1)
		}
	}
}

// portions devolve as porções da ficha necessárias na posição:
// pessoas × adesão da função ÷ opções × per capita do contrato ÷ peso da porção da ficha.
func (e *planEngine) portions(i int, recipeID int64) (float64, float64, string, bool) {
	p := e.pos[i]
	r := e.in.Recipes[recipeID]
	menu, ok := e.in.Menu[planMenuKey{p.Group, p.Meal, p.Function}]
	if r == nil || !ok || r.PortionSize <= 0 || r.PortionUnit != menu.Unit {
		return 0, 0, "", false
	}
	people := e.in.Meals[e.posMeal[i]].People
	options := menu.Options
	if options < 1 {
		options = 1
	}
	produced := people * p.Adhesion / 100 * menu.PerCapita / float64(options)
	return produced / r.PortionSize, produced, menu.Unit, true
}

type planCostEntry struct {
	cost float64
	ok   bool
}

// posCost é o custo da ficha na posição. Pessoas e adesão não mudam durante o
// solver, então o valor fica em cache por posição.
func (e *planEngine) posCost(i int, recipeID int64) (float64, bool) {
	if v, hit := e.costCache[i][recipeID]; hit {
		return v.cost, v.ok
	}
	var v planCostEntry
	if c, ok := e.cpp[recipeID]; ok {
		if n, _, _, ok := e.portions(i, recipeID); ok {
			v = planCostEntry{n * c, true}
		}
	}
	e.costCache[i][recipeID] = v
	return v.cost, v.ok
}

func (e *planEngine) valid(i int, recipeID int64) bool {
	r := e.in.Recipes[recipeID]
	if r == nil || r.Function != e.pos[i].Function {
		return false
	}
	_, ok := e.posCost(i, recipeID)
	return ok
}

func (e *planEngine) add(i int, recipeID int64, sign float64) {
	c, _ := e.posCost(i, recipeID)
	mi, g, d := e.posMeal[i], e.posGroup[i], e.posDay[i]
	e.mealCost[mi] += sign * c
	e.dayCost[g][d] += sign * c
	e.grpCost[g] += sign * c
	byGroup := e.share[d][recipeID]
	if byGroup == nil {
		byGroup = map[int]int{}
		e.share[d][recipeID] = byGroup
	}
	byGroup[g] += int(sign)
	e.shareCost[d][recipeID] += sign * c
	if byGroup[g] <= 0 {
		delete(byGroup, g)
	}
}

func (e *planEngine) set(i int, recipeID int64) {
	if old := e.pos[i].Recipe; old != 0 {
		e.add(i, old, -1)
	}
	e.pos[i].Recipe = recipeID
	if recipeID != 0 {
		e.add(i, recipeID, 1)
	}
}

func excess(cost, rev, limit float64) float64 {
	if rev <= 0 {
		return 0
	}
	return math.Max(0, cost/rev*100-limit)
}

// mealViolations avalia as regras do grupo na refeição. Se collect não for nil,
// recebe cada violação com a mensagem para a tela.
func (e *planEngine) mealViolations(mi int, collect func(fn, msg string)) int {
	m := e.in.Meals[mi]
	n := 0
	for _, rule := range e.rules[m.Group] {
		if rule.Kind == "no_repeat" {
			n += e.repeatViolations(mi, rule, collect)
			continue
		}
		triggered := rule.WhenTag == ""
		with, without := 0, 0
		for _, i := range e.mealPos[mi] {
			r := e.in.Recipes[e.pos[i].Recipe]
			if r == nil {
				continue
			}
			if !triggered && r.Tags[rule.WhenTag] {
				triggered = true
			}
			if e.pos[i].Function == rule.Function {
				if r.Tags[rule.Tag] {
					with++
				} else {
					without++
				}
			}
		}
		if !triggered {
			continue
		}
		broken := (rule.Kind == "exists_with" && with == 0) ||
			(rule.Kind == "exists_without" && without == 0) ||
			(rule.Kind == "none_with" && with > 0)
		if !broken {
			continue
		}
		n++
		if collect == nil {
			continue
		}
		when := ""
		if rule.WhenTag != "" {
			when = fmt.Sprintf(" quando houver “%s”", rule.WhenTag)
		}
		switch rule.Kind {
		case "exists_with":
			collect(rule.Function, fmt.Sprintf("Falta uma opção de %s com “%s”%s.", rule.Function, rule.Tag, when))
		case "exists_without":
			collect(rule.Function, fmt.Sprintf("Falta uma opção de %s sem “%s”%s.", rule.Function, rule.Tag, when))
		default:
			collect(rule.Function, fmt.Sprintf("%s não pode ter “%s”%s.", capitalizeFirst(rule.Function), rule.Tag, when))
		}
	}
	return n
}

// repeatViolations compara a refeição com as mesmas refeições dos dias anteriores.
func (e *planEngine) repeatViolations(mi int, rule planRule, collect func(fn, msg string)) int {
	m := e.in.Meals[mi]
	d := e.dayIdx[m.Day]
	n := 0
	for _, i := range e.mealPos[mi] {
		r := e.in.Recipes[e.pos[i].Recipe]
		if r == nil || e.pos[i].Function != rule.Function || (rule.Tag != "" && !r.Tags[rule.Tag]) {
			continue
		}
		for k := 1; k <= rule.Days && d-k >= 0; k++ {
			pm, ok := e.mealIdx[planMealKey{m.Group, e.in.Days[d-k], m.Meal}]
			if !ok {
				continue
			}
			hit := false
			for _, j := range e.mealPos[pm] {
				q := e.in.Recipes[e.pos[j].Recipe]
				if q == nil || e.pos[j].Function != rule.Function {
					continue
				}
				if (rule.Tag == "" && q.ID == r.ID) || (rule.Tag != "" && q.Tags[rule.Tag]) {
					hit = true
					break
				}
			}
			if hit {
				n++
				if collect != nil {
					what := r.Name
					if rule.Tag != "" {
						what = rule.Tag
					}
					collect(rule.Function, fmt.Sprintf("%s repete “%s” em menos de %d dia(s).", capitalizeFirst(rule.Function), what, rule.Days+1))
				}
				break
			}
		}
	}
	return n
}

// repeats conta posições que repetem a ficha do dia anterior na mesma função (variedade).
func (e *planEngine) repeats(mi int) int {
	m := e.in.Meals[mi]
	d := e.dayIdx[m.Day]
	if d == 0 {
		return 0
	}
	pm, ok := e.mealIdx[planMealKey{m.Group, e.in.Days[d-1], m.Meal}]
	if !ok {
		return 0
	}
	n := 0
	for _, i := range e.mealPos[mi] {
		for _, j := range e.mealPos[pm] {
			if e.pos[i].Recipe != 0 && e.pos[i].Recipe == e.pos[j].Recipe && e.pos[i].Function == e.pos[j].Function {
				n++
				break
			}
		}
	}
	return n
}

func (e *planEngine) sharing(d int, recipes ...int64) int {
	n := 0
	for _, r := range recipes {
		if r != 0 && len(e.share[d][r]) > 1 {
			n += len(e.share[d][r]) - 1
		}
	}
	return n
}

// local devolve as parcelas do placar afetadas por uma troca na posição i:
// [0] posições vazias ou inválidas + regras quebradas, [1] excesso diário,
// [2] excesso no período, [3] repetições do dia anterior, [4] custo efetivo das
// fichas trocadas no dia (preparação compartilhada entre grupos ganha desconto
// da tolerância de escala), [5] menos compartilhamento entre grupos.
func (e *planEngine) local(i int, recipes ...int64) [6]float64 {
	var s [6]float64
	p := e.pos[i]
	g, d := e.posGroup[i], e.posDay[i]
	if p.Recipe == 0 || !e.valid(i, p.Recipe) {
		s[0]++
	}
	for k := 0; k <= e.maxDays && d+k < len(e.in.Days); k++ {
		if mi, ok := e.mealIdx[planMealKey{p.Group, e.in.Days[d+k], p.Meal}]; ok {
			s[0] += float64(e.mealViolations(mi, nil))
			if k <= 1 {
				s[3] += float64(e.repeats(mi))
			}
		}
	}
	s[1] = excess(e.dayCost[g][d], e.dayRev[g][d], e.in.Target+e.in.DailyTol)
	s[2] = excess(e.grpCost[g], e.grpRev[g], e.in.Target)
	for n, r := range recipes {
		if r == 0 || (n > 0 && r == recipes[0]) {
			continue
		}
		k := float64(len(e.share[d][r]))
		if k > 0 {
			s[4] += e.shareCost[d][r] * (1 - e.in.ScaleTol/100*(k-1)/k)
		}
	}
	s[5] = -float64(e.sharing(d, recipes...))
	return s
}

func lexLess(a, b []float64) bool {
	for k := range a {
		if a[k] < b[k]-plannerEps {
			return true
		}
		if a[k] > b[k]+plannerEps {
			return false
		}
	}
	return false
}

type planMove struct {
	i      int
	to     int64
	key    []float64
	saving float64
}

// tryMove avalia a troca sem aplicá-la. Posição preenchida só muda para reduzir
// pendências, regras quebradas, excesso de CMV ou repetição; o custo menor sozinho
// não desfaz a escolha de quem planeja. Ganho de escala: quando a troca mexe no
// compartilhamento e a diferença de custo da refeição está dentro da tolerância,
// o custo efetivo (com desconto de escala) é comparado antes do excesso de CMV.
func (e *planEngine) tryMove(i int, to int64) (planMove, bool) {
	from := e.pos[i].Recipe
	mi := e.posMeal[i]
	before := e.local(i, from, to)
	oldMeal := e.mealCost[mi]
	e.set(i, to)
	after := e.local(i, from, to)
	newMeal := e.mealCost[mi]
	e.set(i, from)
	var d [6]float64
	for k := range d {
		d[k] = after[k] - before[k]
	}
	mv := planMove{i: i, to: to, saving: oldMeal - newMeal}
	sharingChanged := math.Abs(d[5]) > plannerEps
	withinTol := from != 0 && math.Abs(newMeal-oldMeal) <= e.in.ScaleTol/100*math.Max(oldMeal, newMeal)+plannerEps
	switch {
	case sharingChanged && withinTol:
		mv.key = []float64{d[0], d[1], d[4], d[2], d[3]}
	case from == 0 || d[5] < -plannerEps:
		mv.key = []float64{d[0], d[1], d[2], d[3], d[4]}
	default:
		mv.key = []float64{d[0], d[1], d[2], d[3], 0}
	}
	return mv, lexLess(mv.key, make([]float64, len(mv.key)))
}

func (e *planEngine) candidates(i int) []int64 {
	out := []int64{}
	for id, r := range e.in.Recipes {
		if r.Active && e.valid(i, id) {
			out = append(out, id)
		}
	}
	sort.Slice(out, func(a, b int) bool { return out[a] < out[b] })
	return out
}

func (e *planEngine) solve() []planChange {
	original := make([]int64, len(e.pos))
	cands := make([][]int64, len(e.pos))
	for i, p := range e.pos {
		original[i] = p.Recipe
		if !p.Locked {
			cands[i] = e.candidates(i)
		}
	}
	// Primeiro preenche as posições vazias, uma a uma, com a melhor candidata.
	for i, p := range e.pos {
		if p.Recipe != 0 || p.Locked {
			continue
		}
		var best *planMove
		for _, to := range cands[i] {
			mv, ok := e.tryMove(i, to)
			if ok && (best == nil || lexLess(mv.key, best.key)) {
				m := mv
				best = &m
			}
		}
		if best != nil {
			e.set(i, best.to)
		}
	}
	// Depois melhora trocando, sempre a troca de maior ganho. Uma troca só muda o
	// placar das posições do mesmo grupo ou do mesmo dia; as demais ficam em cache.
	better := func(a, b *planMove) bool {
		return b == nil || lexLess(a.key, b.key) || (!lexLess(b.key, a.key) && a.saving > b.saving+plannerEps)
	}
	bestOf := make([]*planMove, len(e.pos))
	dirty := make([]bool, len(e.pos))
	for i := range dirty {
		dirty[i] = true
	}
	for it := 0; it < plannerMaxIterations; it++ {
		var best *planMove
		for i := range e.pos {
			if dirty[i] {
				dirty[i] = false
				bestOf[i] = nil
				for _, to := range cands[i] {
					if to == e.pos[i].Recipe {
						continue
					}
					if mv, ok := e.tryMove(i, to); ok && better(&mv, bestOf[i]) {
						m := mv
						bestOf[i] = &m
					}
				}
			}
			if bestOf[i] != nil && better(bestOf[i], best) {
				best = bestOf[i]
			}
		}
		if best == nil {
			break
		}
		g, d := e.posGroup[best.i], e.posDay[best.i]
		e.set(best.i, best.to)
		for j := range e.pos {
			if e.posGroup[j] == g || e.posDay[j] == d {
				dirty[j] = true
			}
		}
	}
	changes := []planChange{}
	for i, p := range e.pos {
		if p.Recipe != original[i] {
			oldCost, _ := e.posCost(i, original[i])
			newCost, _ := e.posCost(i, p.Recipe)
			changes = append(changes, planChange{Index: i, From: original[i], To: p.Recipe, Saving: oldCost - newCost})
		}
	}
	return changes
}

func (e *planEngine) blockers() []string {
	out := []string{}
	empty := 0
	missingItems := map[int64]bool{}
	for _, p := range e.pos {
		if p.Recipe == 0 {
			empty++
			continue
		}
		r := e.in.Recipes[p.Recipe]
		if r == nil {
			out = append(out, "Uma ficha escolhida não existe mais. Escolha outra.")
			continue
		}
		if r.PortionSize <= 0 {
			out = append(out, fmt.Sprintf("A ficha “%s” não tem o peso da porção pronta. Complete a ficha técnica.", r.Name))
			continue
		}
		if r.Function != p.Function {
			out = append(out, fmt.Sprintf("A ficha “%s” não é da função %s. Escolha outra.", r.Name, p.Function))
			continue
		}
		if menu, ok := e.in.Menu[planMenuKey{p.Group, p.Meal, p.Function}]; !ok || menu.Unit != r.PortionUnit {
			out = append(out, fmt.Sprintf("A ficha “%s” não serve em %s: o contrato do grupo não tem per capita para essa função ou usa outra unidade. Ajuste a ficha ou o contrato.", r.Name, p.Function))
			continue
		}
		if _, ok := e.cpp[p.Recipe]; !ok {
			for _, l := range r.Lines {
				if _, known := e.in.Costs[l.ItemID]; !known {
					missingItems[l.ItemID] = true
				}
			}
		}
	}
	if len(missingItems) > 0 {
		names := []string{}
		for id := range missingItems {
			names = append(names, e.in.ItemNames[id])
		}
		sort.Strings(names)
		out = append(out, "Sem custo e sem nenhuma compra registrada: "+strings.Join(names, ", ")+". Registre uma entrada com preço para esses itens.")
	}
	for _, m := range e.in.Meals {
		if !m.HasPrice {
			out = append(out, fmt.Sprintf("O grupo %s não tem preço para %s. Cadastre o preço no grupo.", e.in.GroupNames[m.Group], plannerMealNames[m.Meal]))
		}
	}
	if empty > 0 {
		out = append(out, fmt.Sprintf("Faltam %d prato(s) a escolher. Escolha na grade ou use a sugestão do solver.", empty))
	}
	return dedupeStrings(out)
}

func capitalizeFirst(s string) string {
	r := []rune(s)
	if len(r) == 0 {
		return s
	}
	return strings.ToUpper(string(r[0])) + string(r[1:])
}

func dedupeStrings(in []string) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, s := range in {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}

func cmvOf(cost, rev float64) *float64 {
	if rev <= 0 {
		return nil
	}
	v := cost / rev * 100
	return &v
}

func (e *planEngine) evaluate() planEvaluation {
	ev := planEvaluation{Blocked: e.blockers()}
	for mi, m := range e.in.Meals {
		e.mealViolations(mi, func(fn, msg string) {
			ev.Violations = append(ev.Violations, planViolation{m.planMealKey, fn, msg})
		})
	}
	if len(ev.Blocked) > 0 {
		return ev
	}
	for mi, m := range e.in.Meals {
		rev := m.People * m.Price
		ev.Meals = append(ev.Meals, planMealResult{m.planMealKey, m.People, rev, e.mealCost[mi], cmvOf(e.mealCost[mi], rev)})
	}
	for g, id := range e.groups {
		gr := planGroupResult{Group: id, Revenue: e.grpRev[g], Cost: e.grpCost[g], CMV: cmvOf(e.grpCost[g], e.grpRev[g])}
		for d, day := range e.in.Days {
			if e.dayRev[g][d] > 0 || e.dayCost[g][d] > 0 {
				gr.Days = append(gr.Days, planDayResult{Day: day, Revenue: e.dayRev[g][d], Cost: e.dayCost[g][d], CMV: cmvOf(e.dayCost[g][d], e.dayRev[g][d])})
			}
		}
		ev.Groups = append(ev.Groups, gr)
		ev.Revenue += e.grpRev[g]
		ev.Cost += e.grpCost[g]
	}
	ev.CMV = cmvOf(ev.Cost, ev.Revenue)

	items := map[int64]*planItemNeed{}
	lots := map[[2]int64]*planLot{} // (dia, ficha)
	lotGroups := map[[3]int64]float64{}
	for i, p := range e.pos {
		n, produced, unit, _ := e.portions(i, p.Recipe)
		cost, _ := e.posCost(i, p.Recipe)
		ev.Positions = append(ev.Positions, planPositionResult{Portions: n, Produced: produced, Unit: unit, Cost: cost})
		r := e.in.Recipes[p.Recipe]
		d := e.posDay[i]
		for _, l := range r.Lines {
			need := items[l.ItemID]
			if need == nil {
				c := e.in.Costs[l.ItemID]
				need = &planItemNeed{ItemID: l.ItemID, Unit: c.Cost, Source: c.Source, ByDay: make([]float64, len(e.in.Days))}
				items[l.ItemID] = need
			}
			q := l.Gross * n / r.Yield
			need.ByDay[d] += q
			need.Total += q
			need.Cost += q * need.Unit
		}
		k := [2]int64{int64(d), p.Recipe}
		if lots[k] == nil {
			lots[k] = &planLot{Day: p.Day, Recipe: p.Recipe, Unit: unit}
		}
		lots[k].Produced += produced
		lotGroups[[3]int64{int64(d), p.Recipe, p.Group}] += produced
	}
	for _, need := range items {
		ev.Items = append(ev.Items, *need)
	}
	sort.Slice(ev.Items, func(a, b int) bool { return ev.Items[a].ItemID < ev.Items[b].ItemID })
	for k, lot := range lots {
		for _, g := range e.groups {
			if q := lotGroups[[3]int64{k[0], k[1], g}]; q > 0 {
				lot.ByGroup = append(lot.ByGroup, planLotGroup{Group: g, Produced: q})
			}
		}
		ev.Lots = append(ev.Lots, *lot)
	}
	sort.Slice(ev.Lots, func(a, b int) bool {
		if ev.Lots[a].Day != ev.Lots[b].Day {
			return ev.Lots[a].Day < ev.Lots[b].Day
		}
		return ev.Lots[a].Recipe < ev.Lots[b].Recipe
	})
	return ev
}

func evaluatePlan(in *planInput) planEvaluation { return newPlanEngine(in).evaluate() }

// solvePlan sugere fichas para as posições não travadas. Não altera a entrada.
func solvePlan(in *planInput) planSolveResult {
	e := newPlanEngine(in)
	res := planSolveResult{Before: e.evaluate()}
	for id, r := range in.Recipes {
		if r.Active && r.Function != "" && r.PortionSize > 0 {
			if _, ok := e.cpp[id]; !ok {
				res.Ignored = append(res.Ignored, r.Name)
			}
		}
	}
	sort.Strings(res.Ignored)
	res.Changes = e.solve()
	res.Positions = e.pos
	res.After = e.evaluate()
	for _, p := range e.pos {
		if p.Recipe == 0 {
			res.Unmet = append(res.Unmet, fmt.Sprintf("%s, %s, %s: nenhuma ficha de %s serve (falta peso da porção, unidade igual ao contrato ou custo).", in.GroupNames[p.Group], p.Day, plannerMealNames[p.Meal], p.Function))
		}
	}
	for _, v := range res.After.Violations {
		res.Unmet = append(res.Unmet, fmt.Sprintf("%s, %s, %s: %s", in.GroupNames[v.Group], v.Day, plannerMealNames[v.Meal], v.Message))
	}
	for _, g := range res.After.Groups {
		if g.CMV != nil && *g.CMV > in.Target+0.005 {
			res.Unmet = append(res.Unmet, fmt.Sprintf("%s: o menor CMV possível com as travas e regras atuais é %.2f%%, acima do alvo de %.2f%%.", in.GroupNames[g.Group], *g.CMV, in.Target))
		}
		for _, d := range g.Days {
			if d.CMV != nil && *d.CMV > in.Target+in.DailyTol+0.005 {
				res.Unmet = append(res.Unmet, fmt.Sprintf("%s, %s: CMV do dia de %.2f%%, acima do limite diário de %.2f%%.", in.GroupNames[g.Group], d.Day, *d.CMV, in.Target+in.DailyTol))
			}
		}
	}
	return res
}
