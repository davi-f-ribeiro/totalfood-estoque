package main

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"strconv"
	"sync"
	"testing"
)

func completedDivergentAdjustmentInventory(t *testing.T, label string) (inventoryTestEnv, inventorySelection, int64) {
	t.Helper()
	e := newInventoryTestEnv(t)
	inv, sku := oneReviewInventory(t, e, label, "10.000", "8.000")
	review, err := e.a.getInventoryReviewView(context.Background(), inv.ID)
	if err != nil {
		t.Fatal(err)
	}
	line := reviewBySKU(t, review)[sku]
	if _, err := e.a.saveInventoryRecount(context.Background(), inv.ID, sku, "8.000", line.Version, e.userID); err != nil {
		t.Fatal(err)
	}
	if _, err := e.a.completeInventory(context.Background(), inv.ID, e.userID, "Divergência física confirmada."); err != nil {
		t.Fatal(err)
	}
	return e, inv, sku
}

func TestInventoryAdjustmentCandidateRequiresCompletedDivergence(t *testing.T) {
	e := newInventoryTestEnv(t)
	inv, sku := oneReviewInventory(t, e, "candidate", "10.000", "8.000")
	ctx := context.Background()

	tx, err := e.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	_, err = inventoryAdjustmentCandidateTx(ctx, tx, inv.ID, sku)
	_ = tx.Rollback()
	if !errors.Is(err, errInventoryAdjustmentNotEligible) {
		t.Fatalf("REVIEW candidate error=%v, want %v", err, errInventoryAdjustmentNotEligible)
	}

	review, err := e.a.getInventoryReviewView(ctx, inv.ID)
	if err != nil {
		t.Fatal(err)
	}
	line := reviewBySKU(t, review)[sku]
	if _, err := e.a.saveInventoryRecount(ctx, inv.ID, sku, "8.000", line.Version, e.userID); err != nil {
		t.Fatal(err)
	}
	if _, err := e.a.completeInventory(ctx, inv.ID, e.userID, "Divergência física confirmada."); err != nil {
		t.Fatal(err)
	}

	tx, err = e.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	candidate, err := inventoryAdjustmentCandidateTx(ctx, tx, inv.ID, sku)
	_ = tx.Rollback()
	if err != nil {
		t.Fatal(err)
	}
	if candidate.Difference != "-2.000" {
		t.Fatalf("difference=%q, want -2.000", candidate.Difference)
	}
}

func TestApplyInventoryStockAdjustmentCreatesSingleMovement(t *testing.T) {
	e, inv, sku := completedAdjustmentWithEngineStock(t, "single-movement", "8.000")
	ctx := context.Background()

	var before int
	if err := e.db.QueryRow(`SELECT count(*) FROM movements`).Scan(&before); err != nil {
		t.Fatal(err)
	}
	_, err := e.a.applyInventoryStockAdjustment(ctx, inv.ID, sku, e.userID, "single-adjustment")
	if err != nil {
		t.Fatalf("apply adjustment: %v", err)
	}
	var after int
	if err := e.db.QueryRow(`SELECT count(*) FROM movements`).Scan(&after); err != nil {
		t.Fatal(err)
	}
	if after != before+1 {
		t.Fatalf("movements after adjustment: before=%d after=%d", before, after)
	}
	var adjustments int
	if err := e.db.QueryRow(`SELECT count(*) FROM inventory_stock_adjustments`).Scan(&adjustments); err != nil {
		t.Fatal(err)
	}
	if adjustments != 1 {
		t.Fatalf("adjustments=%d, want 1", adjustments)
	}
}

