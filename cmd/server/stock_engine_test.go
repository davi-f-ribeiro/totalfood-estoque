package main

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestStockEngineEntryExitTransferIdempotencyAndIntegrity(t *testing.T) {
	a, sku, wh1, wh2 := newStockTestApp(t)
	ctx := context.Background()
	mv := func(res operationResult, err error) operationResult { return mustMove2(t, res, err) }

	mv(a.createEntry(ctx, movementRequest{OperationID: "op-entry-1", SKUID: sku, WarehouseID: wh1, Quantity: "10", UnitCost: "10"}, "h-entry-1"))
	assertStock(t, a, sku, wh1, "10.000", "10.0000")
	mv(a.createEntry(ctx, movementRequest{OperationID: "op-entry-2", SKUID: sku, WarehouseID: wh1, Quantity: "10", UnitCost: "14"}, "h-entry-2"))
	assertStock(t, a, sku, wh1, "20.000", "12.0000")

	mv(a.createExit(ctx, movementRequest{OperationID: "op-exit-1", SKUID: sku, WarehouseID: wh1, Quantity: "5"}, "h-exit-1"))
	assertStock(t, a, sku, wh1, "15.000", "12.0000")
	exitRetry1 := mv(a.createExit(ctx, movementRequest{OperationID: "op-exit-idem", SKUID: sku, WarehouseID: wh1, Quantity: "1"}, "h-exit-idem"))
	exitRetry2 := mv(a.createExit(ctx, movementRequest{OperationID: "op-exit-idem", SKUID: sku, WarehouseID: wh1, Quantity: "1"}, "h-exit-idem"))
	if exitRetry1.MovementID != exitRetry2.MovementID {
		t.Fatalf("idempotent exit returned different movement")
	}
	assertStock(t, a, sku, wh1, "14.000", "12.0000")
	if _, err := a.createExit(ctx, movementRequest{OperationID: "op-exit-big", SKUID: sku, WarehouseID: wh1, Quantity: "50"}, "h-exit-big"); !errors.Is(err, errInsufficientStock) {
		t.Fatalf("want insufficient stock, got %v", err)
	}
	assertStock(t, a, sku, wh1, "14.000", "12.0000")

	mv(a.createEntry(ctx, movementRequest{OperationID: "op-entry-dest", SKUID: sku, WarehouseID: wh2, Quantity: "5", UnitCost: "20"}, "h-entry-dest"))
	mv(a.createTransfer(ctx, movementRequest{OperationID: "op-transfer-1", SKUID: sku, FromWarehouseID: wh1, ToWarehouseID: wh2, Quantity: "5"}, "h-transfer-1"))
	assertStock(t, a, sku, wh1, "9.000", "12.0000")
	assertStock(t, a, sku, wh2, "10.000", "16.0000")
	if _, err := a.createTransfer(ctx, movementRequest{OperationID: "op-transfer-same", SKUID: sku, FromWarehouseID: wh1, ToWarehouseID: wh1, Quantity: "1"}, "h-same"); !errors.Is(err, errValidation) {
		t.Fatalf("want validation, got %v", err)
	}
	if _, err := a.createTransfer(ctx, movementRequest{OperationID: "op-transfer-big", SKUID: sku, FromWarehouseID: wh1, ToWarehouseID: wh2, Quantity: "99"}, "h-big"); !errors.Is(err, errInsufficientStock) {
		t.Fatalf("want insufficient, got %v", err)
	}
	assertStock(t, a, sku, wh1, "9.000", "12.0000")

	res1 := mv(a.createEntry(ctx, movementRequest{OperationID: "op-idem", SKUID: sku, WarehouseID: wh1, Quantity: "1", UnitCost: "12"}, "same-hash"))
	res2 := mv(a.createEntry(ctx, movementRequest{OperationID: "op-idem", SKUID: sku, WarehouseID: wh1, Quantity: "1", UnitCost: "12"}, "same-hash"))
	if res1.MovementID != res2.MovementID {
		t.Fatalf("idempotent retry returned different movement")
	}
	assertStock(t, a, sku, wh1, "10.000", "12.0000")
	if _, err := a.createEntry(ctx, movementRequest{OperationID: "op-idem", SKUID: sku, WarehouseID: wh1, Quantity: "2", UnitCost: "12"}, "different-hash"); !errors.Is(err, errOperationConflict) {
		t.Fatalf("want conflict, got %v", err)
	}

	if _, err := a.createEntry(ctx, movementRequest{OperationID: "bad-sku", SKUID: 999999, WarehouseID: wh1, Quantity: "1", UnitCost: "1"}, "h"); !errors.Is(err, errNotFound) {
		t.Fatalf("want sku not found, got %v", err)
	}
	if _, err := a.createEntry(ctx, movementRequest{OperationID: "bad-wh", SKUID: sku, WarehouseID: 999999, Quantity: "1", UnitCost: "1"}, "h"); !errors.Is(err, errNotFound) {
		t.Fatalf("want warehouse not found, got %v", err)
	}
	if _, err := a.createEntry(ctx, movementRequest{OperationID: "bad-qty", SKUID: sku, WarehouseID: wh1, Quantity: "0", UnitCost: "1"}, "h"); err == nil {
		t.Fatalf("want invalid quantity rejected")
	}
}

