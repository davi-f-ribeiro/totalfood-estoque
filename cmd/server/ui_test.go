package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestUIRoutesAndEscaping(t *testing.T) {
	a, _, _, _ := newStockTestApp(t)
	createTestUser(t, a, "dirui", "secret", "Diretor", true)
	createTestUser(t, a, "noui", "secret", "Vendas", true)

	rr := httptest.NewRecorder()
	newHTTPServer("127.0.0.1:0", a).Handler.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/login", nil))
	if rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), "Estoque") {
		t.Fatalf("login page status=%d", rr.Code)
	}
	rr = httptest.NewRecorder()
	newHTTPServer("127.0.0.1:0", a).Handler.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/app", nil))
	if rr.Code != http.StatusSeeOther || rr.Header().Get("Location") != "/stock" {
		t.Fatalf("app redirect status=%d loc=%s", rr.Code, rr.Header().Get("Location"))
	}

	rr = httptest.NewRecorder()
	newHTTPServer("127.0.0.1:0", a).Handler.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/stock", nil))
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("unauth stock status=%d", rr.Code)
	}

	dir := doLogin(t, a, "dirui", "secret")
	req := httptest.NewRequest(http.MethodGet, "/stock", nil)
	req.AddCookie(dir.Cookie)
	rr = httptest.NewRecorder()
	newHTTPServer("127.0.0.1:0", a).Handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), "csrf-token") {
		t.Fatalf("director stock status=%d", rr.Code)
	}
	if !strings.Contains(rr.Header().Get("Content-Security-Policy"), "frame-ancestors 'none'") {
		t.Fatalf("missing CSP")
	}

	no := doLogin(t, a, "noui", "secret")
	req = httptest.NewRequest(http.MethodGet, "/stock", nil)
	req.AddCookie(no.Cookie)
	rr = httptest.NewRecorder()
	newHTTPServer("127.0.0.1:0", a).Handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusSeeOther || rr.Header().Get("Location") != "/no-access" {
		t.Fatalf("no permission status=%d loc=%s", rr.Code, rr.Header().Get("Location"))
	}

	ctx := context.Background()
	var cat int64
	if err := a.db.QueryRowContext(ctx, `SELECT id FROM categories WHERE name='Alimentos'`).Scan(&cat); err != nil {
		t.Fatal(err)
	}
	var item int64
	if err := a.db.QueryRowContext(ctx, `INSERT INTO inventory_items (name, category_id, unit_code) VALUES ($1,$2,'UN') RETURNING id`, `<b>Unsafe</b>`, cat).Scan(&item); err != nil {
		t.Fatal(err)
	}
	if _, err := a.db.ExecContext(ctx, `INSERT INTO product_skus (item_id, brand, barcode) VALUES ($1,$2,$3)`, item, `<script>x</script>`, "unsafe-code"); err != nil {
		t.Fatal(err)
	}
	req = httptest.NewRequest(http.MethodGet, "/api/products/lookup?q=Unsafe", nil)
	req.AddCookie(dir.Cookie)
	rr = httptest.NewRecorder()
	newHTTPServer("127.0.0.1:0", a).Handler.ServeHTTP(rr, req)
	if strings.Contains(rr.Body.String(), "<script>") {
		t.Fatalf("unescaped script in JSON response")
	}
}

func TestAllMigrationsForUI(t *testing.T) {
	db := openTestDatabase(t)
	if err := runMigrations(context.Background(), db, filepath.Join("..", "..", "migrations")); err != nil {
		t.Fatal(err)
	}
	assertCount(t, db, `SELECT count(*) FROM permissions WHERE resource='stock' AND action='read'`, 1)
}

