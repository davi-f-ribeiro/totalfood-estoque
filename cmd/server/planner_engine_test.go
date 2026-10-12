package main

import (
	"math"
	"reflect"
	"strings"
	"testing"
)

// Testes do motor do planner, sem banco.

type engFixture struct{ in *planInput }

func newEngFixture(days ...string) *engFixture {
	return &engFixture{in: &planInput{
		Target: 40, DailyTol: 5, ScaleTol: 3, Days: days,
		Recipes: map[int64]*planRecipe{}, Costs: map[int64]planItemCost{},
		ItemNames: map[int64]string{}, GroupNames: map[int64]string{},
		Menu: map[planMenuKey]planMenu{},
	}}
}

func (f *engFixture) item(id int64, name string, cost float64) {
	f.in.ItemNames[id] = name
	if cost >= 0 {
		f.in.Costs[id] = planItemCost{Cost: cost, Source: "estoque"}
	}
}

// recipe cria uma ficha de 1 porção com um único ingrediente.
func (f *engFixture) recipe(id int64, name, fn string, size float64, unit string, item int64, gross float64, tags ...string) {
	t := map[string]bool{}
	for _, x := range tags {
		t[x] = true
	}
	f.in.Recipes[id] = &planRecipe{ID: id, Name: name, Function: fn, Yield: 1, PortionSize: size, PortionUnit: unit, Tags: t, Lines: []planRecipeLine{{item, gross}}, Active: true}
}

func (f *engFixture) group(id int64, name, meal string, price float64, menu map[string]planMenu) {
	f.in.GroupNames[id] = name
	for fn, m := range menu {
		f.in.Menu[planMenuKey{id, meal, fn}] = m
	}
	_ = price
}

func (f *engFixture) meal(group int64, day, meal string, people, price float64) {
	f.in.Meals = append(f.in.Meals, planMeal{planMealKey{group, day, meal}, people, price, true})
}

func (f *engFixture) position(group int64, day, meal, fn string, option int, recipe int64, locked bool) {
	f.in.Positions = append(f.in.Positions, planPosition{planMealKey{group, day, meal}, fn, option, recipe, 100, locked})
}

func near(t *testing.T, name string, got, want float64) {
	t.Helper()
	if math.Abs(got-want) > 1e-6 {
		t.Fatalf("%s = %.6f, want %.6f", name, got, want)
	}
}

func TestPlannerEngineScalesByPeopleAndContractPerCapita(t *testing.T) {
	f := newEngFixture("2026-10-12")
	f.item(1, "Arroz", 6)    // R$ 6/kg
	f.item(2, "Patinho", 40) // R$ 40/kg
	// Arroz: porção de 200 g pronta usa 0,08 kg cru. Bife: porção de 120 g usa 0,15 kg cru.
	f.recipe(10, "Arroz branco", "base", 200, "g", 1, 0.08)
	f.recipe(20, "Bife à parmegiana", "proteína", 120, "g", 2, 0.15)
	f.group(1, "Data Center", "almoco", 20, map[string]planMenu{"base": {PerCapita: 200, Unit: "g"}, "proteína": {PerCapita: 120, Unit: "g"}})
	f.group(2, "Base Aérea", "almoco", 30, map[string]planMenu{"base": {PerCapita: 200, Unit: "g"}, "proteína": {PerCapita: 200, Unit: "g"}})
	f.meal(1, "2026-10-12", "almoco", 900, 20)
	f.meal(2, "2026-10-12", "almoco", 300, 30)
	for _, g := range []int64{1, 2} {
		f.position(g, "2026-10-12", "almoco", "base", 1, 10, false)
		f.position(g, "2026-10-12", "almoco", "proteína", 1, 20, false)
	}
	ev := evaluatePlan(f.in)
	if len(ev.Blocked) > 0 {
		t.Fatalf("blocked: %v", ev.Blocked)
	}
	// 900 pessoas × 200 g = 180 kg de arroz pronto (exemplo do contrato).
	near(t, "arroz pronto DC", ev.Positions[0].Produced, 180000)
	near(t, "porções arroz DC", ev.Positions[0].Portions, 900)
	// Base Aérea: 300 × 200 g ÷ 120 g = 500 porções da ficha do bife.
	near(t, "porções bife BA", ev.Positions[3].Portions, 500)
	// Custo: DC = 900×0,08×6 + 900×0,15×40 = 432 + 5400; BA = 300×0,08×6 + 500×0,15×40 = 144 + 3000.
	near(t, "custo DC", ev.Groups[0].Cost, 5832)
	near(t, "custo BA", ev.Groups[1].Cost, 3144)
	near(t, "faturamento DC", ev.Groups[0].Revenue, 18000)
	near(t, "CMV BA", *ev.Groups[1].CMV, 3144.0/9000*100)
	// O consumo por item fecha com o custo total.
	sum := 0.0
	for _, it := range ev.Items {
		sum += it.Cost
	}
	near(t, "custo dos itens", sum, ev.Cost)
	// Lote consolidado do bife: 900×120 + 300×200 = 168 kg, com a divisão por grupo.
	for _, lot := range ev.Lots {
		if lot.Recipe == 20 {
			near(t, "lote bife", lot.Produced, 168000)
			if len(lot.ByGroup) != 2 {
				t.Fatalf("lote por grupo = %+v", lot.ByGroup)
			}
		}
	}
}