func TestConcurrentExitsCannotBothSpendSameStock(t *testing.T) {
	a, sku, wh, _ := newStockTestApp(t)
	ctx := context.Background()
	mv := func(res operationResult, err error) operationResult { return mustMove2(t, res, err) }
	mv(a.createEntry(ctx, movementRequest{OperationID: "op-seed", SKUID: sku, WarehouseID: wh, Quantity: "10", UnitCost: "10"}, "h-seed"))
	var wg sync.WaitGroup
	errs := make([]error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, errs[i] = a.createExit(ctx, movementRequest{OperationID: []string{"op-a", "op-b"}[i], SKUID: sku, WarehouseID: wh, Quantity: "8"}, []string{"h-a", "h-b"}[i])
		}(i)
	}
	wg.Wait()
	success := 0
	for _, err := range errs {
		if err == nil {
			success++
		} else if !errors.Is(err, errInsufficientStock) {
			t.Fatalf("unexpected concurrent error: %v", err)
		}
	}
	if success != 1 {
		t.Fatalf("expected exactly one success, got %d (%v)", success, errs)
	}
	assertStock(t, a, sku, wh, "2.000", "10.0000")
}

func TestConcurrentTransfersCannotBothSpendSameStock(t *testing.T) {
	a, sku, wh1, wh2 := newStockTestApp(t)
	ctx := context.Background()
	mv := func(res operationResult, err error) operationResult { return mustMove2(t, res, err) }
	mv(a.createEntry(ctx, movementRequest{OperationID: "op-transfer-seed", SKUID: sku, WarehouseID: wh1, Quantity: "10", UnitCost: "10"}, "h-transfer-seed"))
	var wg sync.WaitGroup
	errs := make([]error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, errs[i] = a.createTransfer(ctx, movementRequest{OperationID: []string{"op-ta", "op-tb"}[i], SKUID: sku, FromWarehouseID: wh1, ToWarehouseID: wh2, Quantity: "7"}, []string{"h-ta", "h-tb"}[i])
		}(i)
	}
	wg.Wait()
	success := 0
	for _, err := range errs {
		if err == nil {
			success++
		} else if !errors.Is(err, errInsufficientStock) {
			t.Fatalf("unexpected concurrent transfer error: %v", err)
		}
	}
	if success != 1 {
		t.Fatalf("expected exactly one transfer success, got %d (%v)", success, errs)
	}
	assertStock(t, a, sku, wh1, "3.000", "10.0000")
	assertStock(t, a, sku, wh2, "7.000", "10.0000")
}

func TestDecodeMovementRequestAcceptsBrazilianDecimalComma(t *testing.T) {
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/movements/entry", strings.NewReader(`{"operation_id":"op-comma","sku_id":1,"warehouse_id":1,"quantity":"180","unit_cost":"5,89"}`))
	got, _, ok := decodeMovementRequest(rr, req, true)
	if !ok {
		t.Fatalf("comma decimal request rejected: status=%d body=%s", rr.Code, rr.Body.String())
	}
	if got.Quantity != "180" || got.UnitCost != "5.89" {
		t.Fatalf("decimal normalization got quantity=%q cost=%q", got.Quantity, got.UnitCost)
	}
}

func TestPublicInsufficientStockMessage(t *testing.T) {
	a := &app{}
	rr := httptest.NewRecorder()
	a.writeOperationResponse(rr, "SAIDA", "op-public", operationResult{}, insufficientStockError{Available: "2.000", Unit: "KG"})
	if rr.Code != http.StatusConflict {
		t.Fatalf("status got %d want %d", rr.Code, http.StatusConflict)
	}
	body := rr.Body.String()
	if !strings.Contains(body, "Estoque insuficiente. Disponível: 2.000 KG.") {
		t.Fatalf("public message not localized: %s", body)
	}
	if strings.Contains(body, "insufficient stock") {
		t.Fatalf("public message exposed internal text: %s", body)
	}
}