func completedAdjustmentWithEngineStock(t *testing.T, label, physical string) (inventoryTestEnv, inventorySelection, int64) {
	t.Helper()
	e := newInventoryTestEnv(t)
	ctx := context.Background()
	wh := e.warehouse(t, label+"-warehouse", true)
	sku := e.sku(t, label+"-sku", true, true)
	if _, err := e.a.createEntry(ctx, movementRequest{
		OperationID: "seed-" + label,
		SKUID: sku,
		WarehouseID: wh,
		Quantity: "10",
		UnitCost: "5",
		UserID: e.userID,
	}, "seed-"+label); err != nil {
		t.Fatal(err)
	}
	inv, err := e.a.createTotalInventory(ctx, e.userID, wh)
	if err != nil {
		t.Fatal(err)
	}
	countInventoryToReview(t, e, inv, map[int64]string{sku: physical})
	review, err := e.a.getInventoryReviewView(ctx, inv.ID)
	if err != nil {
		t.Fatal(err)
	}
	line := reviewBySKU(t, review)[sku]
	if _, err := e.a.saveInventoryRecount(ctx, inv.ID, sku, physical, line.Version, e.userID); err != nil {
		t.Fatal(err)
	}
	if _, err := e.a.completeInventory(ctx, inv.ID, e.userID, "Divergência física confirmada."); err != nil {
		t.Fatal(err)
	}
	return e, inv, sku
}

func TestInventoryAdjustmentNegativeUsesLotsAndIsIdempotent(t *testing.T) {
	e, inv, sku := completedAdjustmentWithEngineStock(t, "negative", "8.000")
	ctx := context.Background()
	res, err := e.a.applyInventoryStockAdjustment(ctx, inv.ID, sku, e.userID, "adjust-negative")
	if err != nil {
		t.Fatal(err)
	}
	retry, err := e.a.applyInventoryStockAdjustment(ctx, inv.ID, sku, e.userID, "adjust-negative")
	if err != nil {
		t.Fatal(err)
	}
	if retry.MovementID != res.MovementID {
		t.Fatalf("retry movement=%d want %d", retry.MovementID, res.MovementID)
	}
	s, err := getStockSnapshot(ctx, e.db, sku, inv.WarehouseID)
	if err != nil {
		t.Fatal(err)
	}
	if s.Quantity != "8.000" || s.AvgCost != "5.0000" {
		t.Fatalf("stock after negative adjustment=%+v", s)
	}
	var allocated string
	if err := e.db.QueryRow(`
SELECT COALESCE(sum(a.quantity),0)::text
FROM stock_lot_allocations a
JOIN movement_lines ml ON ml.id=a.movement_line_id
WHERE ml.movement_id=$1`, res.MovementID).Scan(&allocated); err != nil {
		t.Fatal(err)
	}
	if allocated != "2.000" {
		t.Fatalf("allocated=%s want 2.000", allocated)
	}
	if _, err := e.a.applyInventoryStockAdjustment(ctx, inv.ID, sku, e.userID, "another-operation"); !errors.Is(err, errInventoryAdjustmentAlreadyApplied) {
		t.Fatalf("second operation error=%v", err)
	}
}

func TestInventoryAdjustmentPositiveCreatesTraceableLotAtAverageCost(t *testing.T) {
	e, inv, sku := completedAdjustmentWithEngineStock(t, "positive", "12.000")
	ctx := context.Background()
	res, err := e.a.applyInventoryStockAdjustment(ctx, inv.ID, sku, e.userID, "adjust-positive")
	if err != nil {
		t.Fatal(err)
	}
	s, err := getStockSnapshot(ctx, e.db, sku, inv.WarehouseID)
	if err != nil {
		t.Fatal(err)
	}
	if s.Quantity != "12.000" || s.AvgCost != "5.0000" {
		t.Fatalf("stock after positive adjustment=%+v", s)
	}
	var qty, manufacturerLot string
	if err := e.db.QueryRow(`
SELECT l.initial_quantity::text,COALESCE(l.manufacturer_lot_code,'')
FROM stock_lots l
JOIN movement_lines ml ON ml.id=l.source_movement_line_id
WHERE ml.movement_id=$1`, res.MovementID).Scan(&qty, &manufacturerLot); err != nil {
		t.Fatal(err)
	}
	if qty != "2.000" || manufacturerLot != "" {
		t.Fatalf("adjustment lot qty=%s manufacturer=%q", qty, manufacturerLot)
	}
}

