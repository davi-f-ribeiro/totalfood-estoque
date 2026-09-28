package main

import (
	"context"

	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"golang.org/x/crypto/bcrypt"
)

func TestAdminUsersProfilesPermissionsAndSessions(t *testing.T) {
	a, _, _, _ := newStockTestApp(t)
	ctx := context.Background()
	directorID := createTestUser(t, a, "admin-dir", "secret", "Diretor", true)
	createTestUser(t, a, "admin-sales", "secret", "Vendas", true)
	director := doLogin(t, a, "admin-dir", "secret")
	sales := doLogin(t, a, "admin-sales", "secret")

	req := httptest.NewRequest(http.MethodGet, "/admin", nil)
	req.AddCookie(director.Cookie)
	rr := httptest.NewRecorder()
	newHTTPServer("127.0.0.1:0", a).Handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), "Administração") {
		t.Fatalf("director admin status=%d body=%s", rr.Code, rr.Body.String())
	}
	director = doLogin(t, a, "admin-dir", "secret")
	if rr := authReq(a, http.MethodGet, "/api/admin/users", nil, sales, ""); rr.Code != http.StatusForbidden {
		t.Fatalf("sales admin status=%d", rr.Code)
	}

	resp := authReq(a, http.MethodPost, "/api/admin/users", map[string]any{"login": "managed", "password": "oldpass", "profile_id": profileID(t, a, "Vendas"), "active": true}, director, "")
	if resp.Code != http.StatusCreated {
		t.Fatalf("create user status=%d body=%s", resp.Code, resp.Body.String())
	}
	managedID := jsonID(t, resp.Body.Bytes())
	managed := doLogin(t, a, "managed", "oldpass")
	var hash string
	if err := a.db.QueryRowContext(ctx, `SELECT password_hash FROM users WHERE id=$1`, managedID).Scan(&hash); err != nil || hash == "oldpass" || bcrypt.CompareHashAndPassword([]byte(hash), []byte("oldpass")) != nil {
		t.Fatalf("password hash invalid hash=%q err=%v", hash, err)
	}

	gerenteID := profileID(t, a, "Gerente")
	if rr := authReq(a, http.MethodPatch, "/api/admin/users/"+itoa(managedID), map[string]any{"login": "managed", "profile_id": gerenteID, "active": true}, director, ""); rr.Code != http.StatusOK {
		t.Fatalf("change profile status=%d body=%s", rr.Code, rr.Body.String())
	}
	if rr := authReq(a, http.MethodPatch, "/api/admin/users/"+itoa(managedID)+"/password", map[string]any{"password": "newpass"}, director, ""); rr.Code != http.StatusOK {
		t.Fatalf("reset password status=%d body=%s", rr.Code, rr.Body.String())
	}
	if rr := authReq(a, http.MethodGet, "/api/me", nil, managed, ""); rr.Code != http.StatusUnauthorized {
		t.Fatalf("old session not invalidated status=%d", rr.Code)
	}
	if rr := loginAttempt(a, "managed", "oldpass"); rr.Code != http.StatusUnauthorized {
		t.Fatalf("old password status=%d", rr.Code)
	}
	managed = doLogin(t, a, "managed", "newpass")
	if rr := authReq(a, http.MethodPatch, "/api/admin/users/"+itoa(managedID), map[string]any{"login": "managed", "profile_id": gerenteID, "active": false}, director, ""); rr.Code != http.StatusOK {
		t.Fatalf("deactivate status=%d body=%s", rr.Code, rr.Body.String())
	}
	if rr := authReq(a, http.MethodGet, "/api/me", nil, managed, ""); rr.Code != http.StatusUnauthorized {
		t.Fatalf("deactivated session status=%d", rr.Code)
	}
	if rr := loginAttempt(a, "managed", "newpass"); rr.Code != http.StatusForbidden {
		t.Fatalf("inactive login status=%d", rr.Code)
	}

	profResp := authReq(a, http.MethodPost, "/api/admin/profiles", map[string]any{"name": "Operador Admin Test"}, director, "")
	if profResp.Code != http.StatusCreated {
		t.Fatalf("create profile status=%d body=%s", profResp.Code, profResp.Body.String())
	}
	profID := jsonID(t, profResp.Body.Bytes())
	stockReadID := permissionID(t, a, "stock", "read")
	if rr := authReq(a, http.MethodPut, "/api/admin/profiles/"+itoa(profID)+"/permissions", map[string]any{"permission_ids": []int64{stockReadID}}, director, ""); rr.Code != http.StatusOK {
		t.Fatalf("permissions status=%d body=%s", rr.Code, rr.Body.String())
	}
	createTestUserWithProfileID(t, a, "stockonly", "secret", profID, true)
	stockOnly := doLogin(t, a, "stockonly", "secret")
	if rr := authReq(a, http.MethodGet, "/api/stock?warehouse_id=1", nil, stockOnly, ""); rr.Code != http.StatusOK {
		t.Fatalf("stock read status=%d", rr.Code)
	}
	if rr := authReq(a, http.MethodPost, "/api/admin/users", map[string]any{"login": "bad", "password": "secret", "profile_id": profID}, stockOnly, ""); rr.Code != http.StatusForbidden {
		t.Fatalf("stockonly admin status=%d", rr.Code)
	}

	if rr := authReq(a, http.MethodPatch, "/api/admin/users/"+itoa(directorID), map[string]any{"login": "admin-dir", "profile_id": profileID(t, a, "Vendas"), "active": true}, director, ""); rr.Code != http.StatusConflict {
		t.Fatalf("self remove admin status=%d body=%s", rr.Code, rr.Body.String())
	}
	if rr := authReq(a, http.MethodPatch, "/api/admin/users/"+itoa(directorID), map[string]any{"login": "admin-dir", "profile_id": profileID(t, a, "Diretor"), "active": false}, director, ""); rr.Code != http.StatusConflict {
		t.Fatalf("self deactivate status=%d body=%s", rr.Code, rr.Body.String())
	}
}

func profileID(t *testing.T, a *app, name string) int64 {
	t.Helper()
	var id int64
	if err := a.db.QueryRow(`SELECT id FROM profiles WHERE name=$1`, name).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}
func permissionID(t *testing.T, a *app, res, act string) int64 {
	t.Helper()
	var id int64
	if err := a.db.QueryRow(`SELECT id FROM permissions WHERE resource=$1 AND action=$2`, res, act).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}
func createTestUserWithProfileID(t *testing.T, a *app, login, pass string, profileID int64, active bool) int64 {
	t.Helper()
	h, _ := bcrypt.GenerateFromPassword([]byte(pass), bcryptCost)
	var id int64
	if err := a.db.QueryRow(`INSERT INTO users (login,password_hash,profile_id,active) VALUES ($1,$2,$3,$4) RETURNING id`, login, string(h), profileID, active).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}
