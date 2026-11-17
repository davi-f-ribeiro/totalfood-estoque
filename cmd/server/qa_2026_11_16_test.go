package main

// Provas das correções do teste de 16/11/2026 (main em 40cdd82). Cada teste leva o
// código do caso do relatório em docs/testes/2026-11-16-40cdd82.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// PERM-01: banco novo entrega Gerente e Estoquista com as telas do trabalho deles.
// O ajuste de estoque continua sem perfil automático (migration 030).
func TestQADefaultProfilesCanOperate(t *testing.T) {
	a, _, _, _ := newStockTestApp(t)
	want := map[string][]string{
		"Estoquista": {"stock/read", "catalog/read", "movement_entry/write", "movement_exit/write", "movement_transfer/write", "material_requests/read", "material_fulfillment/write"},
		"Gerente":    {"stock/read", "catalog/read", "operations/read", "inventory_validation/read", "inventory_validation/write", "material_requests/read"},
	}
	for profile, perms := range want {
		for _, perm := range perms {
			parts := strings.Split(perm, "/")
			var ok bool
			if err := a.db.QueryRow(`SELECT EXISTS(SELECT 1 FROM profile_permissions pp JOIN profiles p ON p.id=pp.profile_id
 JOIN permissions perm ON perm.id=pp.permission_id WHERE p.name=$1 AND perm.resource=$2 AND perm.action=$3)`, profile, parts[0], parts[1]).Scan(&ok); err != nil {
				t.Fatal(err)
			}
			if !ok {
				t.Errorf("%s sem %s depois das migrations", profile, perm)
			}
		}
	}
	var adjust int
	if err := a.db.QueryRow(`SELECT count(*) FROM profile_permissions pp JOIN permissions perm ON perm.id=pp.permission_id
 WHERE perm.resource='stock_adjustment'`).Scan(&adjust); err != nil {
		t.Fatal(err)
	}
	if adjust != 0 {
		t.Fatalf("stock_adjustment ganhou %d perfil(is) automático(s), esperado 0", adjust)
	}
	createTestUser(t, a, "qa-estoquista", "secret", "Estoquista", true)
	estoquista := doLogin(t, a, "qa-estoquista", "secret")
	if rr := authReq(a, http.MethodGet, "/api/stock", nil, estoquista, ""); rr.Code != http.StatusOK {
		t.Fatalf("estoquista /api/stock=%d %s", rr.Code, rr.Body.String())
	}
}

// ADM-02: senha redefinida pelo administrador é temporária; o usuário troca ao entrar.
func TestQAAdminPasswordResetForcesChange(t *testing.T) {
	a, _, _, _ := newStockTestApp(t)
	createTestUser(t, a, "qa-reset-dir", "secret", "Diretor", true)
	userID := createTestUser(t, a, "qa-reset-user", "oldpass", "Vendas", true)
	if _, err := a.db.Exec(`UPDATE users SET must_change_password=false WHERE id=$1`, userID); err != nil {
		t.Fatal(err)
	}
	director := doLogin(t, a, "qa-reset-dir", "secret")
	if rr := authReq(a, http.MethodPatch, "/api/admin/users/"+itoa(userID)+"/password", map[string]any{"password": "temp-pass", "version": crudVersion(t, a, "users", userID)}, director, ""); rr.Code != http.StatusOK {
		t.Fatalf("reset=%d %s", rr.Code, rr.Body.String())
	}
	rr := loginAttempt(a, "qa-reset-user", "temp-pass")
	if rr.Code != http.StatusOK || materialTestJSON(t, rr.Body.Bytes())["must_change_password"] != true {
		t.Fatalf("login depois da redefinição=%d %s, esperado must_change_password=true", rr.Code, rr.Body.String())
	}
	// Edição do usuário com senha nova segue a mesma regra.
	if _, err := a.db.Exec(`UPDATE users SET must_change_password=false WHERE id=$1`, userID); err != nil {
		t.Fatal(err)
	}
	if rr := authReq(a, http.MethodPatch, "/api/admin/users/"+itoa(userID), map[string]any{"login": "qa-reset-user", "password": "temp-pass-2", "profile_id": profileID(t, a, "Vendas"), "active": true, "version": crudVersion(t, a, "users", userID)}, director, ""); rr.Code != http.StatusOK {
		t.Fatalf("update com senha=%d %s", rr.Code, rr.Body.String())
	}
	var must bool
	if err := a.db.QueryRow(`SELECT must_change_password FROM users WHERE id=$1`, userID).Scan(&must); err != nil || !must {
		t.Fatalf("must_change_password=%v err=%v depois de editar com senha", must, err)
	}
}

