package main

import (
	"context"

	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
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


func TestAdminUserUpdateWithPasswordIsAtomic(t *testing.T) {
	a, _, _, _ := newStockTestApp(t)
	directorID := createTestUser(t, a, "atomic-dir", "secret", "Diretor", true)
	director := doLogin(t, a, "atomic-dir", "secret")
	resp := authReq(a, http.MethodPost, "/api/admin/users", map[string]any{
		"login": "atomic-managed", "password": "oldpass", "profile_id": profileID(t, a, "Vendas"), "active": true,
	}, director, "")
	if resp.Code != http.StatusCreated {
		t.Fatalf("create managed status=%d body=%s", resp.Code, resp.Body.String())
	}
	managedID := jsonID(t, resp.Body.Bytes())
	oldSession := doLogin(t, a, "atomic-managed", "oldpass")
	gerenteID := profileID(t, a, "Gerente")

	rr := authReq(a, http.MethodPatch, "/api/admin/users/"+itoa(managedID), map[string]any{
		"login": "atomic-managed", "password": "newpass", "profile_id": gerenteID, "active": true,
	}, director, "")
	if rr.Code != http.StatusOK {
		t.Fatalf("atomic update status=%d body=%s", rr.Code, rr.Body.String())
	}
	if got := authReq(a, http.MethodGet, "/api/me", nil, oldSession, ""); got.Code != http.StatusUnauthorized {
		t.Fatalf("old session survived atomic user update: %d", got.Code)
	}
	if got := loginAttempt(a, "atomic-managed", "oldpass"); got.Code != http.StatusUnauthorized {
		t.Fatalf("old password still authenticates: %d", got.Code)
	}
	if got := loginAttempt(a, "atomic-managed", "newpass"); got.Code != http.StatusOK {
		t.Fatalf("new password failed after atomic update: %d body=%s", got.Code, got.Body.String())
	}
	var profileAfter int64
	if err := a.db.QueryRow(`SELECT profile_id FROM users WHERE id=$1`, managedID).Scan(&profileAfter); err != nil {
		t.Fatal(err)
	}
	if profileAfter != gerenteID {
		t.Fatalf("profile=%d want %d", profileAfter, gerenteID)
	}

	// Force the cadastral UPDATE to fail on the unique login. Password and
	// profile must roll back with it; the endpoint is one atomic mutation.
	rr = authReq(a, http.MethodPatch, "/api/admin/users/"+itoa(managedID), map[string]any{
		"login": "atomic-dir", "password": "must-not-apply", "profile_id": profileID(t, a, "Vendas"), "active": true,
	}, director, "")
	if rr.Code != http.StatusConflict {
		t.Fatalf("failing atomic update status=%d body=%s", rr.Code, rr.Body.String())
	}
	if got := loginAttempt(a, "atomic-managed", "newpass"); got.Code != http.StatusOK {
		t.Fatalf("previous password changed after rollback: %d body=%s", got.Code, got.Body.String())
	}
	if got := loginAttempt(a, "atomic-managed", "must-not-apply"); got.Code != http.StatusUnauthorized {
		t.Fatalf("rolled back password unexpectedly authenticates: %d", got.Code)
	}
	if err := a.db.QueryRow(`SELECT profile_id FROM users WHERE id=$1`, managedID).Scan(&profileAfter); err != nil {
		t.Fatal(err)
	}
	if profileAfter != gerenteID {
		t.Fatalf("profile changed despite rollback: %d want %d", profileAfter, gerenteID)
	}
	if directorID <= 0 {
		t.Fatal("director setup failed")
	}
}

func TestAdminPermissionsRequireValidExplicitSetAndPreserveOnFailure(t *testing.T) {
	a, _, _, _ := newStockTestApp(t)
	usersWrite := permissionID(t, a, "users", "write")
	profilesWrite := permissionID(t, a, "profiles", "write")
	stockRead := permissionID(t, a, "stock", "read")

	var adminProfile, targetProfile int64
	if err := a.db.QueryRow(`INSERT INTO profiles(name) VALUES('Atomic Admin Profile') RETURNING id`).Scan(&adminProfile); err != nil {
		t.Fatal(err)
	}
	if err := a.db.QueryRow(`INSERT INTO profiles(name) VALUES('Atomic Target Profile') RETURNING id`).Scan(&targetProfile); err != nil {
		t.Fatal(err)
	}
	for _, pid := range []int64{usersWrite, profilesWrite} {
		if _, err := a.db.Exec(`INSERT INTO profile_permissions(profile_id,permission_id) VALUES($1,$2)`, adminProfile, pid); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := a.db.Exec(`INSERT INTO profile_permissions(profile_id,permission_id) VALUES($1,$2)`, targetProfile, stockRead); err != nil {
		t.Fatal(err)
	}
	createTestUserWithProfileID(t, a, "atomic-admin", "secret", adminProfile, true)
	admin := doLogin(t, a, "atomic-admin", "secret")

	// A malformed permission set must be rejected before the existing set is
	// destroyed. This specifically protects against INSERT ... SELECT(0 rows).
	rr := authReq(a, http.MethodPut, "/api/admin/profiles/"+itoa(targetProfile)+"/permissions", map[string]any{
		"permission_ids": []int64{stockRead, 99999999},
	}, admin, "")
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("invalid permission status=%d body=%s", rr.Code, rr.Body.String())
	}
	var n int
	if err := a.db.QueryRow(`SELECT count(*) FROM profile_permissions WHERE profile_id=$1 AND permission_id=$2`, targetProfile, stockRead).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("existing permissions were lost after invalid request: count=%d", n)
	}

	// The current operator cannot remove the permissions that authorize their
	// own administration session.
	rr = authReq(a, http.MethodPut, "/api/admin/profiles/"+itoa(adminProfile)+"/permissions", map[string]any{
		"permission_ids": []int64{stockRead},
	}, admin, "")
	if rr.Code != http.StatusConflict {
		t.Fatalf("self permission removal status=%d body=%s", rr.Code, rr.Body.String())
	}
	for _, pid := range []int64{usersWrite, profilesWrite} {
		if err := a.db.QueryRow(`SELECT count(*) FROM profile_permissions WHERE profile_id=$1 AND permission_id=$2`, adminProfile, pid).Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n != 1 {
			t.Fatalf("admin permission %d lost after rejected self-removal", pid)
		}
	}
	if got := authReq(a, http.MethodGet, "/api/admin/users", nil, admin, ""); got.Code != http.StatusOK {
		t.Fatalf("session revoked on rolled back permission change: %d body=%s", got.Code, got.Body.String())
	}
}