func TestInventoryAdjustmentRouteRequiresGranularPermissionAndRejectsClientQuantity(t *testing.T) {
	e := newInventoryTestEnv(t)
	mux := http.NewServeMux()
	e.a.registerInventoryHandlers(mux)

	// Existing inventory test user belongs to Diretor, but migration 030 does
	// not grant stock_adjustment/write to any profile automatically.
	req := httptest.NewRequest(http.MethodPost, "/api/inventory-validations/1/lines/1/adjust", strings.NewReader(`{"operation_id":"adjust-api"}`))
	rec := httptest.NewRecorder()
	// No session means auth blocks before handler; the permission assignment
	// itself is asserted directly to prove there is no implicit profile grant.
	var grants int
	if err := e.db.QueryRow(`
SELECT count(*)
FROM profile_permissions pp
JOIN permissions p ON p.id=pp.permission_id
WHERE p.resource='stock_adjustment' AND p.action='write'`).Scan(&grants); err != nil {
		t.Fatal(err)
	}
	if grants != 0 {
		t.Fatalf("stock_adjustment/write has %d automatic profile grants, want 0", grants)
	}
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated status=%d want %d", rec.Code, http.StatusUnauthorized)
	}

	var payload inventoryAdjustmentRequest
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"operation_id":"adjust-api","quantity":"99"}`))
	if decodeInventoryJSON(rec, req, &payload, false) {
		t.Fatal("adjustment payload accepted client quantity")
	}
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("unknown-field status=%d want %d", rec.Code, http.StatusBadRequest)
	}
}

func TestInventoryHistoryExposesAppliedAdjustment(t *testing.T) {
	e, inv, sku := completedAdjustmentWithEngineStock(t, "history-adjustment", "12.000")
	ctx := context.Background()
	res, err := e.a.applyInventoryStockAdjustment(ctx, inv.ID, sku, e.userID, "history-adjustment")
	if err != nil {
		t.Fatal(err)
	}
	history, err := e.a.getInventoryHistory(ctx, inv.ID)
	if err != nil {
		t.Fatal(err)
	}
	var movementID *int64
	for _, line := range history.Lines {
		if line.SKUID == sku {
			movementID = line.AdjustmentMovementID
			break
		}
	}
	if movementID == nil || *movementID != res.MovementID {
		t.Fatalf("adjustment movement=%v want %d", movementID, res.MovementID)
	}
}

func TestInventoryCompletionRemainsReadOnlyUntilExplicitAdjustment(t *testing.T) {
	e, inv, _ := completedAdjustmentWithEngineStock(t, "readonly-contract", "8.000")
	var adjustments int
	if err := e.db.QueryRow(`SELECT count(*) FROM inventory_stock_adjustments WHERE validation_id=$1`, inv.ID).Scan(&adjustments); err != nil {
		t.Fatal(err)
	}
	if adjustments != 0 {
		t.Fatalf("audit completion created %d adjustments", adjustments)
	}
	var adjustmentMovements int
	if err := e.db.QueryRow(`SELECT count(*) FROM movements WHERE movement_type='AJUSTE'`).Scan(&adjustmentMovements); err != nil {
		t.Fatal(err)
	}
	if adjustmentMovements != 0 {
		t.Fatalf("audit completion created %d AJUSTE movements", adjustmentMovements)
	}
}

func TestInventoryAdjustmentRejectsReductionWhenCurrentStockChanged(t *testing.T) {
	e, inv, sku := completedAdjustmentWithEngineStock(t, "changed-stock", "2.000")
	ctx := context.Background()
	if _, err := e.a.createExit(ctx, movementRequest{
		OperationID: "post-audit-exit",
		SKUID: sku,
		WarehouseID: inv.WarehouseID,
		Quantity: "5",
		UserID: e.userID,
	}, "post-audit-exit"); err != nil {
		t.Fatal(err)
	}
	if _, err := e.a.applyInventoryStockAdjustment(ctx, inv.ID, sku, e.userID, "adjust-after-change"); !errors.Is(err, errInsufficientStock) {
		t.Fatalf("adjustment error=%v want insufficient stock", err)
	}
	var adjustments int
	if err := e.db.QueryRow(`SELECT count(*) FROM inventory_stock_adjustments WHERE validation_id=$1`, inv.ID).Scan(&adjustments); err != nil {
		t.Fatal(err)
	}
	if adjustments != 0 {
		t.Fatalf("failed adjustment persisted %d links", adjustments)
	}
}

func TestInventoryAdjustmentConcurrentRequestsCreateOneMovement(t *testing.T) {
	e, inv, sku := completedAdjustmentWithEngineStock(t, "concurrent-adjust", "12.000")
	ctx := context.Background()
	var wg sync.WaitGroup
	errs := make(chan error, 2)
	ids := make(chan int64, 2)
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			res, err := e.a.applyInventoryStockAdjustment(ctx, inv.ID, sku, e.userID, "same-concurrent-adjustment")
			if err != nil {
				errs <- err
				return
			}
			ids <- res.MovementID
		}()
	}
	wg.Wait()
	close(errs)
	close(ids)
	for err := range errs {
		t.Fatalf("concurrent adjustment failed: %v", err)
	}
	var first int64
	for id := range ids {
		if first == 0 {
			first = id
		} else if id != first {
			t.Fatalf("concurrent movements differ: %d and %d", first, id)
		}
	}
	var count int
	if err := e.db.QueryRow(`SELECT count(*) FROM inventory_stock_adjustments WHERE validation_id=$1 AND sku_id=$2`, inv.ID, sku).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("adjustment links=%d want 1", count)
	}
}

func TestInventoryAdjustmentPositiveFallsBackToLastRealEntryCost(t *testing.T) {
	e, inv, sku := completedAdjustmentWithEngineStock(t, "fallback-cost", "12.000")
	ctx := context.Background()
	if _, err := e.a.createExit(ctx, movementRequest{
		OperationID: "drain-before-fallback",
		SKUID: sku,
		WarehouseID: inv.WarehouseID,
		Quantity: "10",
		UserID: e.userID,
	}, "drain-before-fallback"); err != nil {
		t.Fatal(err)
	}
	res, err := e.a.applyInventoryStockAdjustment(ctx, inv.ID, sku, e.userID, "adjust-fallback-cost")
	if err != nil {
		t.Fatal(err)
	}
	var cost string
	if err := e.db.QueryRow(`