func newStockTestApp(t *testing.T) (*app, int64, int64, int64) {
	t.Helper()
	db := openTestDatabase(t)
	ctx := context.Background()
	if err := runMigrations(ctx, db, filepath.Join("..", "..", "migrations")); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	a := &app{db: db}
	var cat, item, sku, wh1, wh2 int64
	if err := db.QueryRow(`SELECT id FROM categories WHERE name='Alimentos'`).Scan(&cat); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT id FROM warehouses WHERE name='VR'`).Scan(&wh1); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT id FROM warehouses WHERE name='Totalfood Estoque Seco'`).Scan(&wh2); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`INSERT INTO inventory_items (name, category_id, unit_code) VALUES ('Arroz', $1, 'KG') RETURNING id`, cat).Scan(&item); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`INSERT INTO product_skus (item_id, brand, barcode) VALUES ($1, 'Marca', $2) RETURNING id`, item, t.Name()).Scan(&sku); err != nil {
		t.Fatal(err)
	}
	return a, sku, wh1, wh2
}

func mustMove2(t *testing.T, res operationResult, err error) operationResult {
	t.Helper()
	if err != nil {
		t.Fatalf("move failed: %v", err)
	}
	return res
}

func assertStock(t *testing.T, a *app, sku, wh int64, qty, avg string) {
	t.Helper()
	s, err := getStockSnapshot(context.Background(), a.db, sku, wh)
	if err != nil {
		t.Fatalf("stock: %v", err)
	}
	if s.Quantity != qty || s.AvgCost != avg {
		t.Fatalf("stock got qty=%s avg=%s want qty=%s avg=%s", s.Quantity, s.AvgCost, qty, avg)
	}
}


func TestMovementIdempotencyIsBoundToOriginalOperator(t *testing.T) {
	a, sku, wh, _ := newStockTestApp(t)
	ctx := context.Background()
	userA := createTestUser(t, a, "movement-owner-a", "secret", "Estoquista", true)
	userB := createTestUser(t, a, "movement-owner-b", "secret", "Estoquista", true)

	first, err := a.createEntry(ctx, movementRequest{
		OperationID: "op-owned-idempotency",
		SKUID: sku, WarehouseID: wh, Quantity: "1", UnitCost: "10", UserID: userA,
	}, "same-request-hash")
	if err != nil {
		t.Fatalf("first movement: %v", err)
	}
	if _, err := a.createEntry(ctx, movementRequest{
		OperationID: "op-owned-idempotency",
		SKUID: sku, WarehouseID: wh, Quantity: "1", UnitCost: "10", UserID: userB,
	}, "same-request-hash"); !errors.Is(err, errOperationConflict) {
		t.Fatalf("cross-user replay must conflict, got %v", err)
	}
	var movementCount int
	if err := a.db.QueryRow(`SELECT count(*) FROM movements WHERE operation_id='op-owned-idempotency'`).Scan(&movementCount); err != nil {
		t.Fatal(err)
	}
	if movementCount != 1 {
		t.Fatalf("movement count=%d want 1", movementCount)
	}
	var owner int64
	if err := a.db.QueryRow(`SELECT user_id FROM movements WHERE id=$1`, first.MovementID).Scan(&owner); err != nil {
		t.Fatal(err)
	}
	if owner != userA {
		t.Fatalf("movement owner=%d want %d", owner, userA)
	}
	assertStock(t, a, sku, wh, "1.000", "10.0000")
}

