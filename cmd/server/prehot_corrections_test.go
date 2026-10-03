package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"golang.org/x/crypto/bcrypt"
)

func TestPreHotStockGeneralAvailabilityAndPassword(t *testing.T) {
	a, sku, wh1, wh2 := newStockTestApp(t)
	ctx := context.Background()
	mv := func(res operationResult, err error) operationResult { return mustMove2(t, res, err) }
	mv(a.createEntry(ctx, movementRequest{OperationID: "prehot-entry", SKUID: sku, WarehouseID: wh1, Quantity: "20", UnitCost: "10"}, "prehot-entry"))
	assertStock(t, a, sku, wh1, "20.000", "10.0000")
	assertStock(t, a, sku, wh2, "0", "0")

	rows, err := a.db.QueryContext(ctx, stockRowsSQL(true, false))
	if err != nil {
		t.Fatalf("general stock query: %v", err)
	}
	defer rows.Close()
	var found bool
	for rows.Next() {
		var id int64
		var name, unit, min, qty string
		var below bool
		if err := rows.Scan(&id,&name,&unit,&min,&qty,&below); err != nil {
			t.Fatal(err)
		}
		if name == "Arroz" {
			found = true
			if qty != "20.000" {
				t.Fatalf("general qty=%s", qty)
			}
		}
	}
	if !found {
		t.Fatalf("general stock did not include stocked item")
	}

	// saída com origem única usa o único local com saldo; tentativa no local sem saldo é bloqueada pelo backend.
	mv(a.createExit(ctx, movementRequest{OperationID: "prehot-exit", SKUID: sku, WarehouseID: wh1, Quantity: "5"}, "prehot-exit"))
	assertStock(t, a, sku, wh1, "15.000", "10.0000")
	if _, err := a.createExit(ctx, movementRequest{OperationID: "prehot-wrong-wh", SKUID: sku, WarehouseID: wh2, Quantity: "1"}, "prehot-wrong-wh"); err == nil {
		t.Fatalf("exit from empty warehouse was accepted")
	}

	mv(a.createTransfer(ctx, movementRequest{OperationID: "prehot-transfer", SKUID: sku, FromWarehouseID: wh1, ToWarehouseID: wh2, Quantity: "5"}, "prehot-transfer"))
	assertStock(t, a, sku, wh1, "10.000", "10.0000")
	assertStock(t, a, sku, wh2, "5.000", "10.0000")
	if _, err := a.createExit(ctx, movementRequest{OperationID: "prehot-too-much", SKUID: sku, WarehouseID: wh2, Quantity: "50"}, "prehot-too-much"); err == nil {
		t.Fatalf("overspend was accepted")
	}

	retry1 := mv(a.createTransfer(ctx, movementRequest{OperationID: "prehot-idem", SKUID: sku, FromWarehouseID: wh1, ToWarehouseID: wh2, Quantity: "1"}, "prehot-idem"))
	retry2 := mv(a.createTransfer(ctx, movementRequest{OperationID: "prehot-idem", SKUID: sku, FromWarehouseID: wh1, ToWarehouseID: wh2, Quantity: "1"}, "prehot-idem"))
	if retry1.MovementID != retry2.MovementID {
		t.Fatalf("idempotent transfer duplicated")
	}
	assertStock(t, a, sku, wh1, "9.000", "10.0000")
	assertStock(t, a, sku, wh2, "6.000", "10.0000")

	req := httptest.NewRequest(http.MethodGet, "/api/stock/availability?sku_id="+strconv.FormatInt(sku, 10), nil)
	rr := httptest.NewRecorder()
	a.handleStockAvailability(rr, req)
	if rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), "VR") || !strings.Contains(rr.Body.String(), "Totalfood Estoque Seco") {
		t.Fatalf("availability response: status=%d body=%s", rr.Code, rr.Body.String())
	}
}

func TestChangeOwnPasswordValidationAndLogin(t *testing.T) {
	a := &app{db: openTestDatabase(t)}
	ctx := context.Background()
	if err := runMigrations(ctx, a.db, "../../migrations"); err != nil {
		t.Fatal(err)
	}
	var profileID int64
	if err := a.db.QueryRow(`SELECT id FROM profiles WHERE name='Diretor'`).Scan(&profileID); err != nil {
		t.Fatal(err)
	}
	hashBytes, _ := bcrypt.GenerateFromPassword([]byte("old-password"), bcryptCost)
	hash := string(hashBytes)
	var userID int64
	if err := a.db.QueryRow(`INSERT INTO users (login,password_hash,profile_id,active) VALUES ('selfpass',$1,$2,true) RETURNING id`, hash, profileID).Scan(&userID); err != nil {
		t.Fatal(err)
	}
	post := func(body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPatch, "/api/me/password", bytes.NewBufferString(body))
		req = req.WithContext(context.WithValue(req.Context(), authCtxKey, authUser{ID: userID, Login: "selfpass", ProfileID: profileID, Profile: "Diretor"}))
		rr := httptest.NewRecorder()
		a.handleChangeOwnPassword(rr, req)
		return rr
	}
	if rr := post(`{"current_password":"bad","new_password":"new-password","confirm_password":"new-password"}`); rr.Code != http.StatusUnauthorized {
		t.Fatalf("wrong current status=%d", rr.Code)
	}
	if rr := post(`{"current_password":"old-password","new_password":"new-password","confirm_password":"other-password"}`); rr.Code != http.StatusBadRequest {
		t.Fatalf("mismatch status=%d", rr.Code)
	}
	if rr := post(`{"current_password":"old-password","new_password":"new-password","confirm_password":"new-password"}`); rr.Code != http.StatusOK {
		t.Fatalf("valid status=%d body=%s", rr.Code, rr.Body.String())
	}
	if _, ok := a.verifyPassword(ctx, "selfpass", "old-password"); ok {
		t.Fatalf("old password still valid")
	}
	if _, ok := a.verifyPassword(ctx, "selfpass", "new-password"); !ok {
		t.Fatalf("new password invalid")
	}
	_ = json.Valid(nil)
}
