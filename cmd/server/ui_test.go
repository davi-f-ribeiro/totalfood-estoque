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
	if rr.Code != http.StatusSeeOther || rr.Header().Get("Location") != "/login" {
		t.Fatalf("unauthenticated app redirect status=%d loc=%s", rr.Code, rr.Header().Get("Location"))
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
		`openItem===row.item_id`,
		`warehouse_id`,
		`Quantidade acima do saldo disponível.`,
		`Destino deve ser diferente da origem.`,
		`function movementConfirmed(msg)`,
		`registrada.`,
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

func TestBetterInterfaceOperationalContracts(t *testing.T) {
	jsb, err := os.ReadFile(filepath.Join("static", "app.js")); if err != nil { t.Fatal(err) }
	cssb, err := os.ReadFile(filepath.Join("static", "app.css")); if err != nil { t.Fatal(err) }
	hb, err := os.ReadFile(filepath.Join("templates", "movement.html")); if err != nil { t.Fatal(err) }
	combined := string(jsb)+string(cssb)+string(hb)
	for _, want := range []string{
		`prefers-reduced-motion: no-preference`, `function scrollToContext(`,
		`Nenhum item com saldo neste depósito.`, `Abaixo do estoque mínimo`, `className='stock-expand'`,
		`--focus-ring:`, `body{font-size:16px}`,
	} {
		if !strings.Contains(combined,want) { t.Fatalf("better-interface contract missing %q",want) }
	}
	for _, forbidden := range []string{
		`detail.scrollIntoView({block:'nearest',behavior:'smooth'})`,
		`q.textContent=displayDecimal(r.quantity)+' '+r.unit+' ▼'`,
		`body{font-size:15px}`,
		`role="combobox"`, `role="listbox"`, `b.setAttribute('role','option')`,
	} {
		if strings.Contains(combined,forbidden) { t.Fatalf("better-interface regression %q",forbidden) }
	}
}

func TestStockDetailFollowsSelectedItemContext(t *testing.T) {
	jsb, err := os.ReadFile(filepath.Join("static", "app.js"))
	if err != nil { t.Fatal(err) }
	cssb, err := os.ReadFile(filepath.Join("static", "app.css"))
	if err != nil { t.Fatal(err) }
	js, css := string(jsb), string(cssb)
	for _, want := range []string{
		`let openItem=0,detailSeq=0`,
		`/api/stock/detail/'+row.item_id+qs`,
		`sum.textContent=x.brand+' — '+displayDecimal(x.quantity)+' '+x.unit`,
		`/api/stock/detail/'+row.item_id+'?brand_id='+x.brand_id`,
		`1 un = '+displayDecimal(p.content_quantity||'1',4)+' '+bd.unit`,
		`card.after(detail)`,
		`scrollToContext(detail)`,
		`.stock-shell #stockDetail{grid-column:1/-1}`,
	} {
		if !strings.Contains(js+css,want) { t.Fatalf("parent-first stock contextual detail missing %q",want) }
	}
	for _, forbidden := range []string{
		`row.item_id+':'+row.brand_id`,
		`r.name+' - '+r.brand`,
		"Composição do saldo",
	} {
		if strings.Contains(js,forbidden) { t.Fatalf("stock UI flattened or duplicated parent hierarchy with %q",forbidden) }
	}
}

func TestUnknownEntryBarcodeContinuesThroughExistingCatalogSKUFlow(t *testing.T) {
	appb, err := os.ReadFile(filepath.Join("static","app.js")); if err != nil { t.Fatal(err) }
	pb, err := os.ReadFile(filepath.Join("static","products.js")); if err != nil { t.Fatal(err) }
	htmlb, err := os.ReadFile(filepath.Join("templates","products.html")); if err != nil { t.Fatal(err) }
	combined:=string(appb)+string(pb)+string(htmlb)
	for _, want := range []string{
		`Cadastrar produto`,
		`/products?barcode=`,
		`return_to=%2Fentry`,
		`params.get('return_to')`,
		`body={item_id:+parent.value,brand_id:+brand.value,content_quantity:`,
		`sku_id:String(out.id)`,
		`location.href='/entry?'`,
		`id="productBarcode"`,
	} {
		if !strings.Contains(combined,want) { t.Fatalf("Product/Item continuation missing %q",want) }
	}
	if strings.Contains(combined,"/api/catalog/skus/from-entry") { t.Fatal("entry handoff must reuse Product/Item endpoint") }
}

func TestCatalogSKUCreateLocksSelectedParent(t *testing.T) {
	pb, err := os.ReadFile(filepath.Join("static","products.js")); if err != nil { t.Fatal(err) }
	hb, err := os.ReadFile(filepath.Join("templates","products.html")); if err != nil { t.Fatal(err) }
	combined:=string(pb)+string(hb)
	for _, want := range []string{
		`id="productParent"`,`id="productBrand"`,`id="productName"`,`id="productContent"`,`id="productBarcode"`,
		`parent.disabled=!!x.has_history`,`brand.disabled=!!x.has_history`,
		`Identificação específica do produto, por exemplo Original, Branco ou Oreo.`,
		`name:name.value.trim()`,
	} {
		if !strings.Contains(combined,want) { t.Fatalf("Product/Item identity UI missing %q",want) }
	}
	if strings.Contains(combined,`O nome é gerado automaticamente.`) {
		t.Fatal("Product/Item identity must not be overwritten by an automatically generated name")
	}
}

func TestCatalogIgnoresStaleParentAndSKUResponses(t *testing.T) {
	pb, err := os.ReadFile(filepath.Join("static","products.js")); if err != nil { t.Fatal(err) }
	js:=string(pb)
	for _, want := range []string{`editing=x`,`body.version=editing.version`,`await load()`,`form.hidden=true`} {
		if !strings.Contains(js,want) { t.Fatalf("Product/Item edit safety missing %q",want) }
	}
}

func TestCatalogSKUFeedbackStaysInLocalContext(t *testing.T) {
	pb, err := os.ReadFile(filepath.Join("static","products.js")); if err != nil { t.Fatal(err) }
	hb, err := os.ReadFile(filepath.Join("templates","products.html")); if err != nil { t.Fatal(err) }
	combined:=string(pb)+string(hb)
	for _, want := range []string{
		`id="productStatus" class="operation-feedback" role="status" aria-live="polite" hidden`,
		`function msg(type,text){setFeedback(status,type,text)}`,
		`msg('loading',(editing?'Atualizando ':'Criando ')+label+'…')`,
		`msg('success',label+(wasEditing?' atualizado.':' criado.'))`,
	} {
		if !strings.Contains(combined,want) { t.Fatalf("Product/Item feedback missing %q",want) }
	}
}

func TestFrontendCleanupKeepsSingleCanonicalHelpers(t *testing.T) {
	jsb, err := os.ReadFile(filepath.Join("static", "app.js"))
	if err != nil {
		t.Fatal(err)
	}
	js := string(jsb)
	for _, forbidden := range []string{
		`function pickMovementProduct(`,
		`function stopScan(`,
		`async function loadWarehouses(){let s=document.querySelectorAll('select[id^="warehouse"]')`,
	} {
		if strings.Contains(js, forbidden) {
			t.Fatalf("dead or competing frontend helper reintroduced %q", forbidden)
		}
	}
	if strings.Count(js, `async function loadWarehouses(includeGeneral=false,endpoint='/api/warehouses')`) != 1 {
		t.Fatal("operational warehouse loader must have one canonical global implementation")
	}
}

func TestWarehouseAdminUIContracts(t *testing.T) {
	htmlb, err := os.ReadFile(filepath.Join("templates", "admin.html"))
	if err != nil { t.Fatal(err) }
	jsb, err := os.ReadFile(filepath.Join("static", "app.js"))
	if err != nil { t.Fatal(err) }
	cssb, err := os.ReadFile(filepath.Join("static", "app.css"))
	if err != nil { t.Fatal(err) }
	combined := string(htmlb) + string(jsb) + string(cssb)
	for _, want := range []string{
		"Depósitos cadastrados",
		"O nome é único e permanente para preservar a rastreabilidade.",
		"id=\"adminWarehouseStatus\" class=\"operation-feedback\"",
		"Vazio",
		"Não foi possível carregar os depósitos.",
		".admin-master-detail{display:grid;grid-template-columns:minmax(260px,340px) minmax(0,1fr)",
	} {
		if !strings.Contains(combined, want) { t.Fatalf("warehouse admin UI missing %q", want) }
	}
	if strings.Contains(combined, "warehouse_not_empty") || strings.Contains(combined, "warehouse name already exists") {
		t.Fatal("warehouse admin must not expose technical domain codes")
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
		"Formulário pronto para um novo cadastro.",
		"Formulário pronto para uma nova movimentação.",
		"Nota fiscal já estava confirmada. Formulário pronto para uma nova nota.",
		"O sistema não conseguiu concluir a operação.",
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
		"A operação não pôde ser concluída.",
		"list-action",
	} {
		if !strings.Contains(js+css, want) {
			t.Fatalf("project UI missing shared UX contract %q", want)
		}
	}

	feedback := map[string][]string{
		"stock.html":           {"stockStatus"},
		"movement.html":        {"status"},
		"catalog.html":         {"masterStatus"},
		"products.html":        {"productStatus"},
		"fiscal.html":          {"fiscalStatus", "fiscalConfirmStatus"},
		"assets.html":          {"assetLookupStatus", "assetOpStatus", "assetTypeStatus", "assetUnitStatusMsg"},
		"inventory.html":       {"inventoryCreateMessage", "inventoryListMessage", "inventoryMessage"},
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
		".admin-master-detail{display:grid;grid-template-columns:minmax(260px,340px) minmax(0,1fr)",
		".stock-shell #stockList{display:grid;grid-template-columns:repeat(2,minmax(0,1fr))",
		".admin-master-detail{grid-template-columns:1fr;gap:18px",
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
		"reportValidity()",
		`id="assetMoveWarehouse" required`,
		`id="assetNewStatus" required`,
		`id="assetUnitWarehouse" required`,
		`id="fiscalWarehouse" required`,
		"Ao baixar, a localização atual será removida",
		"as sessões atuais dos usuários deste perfil serão encerradas",
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
	baseb, err := os.ReadFile(filepath.Join("templates", "base.html"))
	if err != nil { t.Fatal(err) }
	base := string(baseb)
	if strings.Count(base, `{{define "nav"}}`) != 1 {
		t.Fatal("navigation must have one canonical template")
	}
	for _, want := range []string{
		`{{if .CanStock}}<a href="/stock">Estoque</a>{{end}}`,
		`{{if .CanEntry}}<a href="/entry">Entrada</a>{{end}}`,
		`{{if .CanExit}}<a href="/exit">Saída</a>{{end}}`,
		`{{if .CanTransfer}}<a href="/transfer">Transferir</a>{{end}}`,
		`{{if .CanReports}}<a href="/reports">Relatórios</a>{{end}}`,
		`{{if .CanCatalog}}<a href="/catalog">Catálogo</a><a href="/products">Produto/Item</a>{{end}}`,
		`{{if .CanAssets}}<a href="/assets">Patrimônio</a>{{end}}`,
		`{{if .CanInventory}}<a href="/inventory">Auditoria de Estoque</a>{{end}}`,
		`{{if .CanAdmin}}<a href="/admin">Administração</a>{{end}}`,
	} {
		if !strings.Contains(base, want) { t.Fatalf("canonical navigation missing %q", want) }
	}
	for _, name := range []string{"stock.html","movement.html","catalog.html","products.html","fiscal.html","assets.html","inventory.html","admin.html","reports.html"} {
		b, err := os.ReadFile(filepath.Join("templates", name)); if err != nil { t.Fatal(err) }
		html := string(b)
		if !strings.Contains(html, `{{template "nav" .}}`) { t.Fatalf("%s must use canonical navigation", name) }
		if strings.Contains(html, `<nav class="nav">`) { t.Fatalf("%s reintroduced a competing navigation", name) }
	}
	catalogb, err := os.ReadFile(filepath.Join("templates", "catalog.html")); if err != nil { t.Fatal(err) }
	catalog := string(catalogb)
	if !strings.Contains(catalog,"Cadastros mestres de item pai e marca.") || strings.Contains(catalog,"Produtos/SKUs vinculados") {
		t.Fatal("catalog must contain only item and brand masters")
	}
}

func TestCRUDFormsExposeCreateUpdateStateAndResetAfterSave(t *testing.T) {
	read := func(path string) string {
		t.Helper()
		b, err := os.ReadFile(path)
		if err != nil { t.Fatal(err) }
		return string(b)
	}
	app := read(filepath.Join("static","app.js"))
	masters := read(filepath.Join("static","catalog_masters.js"))
	products := read(filepath.Join("static","products.js"))
	catalog := read(filepath.Join("templates","catalog.html"))
	productHTML := read(filepath.Join("templates","products.html"))
	assets := read(filepath.Join("templates","assets.html"))
	admin := read(filepath.Join("templates","admin.html"))

	for _, want := range []string{
		"function setCrudMode(form,editing,createLabel,updateLabel)",
		"itemForm.reset()","brandForm.reset()","form.reset()",
		"body.version=itemVersion","body.version=brandVersion","body.version=editing.version",
		"msg('success',label+(wasEditing?' atualizado.':' criado.'))",
		"setCrudMode(typeForm,true,'Criar tipo','Atualizar tipo')",
		"function setAssetUnitEditMode(editing)",
		"setCrudMode(userForm,true,'Criar usuário','Atualizar usuário')",
	} {
		if !strings.Contains(app+masters+products,want) {
			t.Fatalf("CRUD safety state missing %q",want)
		}
	}
	for _, want := range []string{"Novo item","Nova marca"} {
		if !strings.Contains(catalog,want) { t.Fatalf("catalog create mode missing %q",want) }
	}
	if !strings.Contains(productHTML,"Novo Produto/Item") { t.Fatal("Product/Item create mode missing") }
	for _, want := range []string{"Criar tipo","Criar unidade"} {
		if !strings.Contains(assets,want) { t.Fatalf("assets initial create mode missing %q",want) }
	}
	for _, want := range []string{"Criar usuário","Criar perfil"} {
		if !strings.Contains(admin,want) { t.Fatalf("admin initial create mode missing %q",want) }
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
		"A movimentação pendente já estava confirmada.",
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
	jsb, err := os.ReadFile(filepath.Join("static", "app.js")); if err != nil { t.Fatal(err) }
	htmlb, err := os.ReadFile(filepath.Join("templates", "admin.html")); if err != nil { t.Fatal(err) }
	js, html := string(jsb), string(htmlb)
	for _, forbidden := range []string{
		"if(pass)await api('/api/admin/users/'+id+'/password'",
		"if(!selectedProfile&&profiles[0])selectedProfile=profiles[0].id",
		"marque somente permissões reais existentes no banco",
	} {
		if strings.Contains(js+html, forbidden) { t.Fatalf("admin safety reintroduced unsafe flow %q", forbidden) }
	}
	for _, want := range []string{
		"if(pass)body.password=pass",
		"out=await api('/api/admin/users/'+id,{method:'PATCH'",
		"selectedProfile=0",
		"Nenhum perfil selecionado. Selecione um perfil na lista ao lado.",
		"Perfil selecionado: ",
		"Selecione explicitamente um perfil antes de salvar os acessos.",
		`id="adminSavePerms" type="button" disabled>Salvar acessos`,
		"Obrigatória ao criar. Ao editar, deixe em branco para manter a senha atual.",
		"/api/admin/profile-options",
		"function permissionActionLabel(resource,action)",
		"Perfis e acessos",
	} {
		if !strings.Contains(js+html, want) { t.Fatalf("admin safety contract missing %q", want) }
	}
}

func TestAssetEditUsesAuditedOperationalFlowsOnly(t *testing.T) {
	jsb, err := os.ReadFile(filepath.Join("static", "app.js"))
	if err != nil { t.Fatal(err) }
	htmlb, err := os.ReadFile(filepath.Join("templates", "assets.html"))
	if err != nil { t.Fatal(err) }
	js, html := string(jsb), string(htmlb)

	checks := []string{
		"function setAssetUnitEditMode(editing)",
		"unitStatus.disabled=editing",
		"unitWarehouse.disabled=editing",
		"setAssetUnitEditMode(true)",
		"setAssetUnitEditMode(false)",
		"let body=id?{asset_type_id:+assetUnitType.value,tag:assetUnitTag.value,version:editingAssetVersion}:{asset_type_id:+assetUnitType.value,tag:assetUnitTag.value,status:unitStatus.value,warehouse_id:+unitWarehouse.value}",
		"body.tag+(id?' atualizado. Estado e localização permanecem inalterados.':' criado.')" ,
		"id=\"assetUnitOperationalHint\"",
		"Estado e localização preservam histórico.",
		"altere-os somente pelos controles",
	}
	for _, want := range checks {
		if !strings.Contains(js+html, want) {
			t.Fatalf("asset audited edit contract missing %q", want)
		}
	}
	if strings.Contains(js, "let body={asset_type_id:+assetUnitType.value,tag:assetUnitTag.value,status:assetUnitStatus.value,warehouse_id:+assetUnitWarehouse.value}") {
		t.Fatal("generic asset edit still sends operational fields")
	}
}

func TestFiscalAndInventoryCreationSafetyContracts(t *testing.T) {
	read := func(path string) string {
		t.Helper()
		b, err := os.ReadFile(path)
		if err != nil { t.Fatal(err) }
		return string(b)
	}
	js := read(filepath.Join("static", "app.js"))
	stock := read("stock_engine.go")
	migration := read(filepath.Join("..", "..", "migrations", "020_inventory_creation_idempotency.sql"))

	for _, want := range []string{
		"function resetFiscalAfterSuccess()",
		"lines.textContent=''",
		"Este SKU já foi adicionado à nota.",
		"Cada item deve ter um SKU selecionado e sem duplicidade.",
		"Nota fiscal já estava confirmada.",
		"const inventoryCreatePendingKey='pendingInventoryCreate'",
		"function readInventoryCreatePending()",
		"function saveInventoryCreatePending(p)",
		"function clearInventoryCreatePending()",
		"Reenviar criação pendente",
		"operation_id:pending.operation_id",
		"definitiveInventoryCreateError(err)",
		"sort.Slice(skuIDs",
		"for _, skuID := range skuIDs",
		"ADD COLUMN operation_id text",
		"ADD COLUMN request_hash text",
		"inventory_validations_operation_id_unique",
	} {
		if !strings.Contains(js+stock+migration, want) {
			t.Fatalf("fiscal/inventory safety contract missing %q", want)
		}
	}

	if strings.Contains(js, "body:JSON.stringify({warehouse_id:warehouseId})") {
		t.Fatal("inventory total creation reintroduced request without operation_id")
	}
	if strings.Contains(js, "body:'{}'});setListMessage('Inventário cego criado.") {
		t.Fatal("inventory blind creation reintroduced request without operation_id")
	}
}

func TestCRUDOptimisticConcurrencyUIContracts(t *testing.T) {
	jsb, err := os.ReadFile(filepath.Join("static", "app.js"))
	if err != nil { t.Fatal(err) }
	mb, err := os.ReadFile(filepath.Join("static", "catalog_masters.js"))
	if err != nil { t.Fatal(err) }
	pb, err := os.ReadFile(filepath.Join("static", "products.js"))
	if err != nil { t.Fatal(err) }
	js := string(jsb)+string(mb)+string(pb)
	for _, want := range []string{
		"registro foi alterado por outro operador",
		"body.version=itemVersion",
		"body.version=editing.version",
		"version:editingAssetVersion",
		"body.version=selectedUserVersion",
		"body.version=Number(selected?.version||0)",
		"permission_ids:ids,version:Number(profile?.version||0)",
	} {
		if !strings.Contains(js, want) {
			t.Fatalf("CRUD optimistic concurrency UI contract missing %q", want)
		}
	}
}


func TestReportsWTDMTDAndWarehouseAccessContracts(t *testing.T) {
	jsb, err := os.ReadFile(filepath.Join("static", "app.js"))
	if err != nil {
		t.Fatal(err)
	}
	htmlb, err := os.ReadFile(filepath.Join("templates", "reports.html"))
	if err != nil {
		t.Fatal(err)
	}
	js := string(jsb)
	html := string(htmlb)
	for _, want := range []string{
		"loadWarehouses(true,'/api/reports/warehouses')",
		"a.setDate(a.getDate()-a.getDay())",
		"Semana até hoje (WTD)",
		"Mês até hoje (MTD)",
		`data-period="custom"`,
		"WTD considera domingo como início da semana.",
		`id="reportStartLabel"`,
		"startLabel.hidden=balance",
		"endLabelText.textContent=balance?'Data de corte':'Até'",
		`let csv='\uFEFF'`,
		`.join('\r\n')`,
		"/^[=+\\-@]/",
		"csvText(r.barcode)",
	} {
		if !strings.Contains(js+html, want) {
			t.Fatalf("reports WTD/MTD/export contract missing %q", want)
		}
	}
	if strings.Contains(js, "(a.getDay()+6)%7") {
		t.Fatal("week-to-date must start on Sunday, not Monday")
	}
	if strings.Contains(js, `let csv='\\uFEFF'`) || strings.Contains(js, `.join('\\r\\n')`) {
		t.Fatal("CSV must emit real BOM and CRLF, not escaped text")
	}
}


func TestReportsNavigationIsPermissionAwareAcrossPages(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("templates", "base.html"))
	if err != nil { t.Fatal(err) }
	html := string(b)
	if strings.Count(html, `{{if .CanReports}}<a href="/reports">Relatórios</a>{{end}}`) != 1 {
		t.Fatal("Reports must be exposed exactly once by the canonical permission-aware navigation")
	}
}

func TestCrossModuleSelectedObjectContextContracts(t *testing.T) {
	read := func(path string) string {
		t.Helper()
		b, err := os.ReadFile(path)
		if err != nil { t.Fatal(err) }
		return string(b)
	}
	js := read(filepath.Join("static", "app.js"))
	css := read(filepath.Join("static", "app.css"))
	assets := read(filepath.Join("templates", "assets.html"))
	fiscal := read(filepath.Join("templates", "fiscal.html"))
	inventory := read(filepath.Join("templates", "inventory.html"))
	catalog := read(filepath.Join("templates", "catalog.html"))

	for _, want := range []string{
		`card.after(detail)`,
		`id="masterStatus" class="operation-feedback"`,
		`id="productStatus" class="operation-feedback"`,
		`id="inventoryCreateMessage" class="operation-feedback"`,
		`function setCreateMessage(text,kind='')`,
		`id="assetUnitOperations"`,
		`renderAsset(r,true)`,
		`ops.parentElement===unitOpsHost`,
		`className='operation-feedback fiscal-line-feedback'`,
		`Selecione um produto/SKU nesta linha.`,
		".asset-unit-layout{display:grid;grid-template-columns:minmax(260px,340px) minmax(0,1fr)",
		".fiscal-line-feedback{grid-column:1/-1",
	} {
		if !strings.Contains(js+css+assets+fiscal+inventory+catalog+read(filepath.Join("templates","products.html")), want) {
			t.Fatalf("selected-object context contract missing %q", want)
		}
	}
	if strings.Contains(js, `if(createTotal)if(createTotal)`) {
		t.Fatal("inventory creation must not retain duplicated guards")
	}
}
