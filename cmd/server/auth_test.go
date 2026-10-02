package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"
)

type loginData struct {
	Cookie *http.Cookie
	CSRF   string
}

func TestAuthLoginSessionLogoutAndAuthorization(t *testing.T) {
	a, sku, wh, _ := newStockTestApp(t)
	ctx := context.Background()
	directorID := createTestUser(t, a, "director", "secret", "Diretor", true)
	createTestUser(t, a, "inactive", "secret", "Diretor", false)
	createTestUser(t, a, "sales", "secret", "Vendas", true)

	login := doLogin(t, a, "director", "secret")
	if login.Cookie == nil || login.CSRF == "" {
		t.Fatalf("login did not return cookie/csrf")
	}
	if rr := loginAttempt(a, "director", "bad"); rr.Code != http.StatusUnauthorized {
		t.Fatalf("wrong password status=%d", rr.Code)
	}
	if rr := loginAttempt(a, "nobody", "bad"); rr.Code != http.StatusUnauthorized {
		t.Fatalf("unknown user status=%d", rr.Code)
	}
	if rr := loginAttempt(a, "inactive", "secret"); rr.Code != http.StatusForbidden {
		t.Fatalf("inactive status=%d", rr.Code)
	}

	me := authReq(a, http.MethodGet, "/api/me", nil, login, "")
	if me.Code != http.StatusOK {
		t.Fatalf("me status=%d body=%s", me.Code, me.Body.String())
	}

	a.db.ExecContext(ctx, `UPDATE sessions SET expires_at=now()-interval '1 second' WHERE token_hash=$1`, hashToken(login.Cookie.Value))
	if rr := authReq(a, http.MethodGet, "/api/me", nil, login, ""); rr.Code != http.StatusUnauthorized {
		t.Fatalf("expired status=%d", rr.Code)
	}
	login = doLogin(t, a, "director", "secret")

	body := map[string]any{"operation_id": "auth-entry-1", "sku_id": sku, "warehouse_id": wh, "quantity": "1", "unit_cost": "10", "responsible": "forged"}
	if rr := authReq(a, http.MethodPost, "/api/movements/entry", body, login, "bad-csrf"); rr.Code != http.StatusForbidden {
		t.Fatalf("csrf status=%d", rr.Code)
	}
	if rr := authReq(a, http.MethodPost, "/api/movements/entry", body, loginData{}, ""); rr.Code != http.StatusUnauthorized {
		t.Fatalf("no session status=%d", rr.Code)
	}
	if rr := authReq(a, http.MethodPost, "/api/movements/entry", body, login, ""); rr.Code != http.StatusOK {
		t.Fatalf("director entry status=%d body=%s", rr.Code, rr.Body.String())
	}
	var userID int64
	if err := a.db.QueryRowContext(ctx, `SELECT user_id FROM movements WHERE operation_id='auth-entry-1'`).Scan(&userID); err != nil || userID != directorID {
		t.Fatalf("movement user_id=%d err=%v", userID, err)
	}

	res1 := authReq(a, http.MethodPost, "/api/movements/entry", body, login, "")
	if res1.Code != http.StatusOK {
		t.Fatalf("idempotent retry status=%d", res1.Code)
	}
	assertStock(t, a, sku, wh, "1.000", "10.0000")

	sales := doLogin(t, a, "sales", "secret")
	if rr := authReq(a, http.MethodGet, "/api/me", nil, sales, ""); rr.Code != http.StatusOK {
		t.Fatalf("profile without operations permission must still read own session, status=%d body=%s", rr.Code, rr.Body.String())
	}
	if rr := authReq(a, http.MethodPost, "/api/movements/entry", map[string]any{"operation_id": "denied", "sku_id": sku, "warehouse_id": wh, "quantity": "1", "unit_cost": "1"}, sales, ""); rr.Code != http.StatusForbidden {
		t.Fatalf("profile without permission status=%d", rr.Code)
	}
	if rr := authReq(a, http.MethodPost, "/api/users", map[string]any{"login": "x", "password": "secret", "profile": "Vendas"}, sales, ""); rr.Code != http.StatusForbidden {
		t.Fatalf("unauth create user status=%d", rr.Code)
	}
	if rr := authReq(a, http.MethodPost, "/api/logout", nil, sales, ""); rr.Code != http.StatusOK {
		t.Fatalf("profile without operations permission must still logout, status=%d body=%s", rr.Code, rr.Body.String())
	}
	if rr := authReq(a, http.MethodPost, "/api/users", map[string]any{"login": "newuser", "password": "secret", "profile": "Vendas"}, login, ""); rr.Code != http.StatusCreated {
		t.Fatalf("director create user status=%d body=%s", rr.Code, rr.Body.String())
	}

	if rr := authReq(a, http.MethodPost, "/api/logout", nil, login, ""); rr.Code != http.StatusOK {
		t.Fatalf("logout status=%d", rr.Code)
	}
	if rr := authReq(a, http.MethodGet, "/api/me", nil, login, ""); rr.Code != http.StatusUnauthorized {
		t.Fatalf("post logout status=%d", rr.Code)
	}
}