func TestConcurrentCrossDemotionsCannotRemoveAllAdministrators(t *testing.T) {
	a, _, _, _ := newStockTestApp(t)
	usersWrite := permissionID(t, a, "users", "write")
	profilesWrite := permissionID(t, a, "profiles", "write")

	makeAdminProfile := func(name string) int64 {
		t.Helper()
		var id int64
		if err := a.db.QueryRow(`INSERT INTO profiles(name) VALUES($1) RETURNING id`, name).Scan(&id); err != nil {
			t.Fatal(err)
		}
		for _, pid := range []int64{usersWrite, profilesWrite} {
			if _, err := a.db.Exec(`INSERT INTO profile_permissions(profile_id,permission_id) VALUES($1,$2)`, id, pid); err != nil {
				t.Fatal(err)
			}
		}
		return id
	}
	profileA := makeAdminProfile("Concurrent Admin A")
	profileB := makeAdminProfile("Concurrent Admin B")
	createTestUserWithProfileID(t, a, "concurrent-admin-a", "secret", profileA, true)
	createTestUserWithProfileID(t, a, "concurrent-admin-b", "secret", profileB, true)
	loginA := doLogin(t, a, "concurrent-admin-a", "secret")
	loginB := doLogin(t, a, "concurrent-admin-b", "secret")

	start := make(chan struct{})
	codes := make([]int, 2)
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		<-start
		codes[0] = authReq(a, http.MethodPut, "/api/admin/profiles/"+itoa(profileB)+"/permissions", map[string]any{"permission_ids": []int64{}}, loginA, "").Code
	}()
	go func() {
		defer wg.Done()
		<-start
		codes[1] = authReq(a, http.MethodPut, "/api/admin/profiles/"+itoa(profileA)+"/permissions", map[string]any{"permission_ids": []int64{}}, loginB, "").Code
	}()
	close(start)
	wg.Wait()

	success := 0
	for _, code := range codes {
		if code == http.StatusOK {
			success++
			continue
		}
		if code != http.StatusConflict && code != http.StatusUnauthorized && code != http.StatusForbidden {
			t.Fatalf("unexpected concurrent demotion status=%d all=%v", code, codes)
		}
	}
	if success != 1 {
		t.Fatalf("exactly one cross-demotion may commit, got statuses=%v", codes)
	}

	var activeAdmins int
	if err := a.db.QueryRow(`
SELECT count(DISTINCT u.id)
FROM users u
WHERE u.active=true
  AND EXISTS(SELECT 1 FROM profile_permissions pp JOIN permissions p ON p.id=pp.permission_id WHERE pp.profile_id=u.profile_id AND p.resource='users' AND p.action='write')
  AND EXISTS(SELECT 1 FROM profile_permissions pp JOIN permissions p ON p.id=pp.permission_id WHERE pp.profile_id=u.profile_id AND p.resource='profiles' AND p.action='write')`).Scan(&activeAdmins); err != nil {
		t.Fatal(err)
	}
	if activeAdmins < 1 {
		t.Fatalf("concurrent demotions removed all administrators: statuses=%v", codes)
	}
}
