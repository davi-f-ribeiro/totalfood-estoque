package main

import (
	"context"
	"errors"
	"net/http"
	"testing"
)

func TestWarehouseAdministrationLifecycleAndMovementGuard(t *testing.T) {
	a, sku, wh1, wh2 := newStockTestApp(t)
	ctx := context.Background()
	createTestUser(t, a, "warehouse-director", "secret", "Diretor", true)
	director := doLogin(t, a, "warehouse-director", "secret")

	create := authReq(a, http.MethodPost, "/api/admin/warehouses", map[string]any{"name": "Depósito Teste"}, director, "")
	if create.Code != http.StatusCreated {
		t.Fatalf("create warehouse status=%d body=%s", create.Code, create.Body.String())
	}
	newID := jsonID(t, create.Body.Bytes())
	var active bool
	if err := a.db.QueryRow(`SELECT active FROM warehouses WHERE id=$1`, newID).Scan(&active); err != nil || active {
		t.Fatalf("new warehouse must start inactive active=%v err=%v", active, err)
	}

	if _, err := a.createEntry(ctx, movementRequest{OperationID: "inactive-entry", SKUID: sku, WarehouseID: newID, Quantity: "1", UnitCost: "1"}, "inactive-entry"); !errors.Is(err, errWarehouseInactive) {
		t.Fatalf("inactive warehouse accepted entry: %v", err)
	}
	if rr := authReq(a, http.MethodPost, "/api/admin/warehouses/"+itoa(newID)+"/activate", nil, director, ""); rr.Code != http.StatusOK {
		t.Fatalf("activate status=%d body=%s", rr.Code, rr.Body.String())
	}
	if _, err := a.createEntry(ctx, movementRequest{OperationID: "active-entry", SKUID: sku, WarehouseID: newID, Quantity: "2", UnitCost: "3"}, "active-entry"); err != nil {
		t.Fatalf("active warehouse rejected entry: %v", err)
	}
	if rr := authReq(a, http.MethodPost, "/api/admin/warehouses/"+itoa(newID)+"/deactivate", nil, director, ""); rr.Code != http.StatusConflict {
		t.Fatalf("non-empty deactivate status=%d body=%s", rr.Code, rr.Body.String())
	}
	if _, err := a.createExit(ctx, movementRequest{OperationID: "empty-new", SKUID: sku, WarehouseID: newID, Quantity: "2"}, "empty-new"); err != nil {
		t.Fatalf("emptying warehouse failed: %v", err)
	}
	if rr := authReq(a, http.MethodPost, "/api/admin/warehouses/"+itoa(newID)+"/deactivate", nil, director, ""); rr.Code != http.StatusOK {
		t.Fatalf("empty deactivate status=%d body=%s", rr.Code, rr.Body.String())
	}
	if _, err := a.createEntry(ctx, movementRequest{OperationID: "inactive-entry-2", SKUID: sku, WarehouseID: newID, Quantity: "1", UnitCost: "1"}, "inactive-entry-2"); !errors.Is(err, errWarehouseInactive) {
		t.Fatalf("deactivated warehouse accepted entry: %v", err)
	}

	if _, err := a.createEntry(ctx, movementRequest{OperationID: "seed-transfer", SKUID: sku, WarehouseID: wh1, Quantity: "1", UnitCost: "1"}, "seed-transfer"); err != nil {
		t.Fatal(err)
	}
	if _, err := a.createTransfer(ctx, movementRequest{OperationID: "inactive-destination", SKUID: sku, FromWarehouseID: wh1, ToWarehouseID: newID, Quantity: "1"}, "inactive-destination"); !errors.Is(err, errWarehouseInactive) {
		t.Fatalf("inactive transfer destination accepted: %v", err)
	}
	_ = wh2

	var movements int
	if err := a.db.QueryRow(`SELECT count(*) FROM movement_lines WHERE warehouse_id=$1`, newID).Scan(&movements); err != nil {
		t.Fatal(err)
	}
	if movements != 2 {
		t.Fatalf("history was lost or unexpected lines created: got=%d want=2", movements)
	}
	var audits int
	if err := a.db.QueryRow(`SELECT count(*) FROM warehouse_audit WHERE warehouse_id=$1`, newID).Scan(&audits); err != nil || audits != 3 {
		t.Fatalf("audit count=%d err=%v want=3", audits, err)
	}
}


func TestWarehouseNameIsUniqueForTracking(t *testing.T) {
	a, _, _, _ := newStockTestApp(t)
	createTestUser(t, a, "warehouse-name-director", "secret", "Diretor", true)
	director := doLogin(t, a, "warehouse-name-director", "secret")

	first := authReq(a, http.MethodPost, "/api/admin/warehouses", map[string]any{"name": "Depósito Rastreável"}, director, "")
	if first.Code != http.StatusCreated {
		t.Fatalf("first create status=%d body=%s", first.Code, first.Body.String())
	}
	for _, name := range []string{"depósito rastreável", " DEPÓSITO RASTREÁVEL "} {
		rr := authReq(a, http.MethodPost, "/api/admin/warehouses", map[string]any{"name": name}, director, "")
		if rr.Code != http.StatusConflict {
			t.Fatalf("duplicate %q status=%d body=%s", name, rr.Code, rr.Body.String())
		}
	}
}

func TestWarehouseCannotBeDeleted(t *testing.T) {
	a, _, wh, _ := newStockTestApp(t)
	if _, err := a.db.Exec(`DELETE FROM warehouses WHERE id=$1`, wh); err == nil {
		t.Fatal("warehouse delete unexpectedly succeeded")
	}
	var exists bool
	if err := a.db.QueryRow(`SELECT EXISTS(SELECT 1 FROM warehouses WHERE id=$1)`, wh).Scan(&exists); err != nil || !exists {
		t.Fatalf("warehouse missing after rejected delete exists=%v err=%v", exists, err)
	}
}

func TestWarehouseAdminRequiresPermission(t *testing.T) {
	a, _, _, _ := newStockTestApp(t)
	createTestUser(t, a, "warehouse-sales", "secret", "Vendas", true)
	sales := doLogin(t, a, "warehouse-sales", "secret")
	if rr := authReq(a, http.MethodGet, "/api/admin/warehouses", nil, sales, ""); rr.Code != http.StatusForbidden {
		t.Fatalf("sales warehouse admin status=%d", rr.Code)
	}
}
