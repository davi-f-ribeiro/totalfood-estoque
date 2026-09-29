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
