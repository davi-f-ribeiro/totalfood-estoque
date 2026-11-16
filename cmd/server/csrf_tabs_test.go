package main

import (
	"net/http"
	"regexp"
	"testing"
)

// Abrir o sistema em outra aba não pode derrubar o envio da primeira. O token CSRF é fixo
// por sessão: toda página carrega o mesmo valor, e o token recebido no login continua
// valendo depois de qualquer página ser aberta.
func TestCSRFTokenSurvivesOpeningAnotherPage(t *testing.T) {
	a, _, _, _ := newStockTestApp(t)
	createTestUser(t, a, "tabs-director", "secret", "Diretor", true)
	login := doLogin(t, a, "tabs-director", "secret")

	meta := regexp.MustCompile(`<meta name="csrf-token" content="([^"]+)">`)
	pageToken := func(path string) string {
		t.Helper()
		rr := authReq(a, http.MethodGet, path, nil, login, "")
		m := meta.FindStringSubmatch(rr.Body.String())
		if rr.Code != http.StatusOK || m == nil {
			t.Fatalf("GET %s status=%d, no csrf-token meta", path, rr.Code)
		}
		return m[1]
	}
	first := pageToken("/products")
	second := pageToken("/home")
	if first != second || first != login.CSRF {
		t.Fatalf("csrf token changed between pages: login=%q products=%q home=%q", login.CSRF, first, second)
	}

	// Primeira aba envia depois de a segunda ter sido aberta: passa da checagem de CSRF
	// (400 por faltar campo, não 403).
	rr := authReq(a, http.MethodPost, "/api/catalog/skus", map[string]any{}, login, first)
	if rr.Code == http.StatusForbidden {
		t.Fatalf("submit from the first tab was refused: %s", rr.Body.String())
	}
	if rr := authReq(a, http.MethodPost, "/api/catalog/skus", map[string]any{}, login, "bad-csrf"); rr.Code != http.StatusForbidden {
		t.Fatalf("wrong csrf token status=%d, want 403", rr.Code)
	}

	// Sessão antiga, com token sorteado: a primeira página carregada passa a usar o token fixo.
	if _, err := a.db.Exec(`INSERT INTO sessions (user_id,token_hash,csrf_token_hash,expires_at) SELECT id,$1,$2,now()+interval '1 hour' FROM users WHERE login='tabs-director'`, hashToken("old-session-token"), hashToken("old-random-csrf")); err != nil {
		t.Fatal(err)
	}
	old := loginData{Cookie: &http.Cookie{Name: sessionCookieName, Value: "old-session-token"}}
	rrOld := authReq(a, http.MethodGet, "/products", nil, old, "")
	m := meta.FindStringSubmatch(rrOld.Body.String())
	if m == nil || m[1] != csrfForSession("old-session-token") {
		t.Fatalf("old session did not move to the stable token")
	}
	if rr := authReq(a, http.MethodPost, "/api/catalog/skus", map[string]any{}, old, m[1]); rr.Code == http.StatusForbidden {
		t.Fatalf("old session refused after page load: %s", rr.Body.String())
	}
}