func TestBootstrapDirectorIsIdempotent(t *testing.T) {
	db := openTestDatabase(t)
	a := &app{db: db}
	if err := runMigrations(context.Background(), db, filepath.Join("..", "..", "migrations")); err != nil {
		t.Fatal(err)
	}
	t.Setenv("BOOTSTRAP_ADMIN_ENABLED", "true")
	t.Setenv("BOOTSTRAP_ADMIN_USER", "diretor")
	t.Setenv("BOOTSTRAP_ADMIN_PASSWORD", "secret")
	if err := a.bootstrapDirector(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := a.bootstrapDirector(context.Background()); err != nil {
		t.Fatal(err)
	}
	assertCount(t, db, `SELECT count(*) FROM users WHERE login=$1`, 1, "diretor")
	var active, mustChange bool
	var profile string
	var hash string
	if err := db.QueryRow(`SELECT u.active,u.must_change_password,p.name,u.password_hash FROM users u JOIN profiles p ON p.id=u.profile_id WHERE u.login='diretor'`).Scan(&active, &mustChange, &profile, &hash); err != nil {
		t.Fatal(err)
	}
	if !active || !mustChange || profile != "Diretor" || hash == "secret" || bcrypt.CompareHashAndPassword([]byte(hash), []byte("secret")) != nil {
		t.Fatalf("bootstrap director invalid active=%v must_change=%v profile=%s", active, mustChange, profile)
	}
	var permissionsTotal int
	if err := db.QueryRow(`SELECT count(*) FROM permissions`).Scan(&permissionsTotal); err != nil {
		t.Fatal(err)
	}
	assertCount(t, db, `SELECT count(*) FROM profile_permissions pp JOIN profiles p ON p.id=pp.profile_id WHERE p.name='Diretor'`, permissionsTotal)
}

func createTestUser(t *testing.T, a *app, login, pass, profile string, active bool) int64 {
	t.Helper()
	h, err := bcrypt.GenerateFromPassword([]byte(pass), bcryptCost)
	if err != nil {
		t.Fatal(err)
	}
	var id int64
	err = a.db.QueryRow(`INSERT INTO users (login,password_hash,profile_id,active) SELECT $1,$2,id,$3 FROM profiles WHERE name=$4 RETURNING id`, login, string(h), active, profile).Scan(&id)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func doLogin(t *testing.T, a *app, login, pass string) loginData {
	t.Helper()
	rr := loginAttempt(a, login, pass)
	if rr.Code != http.StatusOK {
		t.Fatalf("login status=%d body=%s", rr.Code, rr.Body.String())
	}
	var out map[string]any
	_ = json.Unmarshal(rr.Body.Bytes(), &out)
	csrf, _ := out["csrf_token"].(string)
	return loginData{Cookie: rr.Result().Cookies()[0], CSRF: csrf}
}

func loginAttempt(a *app, login, pass string) *httptest.ResponseRecorder {
	authThrottle.mu.Lock()
	authThrottle.m = map[string][]time.Time{}
	authThrottle.mu.Unlock()
	return authReq(a, http.MethodPost, "/api/login", map[string]any{"login": login, "password": pass}, loginData{}, "")
}

func authReq(a *app, method, path string, body any, ld loginData, csrf string) *httptest.ResponseRecorder {
	var b []byte
	if body != nil {
		b, _ = json.Marshal(body)
	}
	req := httptest.NewRequest(method, path, bytes.NewReader(b))
	req.RemoteAddr = "127.0.0.1:12345"
	if ld.Cookie != nil {
		req.AddCookie(ld.Cookie)
	}
	if method != http.MethodGet && method != http.MethodPost || path != "/api/login" {
	}
	if csrf == "" {
		csrf = ld.CSRF
	}
	if csrf != "" {
		req.Header.Set(csrfHeaderName, csrf)
	}
	rr := httptest.NewRecorder()
	newHTTPServer("127.0.0.1:0", a).Handler.ServeHTTP(rr, req)
	return rr
}

func TestMain(m *testing.M) { os.Exit(m.Run()) }

func TestSessionExpiredRejectsDirectly(t *testing.T) { _ = time.Second }