func TestPlannerEngineBlocksWithoutCostAndNeverReturnsPartialNumbers(t *testing.T) {
	f := newEngFixture("2026-10-12")
	f.item(1, "Arroz", 6)
	f.item(2, "Açafrão", -1) // sem custo e sem compra
	f.recipe(10, "Arroz", "base", 200, "g", 1, 0.08)
	f.recipe(11, "Arroz amarelo", "base", 200, "g", 2, 0.01)
	f.group(1, "DC", "almoco", 20, map[string]planMenu{"base": {PerCapita: 200, Unit: "g"}})
	f.meal(1, "2026-10-12", "almoco", 100, 20)
	f.position(1, "2026-10-12", "almoco", "base", 1, 11, false)
	ev := evaluatePlan(f.in)
	if len(ev.Blocked) != 1 || !strings.Contains(ev.Blocked[0], "Açafrão") || ev.Groups != nil || ev.CMV != nil {
		t.Fatalf("blocked=%v groups=%v", ev.Blocked, ev.Groups)
	}
	// O solver ignora a ficha sem custo e escolhe a que tem custo.
	res := solvePlan(f.in)
	if res.Positions[0].Recipe != 10 || len(res.After.Blocked) != 0 || !reflect.DeepEqual(res.Ignored, []string{"Arroz amarelo"}) {
		t.Fatalf("positions=%+v blocked=%v ignored=%v", res.Positions, res.After.Blocked, res.Ignored)
	}
}

func TestPlannerSolverRespectsLockAndFindsCheaperProteinForTarget(t *testing.T) {
	days := []string{"2026-10-12", "2026-10-13"}
	f := newEngFixture(days...)
	f.item(1, "Picanha", 100)
	f.item(2, "Frango", 15)
	f.item(3, "Patinho", 40)
	f.recipe(10, "Picanha", "proteína", 100, "g", 1, 0.1)
	f.recipe(20, "Frango assado", "proteína", 100, "g", 2, 0.1)
	f.recipe(30, "Bife", "proteína", 100, "g", 3, 0.1)
	f.group(1, "DC", "almoco", 20, map[string]planMenu{"proteína": {PerCapita: 100, Unit: "g"}})
	f.in.Target = 30
	f.in.DailyTol = 30 // segunda pode estourar; a semana compensa
	for _, d := range days {
		f.meal(1, d, "almoco", 100, 20)
	}
	f.position(1, days[0], "almoco", "proteína", 1, 10, true)  // picanha travada: R$ 10/pessoa = 50%
	f.position(1, days[1], "almoco", "proteína", 1, 30, false) // bife: R$ 4/pessoa
	res := solvePlan(f.in)
	if res.Positions[0].Recipe != 10 {
		t.Fatal("solver mudou uma posição travada")
	}
	// Período: (1000 + 400)/4000 = 35% > 30%. Com frango: (1000 + 150)/4000 = 28,75%.
	if res.Positions[1].Recipe != 20 || len(res.Changes) != 1 || res.Changes[0].From != 30 {
		t.Fatalf("positions=%+v changes=%+v", res.Positions, res.Changes)
	}
	near(t, "economia", res.Changes[0].Saving, 250)
	near(t, "CMV depois", *res.After.Groups[0].CMV, 28.75)
	if len(res.Unmet) != 0 {
		t.Fatalf("unmet=%v", res.Unmet)
	}
	// Determinístico: mesma entrada, mesma saída.
	if again := solvePlan(f.in); !reflect.DeepEqual(again.Positions, res.Positions) {
		t.Fatal("solver não é determinístico")
	}
	// A entrada não é alterada (só sugestão).
	if f.in.Positions[1].Recipe != 30 {
		t.Fatal("solver alterou a entrada")
	}
}