SELECT unit_cost::numeric(14,4)::text
FROM movement_lines
WHERE movement_id=$1 AND direction='IN'`, res.MovementID).Scan(&cost); err != nil {
		t.Fatal(err)
	}
	if cost != "5.0000" {
		t.Fatalf("fallback adjustment cost=%s want 5.0000", cost)
	}
}

func TestInventoryAdjustmentPositiveBlocksWithoutValidCostBasis(t *testing.T) {
	e := newInventoryTestEnv(t)
	ctx := context.Background()
	wh := e.warehouse(t, "no-cost-warehouse", true)
	sku := e.sku(t, "no-cost-sku", true, true)
	if _, err := e.a.createEntry(ctx, movementRequest{
		OperationID: "seed-zero-cost",
		SKUID: sku,
		WarehouseID: wh,
		Quantity: "10",
		UnitCost: "0",
		UserID: e.userID,
	}, "seed-zero-cost"); err != nil {
		t.Fatal(err)
	}
	inv, err := e.a.createTotalInventory(ctx, e.userID, wh)
	if err != nil {
		t.Fatal(err)
	}
	countInventoryToReview(t, e, inv, map[int64]string{sku: "12.000"})
	review, err := e.a.getInventoryReviewView(ctx, inv.ID)
	if err != nil {
		t.Fatal(err)
	}
	line := reviewBySKU(t, review)[sku]
	if _, err := e.a.saveInventoryRecount(ctx, inv.ID, sku, "12.000", line.Version, e.userID); err != nil {
		t.Fatal(err)
	}
	if _, err := e.a.completeInventory(ctx, inv.ID, e.userID, "Divergência física confirmada."); err != nil {
		t.Fatal(err)
	}
	if _, err := e.a.createExit(ctx, movementRequest{
		OperationID: "drain-zero-cost",
		SKUID: sku,
		WarehouseID: wh,
		Quantity: "10",
		UserID: e.userID,
	}, "drain-zero-cost"); err != nil {
		t.Fatal(err)
	}
	if _, err := e.a.applyInventoryStockAdjustment(ctx, inv.ID, sku, e.userID, "adjust-no-cost"); !errors.Is(err, errInventoryAdjustmentCostUnavailable) {
		t.Fatalf("adjustment error=%v want cost unavailable", err)
	}
	var links int
	if err := e.db.QueryRow(`SELECT count(*) FROM inventory_stock_adjustments WHERE validation_id=$1 AND sku_id=$2`, inv.ID, sku).Scan(&links); err != nil {
		t.Fatal(err)
	}
	if links != 0 {
		t.Fatalf("blocked no-cost adjustment persisted %d links", links)
	}
}

func TestInventoryAdjustmentRouteAndOperationLookupRespectGranularPermissionAndOwner(t *testing.T) {
	e, inv, sku := completedAdjustmentWithEngineStock(t, "permission-owner", "12.000")
	if _, err := e.db.Exec(`