func TestOperationalUIScannerAccordionAndContext(t *testing.T) {
	jsb, err := os.ReadFile(filepath.Join("static", "app.js"))
	if err != nil {
		t.Fatal(err)
	}
	hb, err := os.ReadFile(filepath.Join("templates", "movement.html"))
	if err != nil {
		t.Fatal(err)
	}
	combined := string(hb) + string(jsb)
	checks := []string{
		`id="scanProduct"`,
		`Ler código de barras`,
		`Fechar`,
		`Código de barras não cadastrado.`,
		`navigator.mediaDevices.getUserMedia`,
		`function cameraSupported(){return !!(navigator.mediaDevices&&navigator.mediaDevices.getUserMedia)}`,
		`id="scanner-video" autoplay muted playsinline webkit-playsinline`,
		`video.defaultMuted=true`,
		`video.setAttribute('webkit-playsinline','')`,
		`scannerConstraints(fallback=false)`,
		`width:{ideal:1280}`,
		`height:{ideal:720}`,
		`OverconstrainedError`,
		`fallback=video_true`,
		`decodeFromStream`,
		`stage='+stage`,
		`Não foi possível iniciar a visualização da câmera. Feche o leitor e tente novamente.`,
		`SCANNER: `,
		`BrowserMultiFormatReader`,
		`BarcodeDetector`,
		`CODE_128`,
		`UPC_E`,
		`getTracks().forEach`,
		`stopScanner`,
		`cancelAnimationFrame`,
		`video.pause()`,
		`Permissão da câmera negada. Libere o acesso à câmera nas configurações do navegador.`,
		`Nenhuma câmera disponível.`,
		`Câmera não suportada neste navegador.`,
		`Código identificado`,
		`Selecione um produto`,
		`Disponível:`,
		`aria-expanded`,
		`openItem===id`,
		`stopPropagation`,
		`warehouse_id`,
		`Quantidade acima do saldo disponível.`,
		`Destino deve ser diferente da origem.`,
		`Saldo atualizado`,
	}
	for _, want := range checks {
		if !strings.Contains(combined, want) {
			t.Fatalf("operational UI missing %q", want)
		}
	}

	for _, forbidden := range []string{`📷`, `Leitura por câmera não disponível`} {
		if strings.Contains(combined, forbidden) {
			t.Fatalf("operational UI must not contain %q", forbidden)
		}
	}
}

func TestMovementProductSelectionUXStateContracts(t *testing.T) {
	jsb, err := os.ReadFile(filepath.Join("static", "app.js"))
	if err != nil {
		t.Fatal(err)
	}
	hb, err := os.ReadFile(filepath.Join("templates", "movement.html"))
	if err != nil {
		t.Fatal(err)
	}
	combined := string(hb) + string(jsb)
	checks := []string{
		`id="productFeedback"`,
		`Verificando código...`,
		`EAN: `,
		`Tentar novamente`,
		`manualSeq++`,
		`if(seq!==manualSeq)return`,
		`lookup.hidden=true`,
		`normalizeBarcode(q).length<2`,
		`function skuLabel`,
		`Produto selecionado`,
		`id="changeProduct"`,
		`productMsg('error','<strong>Código de barras não cadastrado.</strong>`,
		`scanBtn?.addEventListener('scanner:start'`,
		`function requestDecimal(v)`,
		`quantity:requestDecimal(qty.value)`,
		`unit_cost:requestDecimal(cost.value)`,
		`if(err.status===400||err.status===404||err.status===409)`,
		`movementMsg('error',movementErrorMessage(err))`,
		`id="status" class="operation-feedback"`,
	}
	for _, want := range checks {
		if !strings.Contains(combined, want) {
			t.Fatalf("product selection UX missing %q", want)
		}
	}
}


func TestWarehouseAdminUIContracts(t *testing.T) {
	htmlb, err := os.ReadFile(filepath.Join("templates", "admin.html"))
	if err != nil {
		t.Fatal(err)
	}
	jsb, err := os.ReadFile(filepath.Join("static", "app.js"))
	if err != nil {
		t.Fatal(err)
	}
	cssb, err := os.ReadFile(filepath.Join("static", "app.css"))
	if err != nil {
		t.Fatal(err)
	}
	combined := string(htmlb) + string(jsb) + string(cssb)
	checks := []string{
		"Depósitos",
		"O nome é único e permanente para preservar a rastreabilidade.",
		"id=\"adminWarehouseStatus\" class=\"operation-feedback\" role=\"status\" aria-live=\"polite\" hidden",
		"Vazio",
		"SKU",
		"action.disabled=true",
		"submit.disabled=true",
		"Não foi possível carregar os depósitos.",
		"Este depósito está inativo. Atualize a página e selecione um depósito ativo.",
		".admin{display:grid;gap:16px}",
		".warehouse-row button{min-width:112px}",
	}
	for _, want := range checks {
		if !strings.Contains(combined, want) {
			t.Fatalf("warehouse admin UI missing %q", want)
		}
	}
	if strings.Contains(combined, "confirm('Desativar este depósito?") {
		t.Fatal("warehouse deactivation must use inline context/feedback, not a redundant modal")
	}
}