// ACE-07: página não fica guardada no navegador; Voltar depois de Sair não mostra a tela.
func TestQAPagesAreNotStoredByBrowser(t *testing.T) {
	a, _, _, _ := newStockTestApp(t)
	createTestUser(t, a, "qa-cache-dir", "secret", "Diretor", true)
	director := doLogin(t, a, "qa-cache-dir", "secret")
	for _, path := range []string{"/login", "/home", "/stock", "/admin"} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req.AddCookie(director.Cookie)
		rr := httptest.NewRecorder()
		newHTTPServer("127.0.0.1:0", a).Handler.ServeHTTP(rr, req)
		if rr.Code != http.StatusOK || rr.Header().Get("Cache-Control") != "no-store" {
			t.Errorf("%s status=%d Cache-Control=%q, esperado no-store", path, rr.Code, rr.Header().Get("Cache-Control"))
		}
	}
}

// INV-21 / TRV-04 / TRV-05: ajuste de contagem em produto controlado por caixas responde
// com a orientação de ajustar pela caixa, não com erro interno, e não mexe no estoque.
func TestQAInventoryAdjustmentOnCrateStockExplainsInsteadOfFailing(t *testing.T) {
	e := newInventoryTestEnv(t)
	ctx := context.Background()
	wh := e.warehouse(t, "crate-adjust", true)
	sku := crateTestSKU(t, e.db)
	userCtx := context.WithValue(ctx, authCtxKey, authUser{ID: e.userID})
	receive := crateReceiveRequest{OperationID: "qa-crate-adjust-receipt", SKUID: sku, WarehouseID: wh, UnitCost: "2",
		DocumentNumber: "QA-21", SupplierName: "CEASA", Weights: []string{"3.000", "7.000"}}
	if rr := crateTestRequest(t, e.a.handleCrateReceive, "POST", "/api/crates/receive", receive, userCtx); rr.Code != 200 {
		t.Fatalf("receive=%d %s", rr.Code, rr.Body.String())
	}
	inv, err := e.a.createTotalInventory(ctx, e.userID, wh)
	if err != nil {
		t.Fatal(err)
	}
	countInventoryToReview(t, e, inv, map[int64]string{sku: "0"})
	review, err := e.a.getInventoryReviewView(ctx, inv.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.a.saveInventoryRecount(ctx, inv.ID, sku, "0", reviewBySKU(t, review)[sku].Version, e.userID); err != nil {
		t.Fatal(err)
	}
	if _, err := e.a.completeInventory(ctx, inv.ID, e.userID, "Caixas não encontradas."); err != nil {
		t.Fatal(err)
	}
	if _, err := e.a.applyInventoryStockAdjustment(ctx, inv.ID, sku, e.userID, "qa-crate-adjust"); !errors.Is(err, errCrateTrackedStock) {
		t.Fatalf("apply error=%v, esperado errCrateTrackedStock", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/inventory-validations/x/lines/y/adjust", strings.NewReader(`{"operation_id":"qa-crate-adjust-http"}`)).WithContext(userCtx)
	req.SetPathValue("id", strconv.FormatInt(inv.ID, 10))
	req.SetPathValue("sku_id", strconv.FormatInt(sku, 10))
	rr := httptest.NewRecorder()
	e.a.handleInventoryStockAdjustment(rr, req)
	if rr.Code != http.StatusConflict || !strings.Contains(rr.Body.String(), "controlado por caixas") || strings.Contains(rr.Body.String(), "Erro interno") {
		t.Fatalf("adjust=%d %s, esperado 409 com a orientação de caixas", rr.Code, rr.Body.String())
	}
	var stock, crates string
	if err := e.db.QueryRow(`SELECT COALESCE(sum(CASE WHEN direction='IN' THEN quantity ELSE -quantity END),0)::numeric(14,3)::text FROM movement_lines WHERE sku_id=$1 AND warehouse_id=$2`, sku, wh).Scan(&stock); err != nil {
		t.Fatal(err)
	}
	if err := e.db.QueryRow(`SELECT COALESCE(sum(current_weight),0)::numeric(14,3)::text FROM stock_crates WHERE sku_id=$1`, sku).Scan(&crates); err != nil {
		t.Fatal(err)
	}
	if stock != "10.000" || crates != "10.000" {
		t.Fatalf("estoque=%s caixas=%s depois do ajuste recusado, esperado 10.000/10.000", stock, crates)
	}
}

// REL-05 / TRV-10: o relatório lista os ajustes de contagem, com sinal, para explicar o saldo.
func TestQAMovementReportListsInventoryAdjustments(t *testing.T) {
	e, inv, sku := completedAdjustmentWithEngineStock(t, "qa-rel05", "8.000")
	ctx := context.Background()
	if _, err := e.a.applyInventoryStockAdjustment(ctx, inv.ID, sku, e.userID, "qa-rel05-adjust"); err != nil {
		t.Fatal(err)
	}
	start, end := time.Now().Add(-time.Hour), time.Now().Add(time.Hour)
	rows, err := e.a.listMovementReport(ctx, "AJUSTE", start, end, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].Type != "AJUSTE" || rows[0].Quantity != "-2.000" || !strings.Contains(rows[0].Notes, "Ajuste da auditoria de estoque") {
		t.Fatalf("ajustes=%+v, esperado uma linha de -2.000 com a observação da auditoria", rows)
	}
	for _, typ := range []string{"ENTRADA", "SAIDA"} {
		other, err := e.a.listMovementReport(ctx, typ, start, end, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, r := range other {
			if r.Type == "AJUSTE" || strings.HasPrefix(r.Quantity, "-") {
				t.Fatalf("%s trouxe ajuste ou quantidade negativa: %+v", typ, r)
			}
		}
	}
	q := "/api/reports/movements?type=AJUSTE&start=" + start.UTC().Format(time.RFC3339) + "&end=" + end.UTC().Format(time.RFC3339)
	rr := httptest.NewRecorder()
	e.a.handleMovementReport(rr, httptest.NewRequest(http.MethodGet, q, nil))
	if rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), `"quantity":"-2.000"`) {
		t.Fatalf("GET AJUSTE=%d %s", rr.Code, rr.Body.String())
	}
	rr = httptest.NewRecorder()
	e.a.handleMovementReport(rr, httptest.NewRequest(http.MethodGet, strings.Replace(q, "AJUSTE", "TRANSFERENCIA", 1), nil))
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("tipo fora do contrato=%d, esperado 400", rr.Code)
	}
}

// CAT-04: produto novo pode ser cadastrado sem marca e aparece como "Sem marca".
func TestQACreateProductWithoutBrand(t *testing.T) {
	a, _, _, _ := newStockTestApp(t)
	createTestUser(t, a, "qa-nobrand-dir", "secret", "Diretor", true)
	director := doLogin(t, a, "qa-nobrand-dir", "secret")
	var item int64
	if err := a.db.QueryRow(`INSERT INTO inventory_items(name,category_id,unit_code) VALUES('Melão QA',(SELECT id FROM categories WHERE name='Alimentos'),'KG') RETURNING id`).Scan(&item); err != nil {
		t.Fatal(err)
	}
	body := map[string]any{"item_id": item, "brand_id": 0, "content_quantity": "1", "barcode": "7890000004004", "operation_id": testOperationID()}
	rr := authReq(a, http.MethodPost, "/api/catalog/skus", body, director, "")
	if rr.Code != http.StatusCreated {
		t.Fatalf("create sem marca=%d %s", rr.Code, rr.Body.String())
	}
	id := jsonID(t, rr.Body.Bytes())
	var brand int64
	var name string
	if err := a.db.QueryRow(`SELECT brand_id,COALESCE(name,'') FROM product_skus WHERE id=$1`, id).Scan(&brand, &name); err != nil {
		t.Fatal(err)
	}
	if brand != 0 || name != "Melão QA - Sem marca - 1 KG" || strings.Contains(name, "LEGACY") {
		t.Fatalf("brand_id=%d name=%q", brand, name)
	}
	list := authReq(a, http.MethodGet, "/api/catalog/products", nil, director, "")
	if list.Code != http.StatusOK || !strings.Contains(list.Body.String(), `"brand":"Sem marca"`) || strings.Contains(list.Body.String(), "__LEGACY_UNASSIGNED__") {
		t.Fatalf("lista de produtos=%d %s", list.Code, list.Body.String())
	}
	body["brand_id"], body["operation_id"], body["barcode"] = -1, testOperationID(), "7890000004005"
	if rr := authReq(a, http.MethodPost, "/api/catalog/skus", body, director, ""); rr.Code != http.StatusBadRequest {
		t.Fatalf("marca negativa=%d %s, esperado 400", rr.Code, rr.Body.String())
	}
}

// TRV-01: repetir o encerramento é seguro; repetir com outra observação é recusado.
func TestQAMaterialCloseReplayWithOtherNotesIsRefused(t *testing.T) {
	a, sku, wh, _ := newStockTestApp(t)
	createTestUser(t, a, "qa-close-cozinha", "secret", "Cozinha", true)
	createTestUser(t, a, "qa-close-estoquista", "secret", "Estoquista", true)
	cozinha := doLogin(t, a, "qa-close-cozinha", "secret")
	estoquista := doLogin(t, a, "qa-close-estoquista", "secret")
	if _, err := a.createEntry(context.Background(), movementRequest{OperationID: "qa-close-seed", SKUID: sku, WarehouseID: wh, Quantity: "50", UnitCost: "5"}, "qa-close-seed"); err != nil {
		t.Fatal(err)
	}
	base, line := materialOpenRequest(t, a, sku, "qa-close", "10", cozinha)
	res := authReq(a, http.MethodPost, base+"/reserve", map[string]any{"operation_id": "qa-close-res", "line_id": line, "sku_id": sku, "warehouse_id": wh, "quantity": "8"}, estoquista, "")
	if res.Code != 201 {
		t.Fatalf("reserve=%d %s", res.Code, res.Body.String())
	}
	rid := int64(materialTestJSON(t, res.Body.Bytes())["id"].(float64))
	del := authReq(a, http.MethodPost, base+"/deliver", map[string]any{"operation_id": "qa-close-del", "reservation_id": rid, "quantity": "8"}, estoquista, "")
	if del.Code != 201 {
		t.Fatalf("deliver=%d %s", del.Code, del.Body.String())
	}
	delID := int64(materialTestJSON(t, del.Body.Bytes())["id"].(float64))
	if rr := authReq(a, http.MethodPost, base+"/deliveries/"+itoa(delID)+"/receive", map[string]any{}, cozinha, ""); rr.Code != 200 {
		t.Fatalf("receive=%d %s", rr.Code, rr.Body.String())
	}
	first := map[string]any{"operation_id": "qa-close-op", "notes": "Faltaram 2, aceito assim"}
	for i := 0; i < 2; i++ {
		if rr := authReq(a, http.MethodPost, base+"/close", first, cozinha, ""); rr.Code != 200 {
			t.Fatalf("close #%d=%d %s", i+1, rr.Code, rr.Body.String())
		}
	}
	other := authReq(a, http.MethodPost, base+"/close", map[string]any{"operation_id": "qa-close-op", "notes": "Outra observação"}, cozinha, "")
	if other.Code != http.StatusConflict || !strings.Contains(other.Body.String(), "outra observação") {
		t.Fatalf("close com outra observação=%d %s, esperado 409", other.Code, other.Body.String())
	}
	if d := materialStatus(t, a, base, cozinha); d["close_notes"] != "Faltaram 2, aceito assim" {
		t.Fatalf("close_notes=%v mudou depois da repetição recusada", d["close_notes"])
	}
}

// CX-05 / CX-11: a fila diz por que a etiqueta foi cancelada, falhou ou ficou incerta.
func TestQAPrintQueueExposesReason(t *testing.T) {
	e := newInventoryTestEnv(t)
	ctx := context.Background()
	wh := e.warehouse(t, "queue-reason", true)
	sku := crateTestSKU(t, e.db)
	userCtx := context.WithValue(ctx, authCtxKey, authUser{ID: e.userID})
	receive := crateReceiveRequest{OperationID: "qa-queue-receipt", SKUID: sku, WarehouseID: wh, UnitCost: "2",
		DocumentNumber: "QA-CX", SupplierName: "CEASA", Weights: []string{"5.000", "6.000", "7.000"}}
	if rr := crateTestRequest(t, e.a.handleCrateReceive, "POST", "/api/crates/receive", receive, userCtx); rr.Code != 200 {
		t.Fatalf("receive=%d %s", rr.Code, rr.Body.String())
	}
	var ids []int64
	rows, err := e.db.Query(`SELECT id FROM stock_crates WHERE sku_id=$1 ORDER BY id`, sku)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id)
	}
	rows.Close()
	if len(ids) != 3 {
		t.Fatalf("caixas=%d", len(ids))
	}
	// Caixa 1 zerada: etiqueta pendente cancelada com o motivo "Caixa vazia".
	req := httptest.NewRequest("POST", fmt.Sprintf("/api/crates/%d/weigh", ids[0]), strings.NewReader(`{"operation_id":"qa-queue-zero","weight":"0"}`)).WithContext(userCtx)
	req.SetPathValue("id", fmt.Sprint(ids[0]))
	wrec := httptest.NewRecorder()
	e.a.handleCrateWeigh(wrec, req)
	if wrec.Code != 200 {
		t.Fatalf("weigh=%d %s", wrec.Code, wrec.Body.String())
	}
	if _, err := e.db.Exec(`UPDATE print_jobs SET status='FAILED',last_error='Sem papel' WHERE crate_id=$1`, ids[1]); err != nil {
		t.Fatal(err)
	}
	if _, err := e.db.Exec(`UPDATE print_jobs SET status='UNCERTAIN',last_error='Envio USB sem confirmação' WHERE crate_id=$1`, ids[2]); err != nil {
		t.Fatal(err)
	}
	rr := crateTestRequest(t, e.a.handlePrintJobs, "GET", "/api/print/jobs", nil, userCtx)
	var out struct {
		Jobs []struct {
			CrateID int64  `json:"crate_id"`
			Status  string `json:"status"`
			Reason  string `json:"reason"`
		} `json:"jobs"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &out); err != nil || rr.Code != 200 {
		t.Fatalf("jobs=%d %s err=%v", rr.Code, rr.Body.String(), err)
	}
	got := map[int64]string{}
	for _, j := range out.Jobs {
		got[j.CrateID] = j.Status + "|" + j.Reason
	}
	want := map[int64]string{ids[0]: "CANCELLED|Caixa vazia", ids[1]: "FAILED|Sem papel", ids[2]: "UNCERTAIN|Envio USB sem confirmação"}
	for id, w := range want {
		if got[id] != w {
			t.Errorf("caixa %d: fila=%q, esperado %q", id, got[id], w)
		}
	}
}

// Contratos de tela das correções: o texto fica no arquivo servido ao navegador.
func TestQAScreenContracts(t *testing.T) {
	read := func(parts ...string) string {
		t.Helper()
		b, err := os.ReadFile(filepath.Join(parts...))
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	appJS, appCSS := read("static", "app.js"), read("static", "app.css")
	checks := []struct{ id, where, text, want string }{
		{"EST-06", "app.js", appJS, `list.append(d)});applyStockFilters()}`},
		{"EST-07", "home.js", read("static", "home.js"), `f.textContent='Faltam '+displayDecimal(r.shortage)+' '+r.unit`},
		{"EST-07", "home.js", read("static", "home.js"), `['Situação','num']`},
		{"ADM-08", "app.js", appJS, `dispatch:'Expedição',delivery:'Minha rota',fiscal_import:'Nota fiscal'`},
		{"ACE-06", "app.js", appJS, `sessionLostError(lastErr)?describe(current)+' não foi enviado: sua sessão foi encerrada.`},
		{"CX-05", "crates.js", read("static", "crates.js"), `CANCELLED:'Cancelada'`},
		{"CX-11", "crates.js", read("static", "crates.js"), `(job.reason?': '+job.reason:'')`},
		{"CAT-06", "products.html", read("templates", "products.html"), `{{if .CanCatalogWrite}}<button id="productNew" type="button">+ Novo produto</button>{{end}}`},
		{"CAT-04", "products.html", read("templates", "products.html"), `<input id="productNoBrand" type="checkbox"> Sem marca`},
		{"REL-05", "reports.html", read("templates", "reports.html"), `<option value="AJUSTE">Ajustes de contagem</option>`},
		{"LAY-05", "app.css", appCSS, `small{font-size:.875rem}`},
		{"LAY-08", "app.js", appJS, `a.closest('.nav-more')?.classList.add('has-current')`},
		{"LAY-08", "app.css", appCSS, `.nav-more.has-current .nav-more-toggle{background:var(--brand-primary)`},
		{"LAY-11", "app.css", appCSS, `.hint a{display:inline-flex;align-items:center;min-height:44px}`},
	}
	for _, c := range checks {
		if !strings.Contains(c.text, c.want) {
			t.Errorf("%s: %s sem %q", c.id, c.where, c.want)
		}
	}
	// LAY-05: a barra inferior não desce de 12 px em tela estreita.
	if strings.Contains(appCSS, "font-size:.6875rem") {
		t.Error("LAY-05: app.css ainda tem texto de 11 px (.6875rem)")
	}
	// LAY-12: texto de interface sem ponto de exclamação no slogan.
	for _, name := range []string{"login.html", "base.html"} {
		if strings.Contains(read("templates", name), "todo dia!") {
			t.Errorf("LAY-12: %s ainda mostra o slogan com exclamação", name)
		}
	}
}

// CAT-06: quem só lê o catálogo não vê o botão de criar produto.
func TestQAProductsPageHidesCreateForReadOnlyProfile(t *testing.T) {
	a, _, _, _ := newStockTestApp(t)
	createTestUser(t, a, "qa-cat-dir", "secret", "Diretor", true)
	createTestUser(t, a, "qa-cat-reader", "secret", "Estoquista", true)
	page := func(login string) string {
		t.Helper()
		ld := doLogin(t, a, login, "secret")
		req := httptest.NewRequest(http.MethodGet, "/products", nil)
		req.AddCookie(ld.Cookie)
		rr := httptest.NewRecorder()
		newHTTPServer("127.0.0.1:0", a).Handler.ServeHTTP(rr, req)
		if rr.Code != http.StatusOK {
			t.Fatalf("%s /products=%d", login, rr.Code)
		}
		return rr.Body.String()
	}
	if !strings.Contains(page("qa-cat-dir"), `id="productNew"`) {
		t.Fatal("Diretor sem o botão + Novo produto")
	}
	if strings.Contains(page("qa-cat-reader"), `id="productNew"`) {
		t.Fatal("perfil só de leitura vê o botão + Novo produto")
	}
}
