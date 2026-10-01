package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)


func warehouseFixture(t *testing.T, a *app, label string, active bool) int64 {
	t.Helper()
	var id int64
	if err := a.db.QueryRow(`INSERT INTO warehouses(name,active) VALUES($1,$2) RETURNING id`, t.Name()+"-"+label, active).Scan(&id); err != nil {
		t.Fatalf("warehouse fixture %s: %v", label, err)
	}
	return id
}

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
	a, sku, wh1, _ := newStockTestApp(t)
	ctx := context.Background()
	wh2 := warehouseFixture(t, a, "inactive-stock", false)
	if _, err := a.createEntry(ctx, movementRequest{OperationID: "guard-seed-source", SKUID: sku, WarehouseID: wh1, Quantity: "3", UnitCost: "1"}, "guard-seed-source"); err != nil {
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
	rr := authReq(a, http.MethodPost, "/api/movements/entry", map[string]any{"operation_id": "inactive-http-entry", "sku_id": sku, "warehouse_id": wh2, "quantity": "1", "unit_cost": "1"}, director, "")
	if rr.Code != http.StatusConflict || !strings.Contains(rr.Body.String(), "Depósito inativo") {
		t.Fatalf("inactive HTTP movement status=%d body=%s", rr.Code, rr.Body.String())
	}
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
	a, sku, _, _ := newStockTestApp(t)
	wh := warehouseFixture(t, a, "negative-balance", true)
	userID := createTestUser(t, a, "warehouse-negative-director", "secret", "Diretor", true)
	director := doLogin(t, a, "warehouse-negative-director", "secret")
	tx, err := a.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	var movementID int64
	if err := tx.QueryRow(`INSERT INTO movements(operation_id,request_hash,movement_type,user_id) VALUES('negative-balance-fixture','fixture','SAIDA',$1) RETURNING id`, userID).Scan(&movementID); err != nil {
		tx.Rollback()
		t.Fatal(err)
	}
	if _, err := tx.Exec(`INSERT INTO movement_lines(movement_id,sku_id,warehouse_id,direction,quantity,unit_cost) VALUES($1,$2,$3,'OUT',1,0)`, movementID, sku, wh); err != nil {
		tx.Rollback()
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
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


func TestWarehouseDatabaseInvariantsRejectDirectBypass(t *testing.T) {
	a, sku, _, _ := newStockTestApp(t)
	ctx := context.Background()
	wh1 := warehouseFixture(t, a, "occupied-db-bypass", true)
	wh2 := warehouseFixture(t, a, "inactive-db-bypass", false)
	if _, err := a.createEntry(ctx, movementRequest{OperationID: "db-bypass-seed", SKUID: sku, WarehouseID: wh1, Quantity: "1", UnitCost: "1"}, "db-bypass-seed"); err != nil {
		t.Fatal(err)
	}
	if _, err := a.db.Exec(`UPDATE warehouses SET active=false WHERE id=$1`, wh1); err == nil || !strings.Contains(err.Error(), "warehouse not empty") {
		t.Fatalf("direct non-empty deactivation err=%v", err)
	}
	var active bool
	if err := a.db.QueryRow(`SELECT active FROM warehouses WHERE id=$1`, wh1).Scan(&active); err != nil || !active {
		t.Fatalf("direct deactivation changed occupied warehouse active=%v err=%v", active, err)
	}
	userID := createTestUser(t, a, "warehouse-db-bypass", "secret", "Diretor", true)
	tx, err := a.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	var movementID int64
	if err := tx.QueryRow(`INSERT INTO movements(operation_id,request_hash,movement_type,user_id) VALUES('db-inactive-line','fixture','ENTRADA',$1) RETURNING id`, userID).Scan(&movementID); err != nil {
		tx.Rollback()
		t.Fatal(err)
	}
	if _, err := tx.Exec(`INSERT INTO movement_lines(movement_id,sku_id,warehouse_id,direction,quantity,unit_cost) VALUES($1,$2,$3,'IN',1,1)`, movementID, sku, wh2); err == nil || !strings.Contains(err.Error(), "movement warehouse inactive or not found") {
		tx.Rollback()
		t.Fatalf("direct movement into inactive warehouse err=%v", err)
	}
	tx.Rollback()

	wh3 := warehouseFixture(t, a, "inactive-history-edit", true)
	entry, err := a.createEntry(ctx, movementRequest{OperationID: "db-history-entry", SKUID: sku, WarehouseID: wh3, Quantity: "1", UnitCost: "1"}, "db-history-entry")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.createExit(ctx, movementRequest{OperationID: "db-history-exit", SKUID: sku, WarehouseID: wh3, Quantity: "1"}, "db-history-exit"); err != nil {
		t.Fatal(err)
	}
	if _, err := a.db.Exec(`UPDATE warehouses SET active=false WHERE id=$1`, wh3); err != nil {
		t.Fatalf("deactivate zero-balance warehouse: %v", err)
	}
	if _, err := a.db.Exec(`UPDATE movement_lines SET quantity=2 WHERE movement_id=$1 AND warehouse_id=$2`, entry.MovementID, wh3); err == nil || !strings.Contains(err.Error(), "movement warehouse inactive or not found") {
		t.Fatalf("direct history edit in inactive warehouse err=%v", err)
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


func TestWarehouseWithCurrentAssetCannotBeDeactivatedAndDirectAssignmentIsRejected(t *testing.T) {
	a, _, _, _ := newStockTestApp(t)
	wh1 := warehouseFixture(t, a, "asset-occupied", true)
	wh2 := warehouseFixture(t, a, "asset-inactive", false)
	var typeID int64
	if err := a.db.QueryRow(`INSERT INTO asset_types(name) VALUES('Tipo Ocupação Depósito') RETURNING id`).Scan(&typeID); err != nil {
		t.Fatal(err)
	}
	if _, err := a.db.Exec(`INSERT INTO assets(asset_type_id,tag,status,current_warehouse_id) VALUES($1,'WH-ASSET-OCCUPIED','ATIVO',$2)`, typeID, wh1); err != nil {
		t.Fatal(err)
	}
	createTestUser(t, a, "warehouse-asset-director", "secret", "Diretor", true)
	director := doLogin(t, a, "warehouse-asset-director", "secret")
	rr := authReq(a, http.MethodPost, "/api/admin/warehouses/"+itoa(wh1)+"/deactivate", nil, director, "")
	if rr.Code != http.StatusConflict || !strings.Contains(rr.Body.String(), "warehouse_not_empty") {
		t.Fatalf("warehouse with asset deactivate status=%d body=%s", rr.Code, rr.Body.String())
	}
	list := authReq(a, http.MethodGet, "/api/admin/warehouses", nil, director, "")
	if list.Code != http.StatusOK || !strings.Contains(list.Body.String(), `"asset_count":1`) {
		t.Fatalf("warehouse occupancy missing asset count status=%d body=%s", list.Code, list.Body.String())
	}

	if _, err := a.db.Exec(`INSERT INTO assets(asset_type_id,tag,status,current_warehouse_id) VALUES($1,'WH-ASSET-DIRECT-INACTIVE','ATIVO',$2)`, typeID, wh2); err == nil || !strings.Contains(err.Error(), "asset warehouse inactive or not found") {
		t.Fatalf("direct inactive asset assignment err=%v", err)
	}
}

func TestWarehouseAdminPanelFollowsWarehousePermission(t *testing.T) {
	a, _, _, _ := newStockTestApp(t)
	createTestUser(t, a, "warehouse-panel-director", "secret", "Diretor", true)
	director := doLogin(t, a, "warehouse-panel-director", "secret")
	if _, err := a.db.Exec(`DELETE FROM profile_permissions pp USING profiles p, permissions perm WHERE pp.profile_id=p.id AND pp.permission_id=perm.id AND p.name='Diretor' AND perm.resource='warehouses' AND perm.action='write'`); err != nil {
		t.Fatal(err)
	}
	page := authReq(a, http.MethodGet, "/admin", nil, director, "")
	if page.Code != http.StatusOK {
		t.Fatalf("admin page status=%d body=%s", page.Code, page.Body.String())
	}
	if strings.Contains(page.Body.String(), "<h1>Depósitos</h1>") {
		t.Fatalf("warehouse panel rendered without warehouses/write")
	}
	if rr := authReq(a, http.MethodGet, "/api/admin/warehouses", nil, director, ""); rr.Code != http.StatusForbidden {
		t.Fatalf("warehouse API status without permission=%d", rr.Code)
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


func TestWarehouseMigration019IncrementalAndIdempotent(t *testing.T) {
	db := openTestDatabase(t)
	ctx := context.Background()
	migrationsDir := filepath.Join("..", "..", "migrations")

	baseDir := t.TempDir()
	for i := 1; i <= 18; i++ {
		matches, err := filepath.Glob(filepath.Join(migrationsDir, fmt.Sprintf("%03d_*.sql", i)))
		if err != nil || len(matches) != 1 {
			t.Fatalf("base migration %03d matches=%v err=%v", i, matches, err)
		}
		body, err := os.ReadFile(matches[0])
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(baseDir, filepath.Base(matches[0])), body, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := runMigrations(ctx, db, baseDir); err != nil {
		t.Fatalf("base migrations: %v", err)
	}

	var movementsBefore, linesBefore, assetsBefore int64
	if err := db.QueryRowContext(ctx, "SELECT count(*) FROM movements").Scan(&movementsBefore); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRowContext(ctx, "SELECT count(*) FROM movement_lines").Scan(&linesBefore); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRowContext(ctx, "SELECT count(*) FROM assets").Scan(&assetsBefore); err != nil {
		t.Fatal(err)
	}

	incrementalDir := t.TempDir()
	matches, err := filepath.Glob(filepath.Join(migrationsDir, "019_*.sql"))
	if err != nil || len(matches) != 1 {
		t.Fatalf("migration 019 matches=%v err=%v", matches, err)
	}
	body, err := os.ReadFile(matches[0])
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(incrementalDir, filepath.Base(matches[0])), body, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := runMigrations(ctx, db, incrementalDir); err != nil {
		t.Fatalf("incremental migration 019: %v", err)
	}
	var appliedBefore int
	if err := db.QueryRowContext(ctx, "SELECT count(*) FROM schema_migrations").Scan(&appliedBefore); err != nil {
		t.Fatal(err)
	}
	if err := runMigrations(ctx, db, incrementalDir); err != nil {
		t.Fatalf("migration 019 idempotent startup: %v", err)
	}
	var appliedAfter int
	if err := db.QueryRowContext(ctx, "SELECT count(*) FROM schema_migrations").Scan(&appliedAfter); err != nil {
		t.Fatal(err)
	}
	if appliedBefore != appliedAfter {
		t.Fatalf("migration 019 reapplied: %d -> %d", appliedBefore, appliedAfter)
	}

	var movementsAfter, linesAfter, assetsAfter int64
	if err := db.QueryRowContext(ctx, "SELECT count(*) FROM movements").Scan(&movementsAfter); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRowContext(ctx, "SELECT count(*) FROM movement_lines").Scan(&linesAfter); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRowContext(ctx, "SELECT count(*) FROM assets").Scan(&assetsAfter); err != nil {
		t.Fatal(err)
	}
	if movementsBefore != movementsAfter || linesBefore != linesAfter || assetsBefore != assetsAfter {
		t.Fatalf("migration 019 changed historical data: movements %d->%d lines %d->%d assets %d->%d",
			movementsBefore, movementsAfter, linesBefore, linesAfter, assetsBefore, assetsAfter)
	}

	var permissionCount, indexCount, triggerCount int
	if err := db.QueryRowContext(ctx, "SELECT count(*) FROM permissions WHERE resource='warehouses' AND action='write'").Scan(&permissionCount); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRowContext(ctx, "SELECT count(*) FROM pg_indexes WHERE tablename='warehouses' AND indexname='warehouses_name_tracking_unique'").Scan(&indexCount); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRowContext(ctx, "SELECT count(*) FROM pg_trigger WHERE NOT tgisinternal AND tgname IN ('warehouses_no_delete','warehouses_deactivate_only_empty','assets_active_warehouse','movement_lines_active_warehouse')").Scan(&triggerCount); err != nil {
		t.Fatal(err)
	}
	if permissionCount != 1 || indexCount != 1 || triggerCount != 4 {
		t.Fatalf("migration 019 contract permission=%d index=%d triggers=%d", permissionCount, indexCount, triggerCount)
	}
}