func TestPlannerSolverReportsUnreachableTarget(t *testing.T) {
	f := newEngFixture("2026-10-12")
	f.item(1, "Picanha", 100)
	f.recipe(10, "Picanha", "proteína", 100, "g", 1, 0.1)
	f.group(1, "DC", "almoco", 20, map[string]planMenu{"proteína": {PerCapita: 100, Unit: "g"}})
	f.meal(1, "2026-10-12", "almoco", 100, 20)
	f.position(1, "2026-10-12", "almoco", "proteína", 1, 10, true)
	res := solvePlan(f.in)
	if len(res.Unmet) == 0 || !strings.Contains(strings.Join(res.Unmet, " "), "acima do alvo") {
		t.Fatalf("unmet=%v", res.Unmet)
	}
}

func TestPlannerRuleFeijoadaRequiresBeanThatIsNotBlack(t *testing.T) {
	days := []string{"2026-10-16", "2026-10-17"}
	f := newEngFixture(days...)
	f.item(1, "Feijão preto", 7)
	f.item(2, "Feijão carioca", 9)
	f.item(3, "Carne", 30)
	f.recipe(10, "Feijão preto", "feijão", 150, "g", 1, 0.06, "feijao-preto")
	f.recipe(11, "Feijão carioca", "feijão", 150, "g", 2, 0.06)
	f.recipe(20, "Feijoada", "proteína", 150, "g", 3, 0.1, "feijoada")
	f.recipe(21, "Carne assada", "proteína", 150, "g", 3, 0.1)
	f.group(1, "Data Center", "almoco", 25, map[string]planMenu{"proteína": {PerCapita: 150, Unit: "g"}, "feijão": {PerCapita: 150, Unit: "g"}})
	f.in.Rules = []planRule{{Group: 1, Kind: "exists_without", WhenTag: "feijoada", Function: "feijão", Tag: "feijao-preto"}}
	for _, d := range days {
		f.meal(1, d, "almoco", 100, 25)
	}
	f.position(1, days[0], "almoco", "proteína", 1, 20, true) // sexta: feijoada travada
	f.position(1, days[0], "almoco", "feijão", 1, 0, false)
	f.position(1, days[1], "almoco", "proteína", 1, 21, true)
	f.position(1, days[1], "almoco", "feijão", 1, 0, false)
	ev := evaluatePlan(f.in)
	if len(ev.Violations) != 1 {
		t.Fatalf("violações antes = %+v", ev.Violations)
	}
	res := solvePlan(f.in)
	// Sexta (feijoada) leva carioca; sábado leva o preto, mais barato.
	if res.Positions[1].Recipe != 11 || res.Positions[3].Recipe != 10 || len(res.After.Violations) != 0 {
		t.Fatalf("positions=%+v violations=%+v", res.Positions, res.After.Violations)
	}
}

