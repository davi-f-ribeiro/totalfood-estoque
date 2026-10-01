package main

import (
	"context"
	"errors"
	"net/http"
	"strings"
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


func TestInactiveWarehouseRejectedAcrossStockOperationsAndSelectors(t *testing.T) {
	a, sku, wh1, wh2 := newStockTestApp(t)
	ctx := context.Background()
	if _, err := a.createEntry(ctx, movementRequest{OperationID: "guard-seed-source", SKUID: sku, WarehouseID: wh1, Quantity: "3", UnitCost: "1"}, "guard-seed-source"); err != nil {
		t.Fatal(err)
	}
	if _, err := a.createEntry(ctx, movementRequest{OperationID: "guard-seed-inactive", SKUID: sku, WarehouseID: wh2, Quantity: "2", UnitCost: "1"}, "guard-seed-inactive"); err != nil {
		t.Fatal(err)
	}
	if _, err := a.db.Exec(`UPDATE warehouses SET active=false WHERE id=$1`, wh2); err != nil {
		t.Fatal(err)
	}
	if _, err := a.createEntry(ctx, movementRequest{OperationID: "guard-entry", SKUID: sku, WarehouseID: wh2, Quantity: "1", UnitCost: "1"}, "guard-entry"); !errors.Is(err, errWarehouseInactive) {
		t.Fatalf("inactive entry err=%v", err)
	}
	if _, err := a.createExit(ctx, movementRequest{OperationID: "guard-exit", SKUID: sku, WarehouseID: wh2, Quantity: "1"}, "guard-exit"); !errors.Is(err, errWarehouseInactive) {
		t.Fatalf("inactive exit err=%v", err)
	}
	if _, err := a.createTransfer(ctx, movementRequest{OperationID: "guard-transfer-source", SKUID: sku, FromWarehouseID: wh2, ToWarehouseID: wh1, Quantity: "1"}, "guard-transfer-source"); !errors.Is(err, errWarehouseInactive) {
		t.Fatalf("inactive transfer source err=%v", err)
	}
	if _, err := a.createTransfer(ctx, movementRequest{OperationID: "guard-transfer-destination", SKUID: sku, FromWarehouseID: wh1, ToWarehouseID: wh2, Quantity: "1"}, "guard-transfer-destination"); !errors.Is(err, errWarehouseInactive) {
		t.Fatalf("inactive transfer destination err=%v", err)
	}

	createTestUser(t, a, "warehouse-selector-director", "secret", "Diretor", true)
	director := doLogin(t, a, "warehouse-selector-director", "secret")
	for _, path := range []string{"/api/warehouses", "/api/inventory-validations/warehouses", "/api/stock/availability?sku_id=" + itoa(sku)} {
		rr := authReq(a, http.MethodGet, path, nil, director, "")
		if rr.Code != http.StatusOK {
			t.Fatalf("selector %s status=%d body=%s", path, rr.Code, rr.Body.String())
		}
		if strings.Contains(rr.Body.String(), `"id":`+itoa(wh2)) || strings.Contains(rr.Body.String(), `"warehouse_id":`+itoa(wh2)) {
			t.Fatalf("inactive warehouse leaked from selector %s body=%s", path, rr.Body.String())
		}
	}
}

func TestWarehouseWithNegativeBalanceCannotBeDeactivated(t *testing.T) {
	a, sku, wh, _ := newStockTestApp(t)
	userID := createTestUser(t, a, "warehouse-negative-director", "secret", "Diretor", true)
	director := doLogin(t, a, "warehouse-negative-director", "secret")
	var movementID int64
	if err := a.db.QueryRow(`INSERT INTO movements(operation_id,request_hash,movement_type,user_id) VALUES('negative-balance-fixture','fixture','SAIDA',$1) RETURNING id`, userID).Scan(&movementID); err != nil {
		t.Fatal(err)
	}
	if _, err := a.db.Exec(`INSERT INTO movement_lines(movement_id,sku_id,warehouse_id,direction,quantity,unit_cost) VALUES($1,$2,$3,'OUT',1,0)`, movementID, sku, wh); err != nil {
		t.Fatal(err)
	}
	rr := authReq(a, http.MethodPost, "/api/admin/warehouses/"+itoa(wh)+"/deactivate", nil, director, "")
	if rr.Code != http.StatusConflict {
		t.Fatalf("negative balance deactivate status=%d body=%s", rr.Code, rr.Body.String())
	}
	var active bool
	if err := a.db.QueryRow(`SELECT active FROM warehouses WHERE id=$1`, wh).Scan(&active); err != nil || !active {
		t.Fatalf("warehouse deactivated with negative balance active=%v err=%v", active, err)
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

func TestInactiveWarehouseRejectedByAssetAssignment(t *testing.T) {
	a, _, _, _ := newStockTestApp(t)
	var typeID, whID int64
	if err := a.db.QueryRow(`INSERT INTO asset_types(name) VALUES('Tipo Warehouse Inativo') RETURNING id`).Scan(&typeID); err != nil {
		t.Fatal(err)
	}
	if err := a.db.QueryRow(`INSERT INTO warehouses(name,active) VALUES('Depósito Patrimônio Inativo',false) RETURNING id`).Scan(&whID); err != nil {
		t.Fatal(err)
	}
	createTestUser(t, a, "asset-warehouse-director", "secret", "Diretor", true)
	director := doLogin(t, a, "asset-warehouse-director", "secret")
	rr := authReq(a, http.MethodPost, "/api/assets", map[string]any{"asset_type_id": typeID, "tag": "WH-INACTIVE-ASSET", "status": "ATIVO", "warehouse_id": whID}, director, "")
	if rr.Code != http.StatusConflict {
		t.Fatalf("inactive asset warehouse status=%d body=%s", rr.Code, rr.Body.String())
	}
	var count int
	if err := a.db.QueryRow(`SELECT count(*) FROM assets WHERE tag='WH-INACTIVE-ASSET'`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("asset unexpectedly created count=%d err=%v", count, err)
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