func TestOperationLookupAllowsOwnerWithMovementPermissionOnly(t *testing.T) {
	a, sku, wh, _ := newStockTestApp(t)
	ctx := context.Background()
	if _, err := a.db.Exec(`
INSERT INTO profile_permissions (profile_id, permission_id)
SELECT p.id,perm.id
FROM profiles p
JOIN permissions perm ON perm.resource='movement_entry' AND perm.action='write'
WHERE p.name='Estoquista'
ON CONFLICT DO NOTHING`); err != nil {
		t.Fatal(err)
	}
	ownerID := createTestUser(t, a, "movement-lookup-owner", "secret", "Estoquista", true)
	otherID := createTestUser(t, a, "movement-lookup-other", "secret", "Estoquista", true)
	if _, err := a.createEntry(ctx, movementRequest{
		OperationID: "op-owner-lookup",
		SKUID: sku, WarehouseID: wh, Quantity: "1", UnitCost: "5", UserID: ownerID,
	}, "lookup-hash"); err != nil {
		t.Fatal(err)
	}
	owner := doLogin(t, a, "movement-lookup-owner", "secret")
	other := doLogin(t, a, "movement-lookup-other", "secret")
	rr := authReq(a, http.MethodGet, "/api/operations/op-owner-lookup", nil, owner, "")
	if rr.Code != http.StatusOK {
		t.Fatalf("owner lookup status=%d body=%s", rr.Code, rr.Body.String())
	}
	if strings.Contains(rr.Body.String(), "user_id") {
		t.Fatalf("operation lookup leaked internal owner: %s", rr.Body.String())
	}
	rr = authReq(a, http.MethodGet, "/api/operations/op-owner-lookup", nil, other, "")
	if rr.Code != http.StatusForbidden {
		t.Fatalf("other operator lookup status=%d body=%s", rr.Code, rr.Body.String())
	}
	if ownerID == otherID {
		t.Fatal("test users unexpectedly identical")
	}
}


func TestCanonicalStockSnapshotReconstructsHistoricalQuantityAndAverageCost(t *testing.T) {
	a, sku, wh1, wh2 := newStockTestApp(t)
	ctx := context.Background()
	mv := func(res operationResult, err error) operationResult { return mustMove2(t, res, err) }

	entry1 := mv(a.createEntry(ctx, movementRequest{OperationID: "report-entry-1", SKUID: sku, WarehouseID: wh1, Quantity: "10", UnitCost: "10"}, "report-h1"))
	entry2 := mv(a.createEntry(ctx, movementRequest{OperationID: "report-entry-2", SKUID: sku, WarehouseID: wh1, Quantity: "10", UnitCost: "14"}, "report-h2"))
	exit1 := mv(a.createExit(ctx, movementRequest{OperationID: "report-exit-1", SKUID: sku, WarehouseID: wh1, Quantity: "5"}, "report-h3"))
	transfer := mv(a.createTransfer(ctx, movementRequest{OperationID: "report-transfer-1", SKUID: sku, FromWarehouseID: wh1, ToWarehouseID: wh2, Quantity: "5"}, "report-h4"))

	times := []struct {
		id int64
		at string
	}{
		{entry1.MovementID, "2026-09-01T10:00:00Z"},
		{entry2.MovementID, "2026-09-10T10:00:00Z"},
		{exit1.MovementID, "2026-09-20T10:00:00Z"},
		{transfer.MovementID, "2026-09-25T10:00:00Z"},
	}
	for _, x := range times {
		if _, err := a.db.Exec(`UPDATE movements SET occurred_at=$1 WHERE id=$2`, x.at, x.id); err != nil {
			t.Fatal(err)
		}
	}

	assertAt := func(cutoff string, warehouseID int64, wantQty, wantAvg string) {
		t.Helper()
		at, err := time.Parse(time.RFC3339, cutoff)
		if err != nil {
			t.Fatal(err)
		}
		s, err := getStockSnapshotAt(ctx, a.db, sku, warehouseID, &at)
		if err != nil {
			t.Fatal(err)
		}
		if s.Quantity != wantQty || s.AvgCost != wantAvg {
			t.Fatalf("cutoff=%s warehouse=%d got qty=%s avg=%s want qty=%s avg=%s", cutoff, warehouseID, s.Quantity, s.AvgCost, wantQty, wantAvg)
		}
	}

	assertAt("2026-09-05T00:00:00Z", wh1, "10.000", "10.0000")
	assertAt("2026-09-15T00:00:00Z", wh1, "20.000", "12.0000")
	assertAt("2026-09-22T00:00:00Z", wh1, "15.000", "12.0000")
	assertAt("2026-09-26T00:00:00Z", wh1, "10.000", "12.0000")
	assertAt("2026-09-26T00:00:00Z", wh2, "5.000", "12.0000")

	current, err := getStockSnapshot(ctx, a.db, sku, wh1)
	if err != nil {
		t.Fatal(err)
	}
	if current.Quantity != "10.000" || current.AvgCost != "12.0000" {
		t.Fatalf("current snapshot changed semantics: qty=%s avg=%s", current.Quantity, current.AvgCost)
	}
}