func TestProjectWideResponsiveUXAndFeedbackContracts(t *testing.T) {
	read := func(name string) string {
		t.Helper()
		b, err := os.ReadFile(filepath.Join("templates", name))
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	jsb, err := os.ReadFile(filepath.Join("static", "app.js"))
	if err != nil {
		t.Fatal(err)
	}
	cssb, err := os.ReadFile(filepath.Join("static", "app.css"))
	if err != nil {
		t.Fatal(err)
	}
	js := string(jsb)
	css := string(cssb)

	for _, forbidden := range []string{
		"confirm(",
		"alert(",
		".textContent=err.message",
		".textContent=e.message",
	} {
		if strings.Contains(js, forbidden) {
			t.Fatalf("project UI reintroduced forbidden error/modal pattern %q", forbidden)
		}
	}

	for _, want := range []string{
		"function setFeedback(",
		"function uiErrorMessage(",
		"Sem conexão com o sistema.",
		"Você não tem permissão para realizar esta ação.",
		"O sistema não conseguiu concluir a operação.",
		"list-action",
	} {
		if !strings.Contains(js+css, want) {
			t.Fatalf("project UI missing shared UX contract %q", want)
		}
	}

	feedback := map[string][]string{
		"stock.html":           {"stockStatus"},
		"movement.html":        {"status"},
		"catalog.html":         {"catalogStatus"},
		"fiscal.html":          {"fiscalStatus", "fiscalConfirmStatus"},
		"assets.html":          {"assetLookupStatus", "assetOpStatus", "assetTypeStatus", "assetUnitStatusMsg"},
		"inventory.html":       {"inventoryListMessage", "inventoryMessage"},
		"admin.html":           {"adminWarehouseStatus", "adminUserStatus", "adminProfileStatus", "adminPermStatus"},
		"change_password.html": {"forcedPasswordStatus"},
	}
	for file, ids := range feedback {
		body := read(file)
		for _, id := range ids {
			needle := `id="` + id + `"`
			pos := strings.Index(body, needle)
			if pos < 0 {
				t.Fatalf("%s missing feedback id %s", file, id)
			}
			end := pos + 220
			if end > len(body) {
				end = len(body)
			}
			fragment := body[pos:end]
			if !strings.Contains(fragment, "operation-feedback") || !strings.Contains(fragment, "aria-live=") {
				t.Fatalf("%s feedback %s is not standardized: %s", file, id, fragment)
			}
		}
	}

	inventory := read("inventory.html")
	for _, forbidden := range []string{"warehouse escolhido", ">Criar TOTAL<", ">Criar BLIND<"} {
		if strings.Contains(inventory, forbidden) {
			t.Fatalf("inventory UI exposes internal terminology %q", forbidden)
		}
	}
	for _, want := range []string{"Depósito ativo", "Criar inventário total", "Criar inventário cego"} {
		if !strings.Contains(inventory, want) {
			t.Fatalf("inventory UI missing business terminology %q", want)
		}
	}

	for _, want := range []string{
		"@media(min-width:1024px)",
		"@media(min-width:768px) and (max-width:1023px)",
		"@media(max-width:767px)",
		".assets{grid-template-columns:repeat(2,minmax(0,1fr))",
		".admin{grid-template-columns:repeat(2,minmax(0,1fr))",
		".stock-shell #stockList{display:grid;grid-template-columns:repeat(2,minmax(0,1fr))",
		".assets,.admin,.stock-shell,.fiscal-shell{grid-template-columns:1fr",
	} {
		if !strings.Contains(css, want) {
			t.Fatalf("responsive UX missing %q", want)
		}
	}
}


func TestOperationalGuardrailsAgainstHumanError(t *testing.T) {
	jsb, err := os.ReadFile(filepath.Join("static", "app.js"))
	if err != nil {
		t.Fatal(err)
	}
	cssb, err := os.ReadFile(filepath.Join("static", "app.css"))
	if err != nil {
		t.Fatal(err)
	}
	assetsb, err := os.ReadFile(filepath.Join("templates", "assets.html"))
	if err != nil {
		t.Fatal(err)
	}
	adminb, err := os.ReadFile(filepath.Join("templates", "admin.html"))
	if err != nil {
		t.Fatal(err)
	}
	fiscalb, err := os.ReadFile(filepath.Join("templates", "fiscal.html"))
	if err != nil {
		t.Fatal(err)
	}
	js := string(jsb)
	combined := js + string(cssb) + string(assetsb) + string(adminb) + string(fiscalb)

	for _, want := range []string{
		"async function runLocked(control,fn)",
		"function requireFields(form,feedback",
		"dataset.busy==='1'",
		"submit.disabled=true",
		"reportValidity()",
		`id="assetMoveWarehouse" required`,
		`id="assetNewStatus" required`,
		`id="assetUnitWarehouse" required`,
		`id="fiscalWarehouse" required`,
		"Ao baixar, a localização atual será removida",
		"todos os usuários deste perfil terão as sessões atuais encerradas",
		".impact-warning{",
		"Selecione um depósito.",
	} {
		if !strings.Contains(combined, want) {
			t.Fatalf("operational guardrail missing %q", want)
		}
	}

	for _, forbidden := range []string{"confirm(", "alert("} {
		if strings.Contains(js, forbidden) {
			t.Fatalf("guardrails must remain inline, found %q", forbidden)
		}
	}
}


func TestCatalogResponsiveGestaltAndAuditNavigation(t *testing.T) {
	read := func(name string) string {
		t.Helper()
		b, err := os.ReadFile(filepath.Join("templates", name))
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	files := []string{"base.html", "stock.html", "movement.html", "catalog.html", "fiscal.html", "assets.html", "inventory.html", "admin.html"}
	for _, name := range files {
		body := read(name)
		if strings.Contains(body, "Validação de Estoque") {
			t.Fatalf("%s reintroduced obsolete inventory label", name)
		}
		if !strings.Contains(body, "Auditoria de Estoque") {
			t.Fatalf("%s missing Auditoria de Estoque label", name)
		}
	}
	for _, name := range []string{"stock.html", "movement.html", "catalog.html", "fiscal.html", "assets.html", "inventory.html", "admin.html"} {
		body := read(name)
		audit := strings.Index(body, `<a href="/inventory"`)
		admin := strings.Index(body, `<a href="/admin"`)
		if audit < 0 || admin < 0 || audit > admin {
			t.Fatalf("%s must place Auditoria de Estoque before Administração", name)
		}
	}
	catalog := read("catalog.html")
	if strings.Index(catalog, `id="catalogEmpty"`) > strings.Index(catalog, `id="catalogList"`) {
		t.Fatal("catalog empty guidance must stay grouped with the parent-list heading before the list")
	}
	cssb, err := os.ReadFile(filepath.Join("static", "app.css"))
	if err != nil {
		t.Fatal(err)
	}
	css := string(cssb)
	for _, want := range []string{
		".catalog-parent-card{justify-content:stretch;justify-items:start",
		".catalog-empty{padding:14px;text-align:left",
		".catalog-parent-list{max-height:none;overflow:visible",
		".catalog-manager{padding:0;border:0;border-radius:0;box-shadow:none",
	} {
		if !strings.Contains(css, want) {
			t.Fatalf("catalog responsive/Gestalt contract missing %q", want)
		}
	}
}


func TestCRUDFormsExposeCreateUpdateStateAndResetAfterSave(t *testing.T) {
	read := func(path string) string {
		t.Helper()
		b, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	js := read(filepath.Join("static", "app.js"))
	catalog := read(filepath.Join("templates", "catalog.html"))
	assets := read(filepath.Join("templates", "assets.html"))
	admin := read(filepath.Join("templates", "admin.html"))

	for _, want := range []string{
		"function setCrudMode(form,editing,createLabel,updateLabel)",
		"setCrudMode(itemForm,true,'Criar item pai','Atualizar item pai')",
		"setCrudMode(itemForm,false,'Criar item pai','Atualizar item pai')",
		"setCrudMode(skuForm,true,'Criar SKU','Atualizar SKU')",
		"setCrudMode(typeForm,true,'Criar tipo','Atualizar tipo')",
		"setCrudMode(unitForm,true,'Criar unidade','Atualizar unidade')",
		"setCrudMode(form,true,'Criar usuário','Atualizar usuário')",
		"true,'Criar perfil','Atualizar perfil'",
		"Formulário pronto para um novo cadastro.",
		"itemForm.reset()",
		"typeForm.reset()",
		"unitForm.reset()",
		"form.reset()",
	} {
		if !strings.Contains(js, want) {
			t.Fatalf("CRUD safety state missing %q", want)
		}
	}
	for _, want := range []string{"Criar item pai", "Criar SKU"} {
		if !strings.Contains(catalog, want) {
			t.Fatalf("catalog initial create mode missing %q", want)
		}
	}
	for _, want := range []string{"Criar tipo", "Criar unidade"} {
		if !strings.Contains(assets, want) {
			t.Fatalf("assets initial create mode missing %q", want)
		}
	}
	for _, want := range []string{"Criar usuário", "Criar perfil"} {
		if !strings.Contains(admin, want) {
			t.Fatalf("admin initial create mode missing %q", want)
		}
	}
}


func TestMovementSafetyAgainstPhantomReplayAndResidualState(t *testing.T) {
	jsb, err := os.ReadFile(filepath.Join("static", "app.js"))
	if err != nil {
		t.Fatal(err)
	}
	htmlb, err := os.ReadFile(filepath.Join("templates", "movement.html"))
	if err != nil {
		t.Fatal(err)
	}
	js, html := string(jsb), string(htmlb)

	for _, forbidden := range []string{
		"requestSubmit()",
		"window.addEventListener('online'",
	} {
		if strings.Contains(js, forbidden) {
			t.Fatalf("movement safety forbids automatic submission %q", forbidden)
		}
	}

	for _, want := range []string{
		`id="movementPending"`,
		"Verificar resultado",
		"Reenviar a mesma operação",
		"function readPending()",
		"function savePending(p)",
		"function resetMovementForm()",
		"function verifyPending()",
		"async function retryPending()",
		"Existe uma operação pendente",
		"Formulário pronto para uma nova movimentação.",
		"sessionStorage.getItem('pendingOp')",
		"sessionStorage.removeItem('pendingOp')",
		"await api('/api/operations/'+encodeURIComponent(p.body.operation_id))",
		"await api(p.url,{method:'POST',body:JSON.stringify(p.body)})",
		"if(readPending()){showPending(readPending(),false)",
		"document.getElementById('skuId').value=''",
		"history.replaceState(null,'',location.pathname)",
		"cost.required=isEntry",
	} {
		if !strings.Contains(js+html, want) {
			t.Fatalf("movement safety contract missing %q", want)
		}
	}

	savePos := strings.Index(js, "savePending(payload)")
	sendPos := strings.Index(js, "await api(payload.url,{method:'POST'")
	if savePos < 0 || sendPos < 0 || savePos > sendPos {
		t.Fatal("movement must persist operation_id before first mutation request")
	}
	verifyPos := strings.Index(js, "let state=await verifyPending()")
	retryPos := strings.Index(js, "await api(p.url,{method:'POST'")
	if verifyPos < 0 || retryPos < 0 || verifyPos > retryPos {
		t.Fatal("pending movement must be verified before explicit resend")
	}
	validatePos := strings.Index(js, "if(!validateMovement())return;submit.dataset.busy='1'")
	if validatePos < 0 {
		t.Fatal("movement validation must happen before locking the submit control")
	}
}

func TestAdminSafetyAtomicUserUpdateAndExplicitPermissionTarget(t *testing.T) {
	jsb, err := os.ReadFile(filepath.Join("static", "app.js"))
	if err != nil {
		t.Fatal(err)
	}
	htmlb, err := os.ReadFile(filepath.Join("templates", "admin.html"))
	if err != nil {
		t.Fatal(err)
	}
	js, html := string(jsb), string(htmlb)

	for _, forbidden := range []string{
		"if(pass)await api('/api/admin/users/'+id+'/password'",
		"if(!selectedProfile&&profiles[0])selectedProfile=profiles[0].id",
	} {
		if strings.Contains(js, forbidden) {
			t.Fatalf("admin safety reintroduced unsafe flow %q", forbidden)
		}
	}
	for _, want := range []string{
		"if(pass)body.password=pass",
		"out=await api('/api/admin/users/'+id,{method:'PATCH'",
		"if(out?.reauth_required){location.href='/login';return}",
		"selectedProfile=0",
		"permSave.disabled=true",
		"permSave.disabled=false",
		"Nenhum perfil selecionado. Selecione um perfil na lista acima.",
		"Perfil selecionado: ",
		"Selecione explicitamente um perfil antes de salvar as permissões.",
		"Selecione novamente um perfil para realizar outra alteração.",
		"id=\"adminSavePerms\" type=\"button\" disabled",
		"id=\"adminPermSelection\"",
		"Obrigatória ao criar. Ao editar, deixe em branco para manter a senha atual.",
	} {
		if !strings.Contains(js+html, want) {
			t.Fatalf("admin safety contract missing %q", want)
		}
	}
}