INSERT INTO profile_permissions (profile_id,permission_id)
SELECT p.id,perm.id
FROM profiles p
JOIN permissions perm ON perm.resource='stock_adjustment' AND perm.action='write'
WHERE p.name='Estoquista'
ON CONFLICT DO NOTHING`); err != nil {
		t.Fatal(err)
	}
	ownerID := createTestUser(t, e.a, "adjust-owner-"+t.Name(), "secret", "Estoquista", true)
	otherID := createTestUser(t, e.a, "adjust-other-"+t.Name(), "secret", "Estoquista", true)
	denied := createTestUser(t, e.a, "adjust-denied-"+t.Name(), "secret", "Vendas", true)
	_ = denied

	deniedLogin := doLogin(t, e.a, "adjust-denied-"+t.Name(), "secret")
	rr := authReq(e.a, http.MethodPost,
		"/api/inventory-validations/"+strconv.FormatInt(inv.ID,10)+"/lines/"+strconv.FormatInt(sku,10)+"/adjust",
		map[string]any{"operation_id":"adjust-authz"}, deniedLogin, "")
	if rr.Code != http.StatusForbidden {
		t.Fatalf("denied adjustment status=%d body=%s", rr.Code, rr.Body.String())
	}

	ownerLogin := doLogin(t, e.a, "adjust-owner-"+t.Name(), "secret")
	rr = authReq(e.a, http.MethodPost,
		"/api/inventory-validations/"+strconv.FormatInt(inv.ID,10)+"/lines/"+strconv.FormatInt(sku,10)+"/adjust",
		map[string]any{"operation_id":"adjust-authz"}, ownerLogin, "")
	if rr.Code != http.StatusOK {
		t.Fatalf("authorized adjustment status=%d body=%s", rr.Code, rr.Body.String())
	}

	rr = authReq(e.a, http.MethodGet, "/api/operations/adjust-authz", nil, ownerLogin, "")
	if rr.Code != http.StatusOK {
		t.Fatalf("owner lookup status=%d body=%s", rr.Code, rr.Body.String())
	}
	otherLogin := doLogin(t, e.a, "adjust-other-"+t.Name(), "secret")
	rr = authReq(e.a, http.MethodGet, "/api/operations/adjust-authz", nil, otherLogin, "")
	if rr.Code != http.StatusForbidden {
		t.Fatalf("other operator lookup status=%d body=%s owner=%d other=%d", rr.Code, rr.Body.String(), ownerID, otherID)
	}
}

func TestInventoryAdjustmentRejectsInactiveWarehouse(t *testing.T) {
	e, inv, sku := completedAdjustmentWithEngineStock(t, "inactive-adjustment", "12.000")
	ctx := context.Background()
	if _, err := e.a.createExit(ctx, movementRequest{
		OperationID: "drain-before-deactivate",
		SKUID: sku,
		WarehouseID: inv.WarehouseID,
		Quantity: "10",
		UserID: e.userID,
	}, "drain-before-deactivate"); err != nil {
		t.Fatal(err)
	}
	if _, err := e.db.Exec(`UPDATE warehouses SET active=false WHERE id=$1`, inv.WarehouseID); err != nil {
		t.Fatal(err)
	}
	if _, err := e.a.applyInventoryStockAdjustment(ctx, inv.ID, sku, e.userID, "adjust-inactive-warehouse"); !errors.Is(err, errWarehouseInactive) {
		t.Fatalf("adjustment error=%v want inactive warehouse", err)
	}
	var links int
	if err := e.db.QueryRow(`SELECT count(*) FROM inventory_stock_adjustments WHERE validation_id=$1 AND sku_id=$2`, inv.ID, sku).Scan(&links); err != nil {
		t.Fatal(err)
	}
	if links != 0 {
		t.Fatalf("inactive warehouse adjustment persisted %d links", links)
	}
}