func TestPlannerRuleNoRepeat(t *testing.T) {
	days := []string{"2026-10-12", "2026-10-13"}
	f := newEngFixture(days...)
	f.item(1, "Frango", 15)
	f.item(2, "Porco", 16)
	f.recipe(10, "Frango assado", "proteína", 100, "g", 1, 0.1, "frango")
	f.recipe(20, "Suíno guisado", "proteína", 100, "g", 2, 0.1, "suino")
	f.group(1, "Quentinhas", "almoco", 20, map[string]planMenu{"proteína": {PerCapita: 100, Unit: "g"}})
	f.in.Rules = []planRule{{Group: 1, Kind: "no_repeat", Function: "proteína", Tag: "frango", Days: 1}}
	for _, d := range days {
		f.meal(1, d, "almoco", 100, 20)
		f.position(1, d, "almoco", "proteína", 1, 10, false)
	}
	if ev := evaluatePlan(f.in); len(ev.Violations) != 1 {
		t.Fatalf("violações = %+v", ev.Violations)
	}
	res := solvePlan(f.in)
	if len(res.After.Violations) != 0 || res.Positions[0].Recipe == res.Positions[1].Recipe {
		t.Fatalf("positions=%+v", res.Positions)
	}
}

func TestPlannerSolverPrefersSharedPreparationWithinScaleTolerance(t *testing.T) {
	build := func(otherCost float64) planSolveResult {
		f := newEngFixture("2026-10-12")
		f.item(1, "Frango", 20)
		f.item(2, "Peixe", otherCost)
		f.recipe(10, "Frango assado", "proteína", 100, "g", 1, 0.1)
		f.recipe(20, "Peixe", "proteína", 100, "g", 2, 0.1)
		for _, g := range []int64{1, 2} {
			f.group(g, "G", "almoco", 20, map[string]planMenu{"proteína": {PerCapita: 100, Unit: "g"}})
			f.meal(g, "2026-10-12", "almoco", 100, 20)
		}
		f.position(1, "2026-10-12", "almoco", "proteína", 1, 10, true) // grupo 1 já faz frango
		f.position(2, "2026-10-12", "almoco", "proteína", 1, 0, false)
		return solvePlan(f.in)
	}
	// Peixe 2% mais barato: dentro da tolerância de 3%, o frango compartilhado vence.
	if res := build(19.6); res.Positions[1].Recipe != 10 {
		t.Fatalf("dentro da tolerância escolheu %d", res.Positions[1].Recipe)
	}
	// Peixe 10% mais barato: fora da tolerância, vence o custo.
	if res := build(18); res.Positions[1].Recipe != 20 {
		t.Fatalf("fora da tolerância escolheu %d", res.Positions[1].Recipe)
	}
}

// A adesão da função é repartida entre as opções: 900 pessoas × 200 g a 100%
// em 2 opções dão 90 kg por prato, e não 180 kg em cada um.
func TestPlannerEngineSplitsFunctionAdhesionAcrossOptions(t *testing.T) {
	f := newEngFixture("2026-10-12")
	f.item(1, "Arroz", 6)
	f.recipe(10, "Arroz branco", "base", 200, "g", 1, 0.08)
	f.recipe(11, "Arroz integral", "base", 200, "g", 1, 0.08)
	f.group(1, "Data Center", "almoco", 20, map[string]planMenu{"base": {Options: 2, PerCapita: 200, Unit: "g"}})
	f.meal(1, "2026-10-12", "almoco", 900, 20)
	f.position(1, "2026-10-12", "almoco", "base", 1, 10, false)
	f.position(1, "2026-10-12", "almoco", "base", 2, 11, false)
	ev := evaluatePlan(f.in)
	if len(ev.Blocked) > 0 {
		t.Fatalf("blocked: %v", ev.Blocked)
	}
	near(t, "pronto opção 1", ev.Positions[0].Produced, 90000)
	near(t, "pronto opção 2", ev.Positions[1].Produced, 90000)
	near(t, "porções opção 1", ev.Positions[0].Portions, 450)
	near(t, "custo do grupo", ev.Groups[0].Cost, 900*0.08*6)
}
