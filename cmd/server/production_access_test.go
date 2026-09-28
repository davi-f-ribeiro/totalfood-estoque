package main

import (
	"context"
	"net/http"
	"testing"

	"golang.org/x/crypto/bcrypt"
)

func TestProductionAccessResetPreservesHistoryAndForcesPasswordChange(t *testing.T) {
	a, sku, wh1, _ := newStockTestApp(t)
	ctx := context.Background()
	director := createTestUser(t, a, "old-director", "old-pass", "Diretor", true)
	oldUser := createTestUser(t, a, "legacy-user", "legacy-pass", "Estoquista", true)
	mv := func(res operationResult, err error) operationResult { return mustMove2(t, res, err) }
	mv(a.createEntry(ctx, movementRequest{OperationID: "prod-reset-entry", SKUID: sku, WarehouseID: wh1, Quantity: "7", UnitCost: "3", UserID: oldUser}, "prod-reset-entry"))
	before := tableCount(t, a, "movements")
	res, err := a.resetProductionAccess(ctx, "initial-temp-pass")
	if err != nil {
		t.Fatalf("reset: %v", err)
	}
	if res.UsersBefore < 2 || res.AuthenticatableAfter != 1 || res.Strategy != "desativação" {
		t.Fatalf("unexpected reset result: %+v", res)
	}
	if got := tableCount(t, a, "movements"); got != before {
		t.Fatalf("movements changed: before=%d after=%d", before, got)
	}
	var active bool
	if err := a.db.QueryRow(`SELECT active FROM users WHERE id=$1`, oldUser).Scan(&active); err != nil || active {
		t.Fatalf("old user active=%v err=%v", active, err)
	}
	var hash string
	var mustChange bool
	if err := a.db.QueryRow(`SELECT password_hash,must_change_password FROM users WHERE login='diretor' AND active=true`).Scan(&hash, &mustChange); err != nil {
		t.Fatal(err)
	}
	if hash == "initial-temp-pass" || bcrypt.CompareHashAndPassword([]byte(hash), []byte("initial-temp-pass")) != nil || !mustChange {
		t.Fatalf("director hash/must_change invalid")
	}
	if _, ok := a.verifyPassword(ctx, "old-director", "old-pass"); ok {
		t.Fatalf("old account still authenticates")
	}
	if id, ok := a.verifyPassword(ctx, "diretor", "initial-temp-pass"); !ok || id == director {
		t.Fatalf("director initial password did not authenticate")
	}

	login := doLogin(t, a, "diretor", "initial-temp-pass")
	if rr := authReq(a, http.MethodGet, "/api/me", nil, login, ""); rr.Code != http.StatusForbidden {
		t.Fatalf("must-change session accessed api: %d", rr.Code)
	}
	if rr := authReq(a, http.MethodPatch, "/api/me/password", map[string]any{"current_password": "initial-temp-pass", "new_password": "new-production-pass", "confirm_password": "new-production-pass"}, login, ""); rr.Code != http.StatusOK {
		t.Fatalf("password change status=%d body=%s", rr.Code, rr.Body.String())
	}
	if _, ok := a.verifyPassword(ctx, "diretor", "initial-temp-pass"); ok {
		t.Fatalf("initial password still authenticates")
	}
	if _, ok := a.verifyPassword(ctx, "diretor", "new-production-pass"); !ok {
		t.Fatalf("new password does not authenticate")
	}
}

func tableCount(t *testing.T, a *app, table string) int {
	t.Helper()
	var n int
	if err := a.db.QueryRow(`SELECT count(*) FROM ` + table).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}
