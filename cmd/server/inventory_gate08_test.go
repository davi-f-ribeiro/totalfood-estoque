package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func secondInventoryOperator(t *testing.T, e inventoryTestEnv, suffix string) int64 {
	t.Helper()
	var id int64
	err := e.db.QueryRow(`
INSERT INTO users (login,password_hash,profile_id,active,must_change_password)
SELECT $1,password_hash,profile_id,true,false
FROM users
WHERE id=$2
RETURNING id`, "inventory-concurrent-"+suffix+"-"+t.Name(), e.userID).Scan(&id)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func TestInventoryCountSameQuantityStaleVersionFromDifferentOperatorConflicts(t *testing.T) {
	e := newInventoryTestEnv(t)
	_, inv, _ := setupTotalCounting(t, e, 2, "9.000")
	view, err := e.a.getInventoryCountingView(context.Background(), inv.ID)
	if err != nil {
		t.Fatal(err)
	}
	line := view.Lines[0]
	if _, _, err := e.a.saveInventoryCount(context.Background(), inv.ID, line.SKUID, "10.000", line.Version, e.userID); err != nil {
		t.Fatalf("operator A count: %v", err)
	}
	other := secondInventoryOperator(t, e, "count")
	if _, _, err := e.a.saveInventoryCount(context.Background(), inv.ID, line.SKUID, "10.000", line.Version, other); !errors.Is(err, errInventoryCountConflict) {
		t.Fatalf("stale same-quantity operator B error=%v want conflict", err)
	}
}

func TestInventoryRecountSameQuantityStaleVersionFromDifferentOperatorConflicts(t *testing.T) {
	e := newInventoryTestEnv(t)
	_, inv, skus := setupTotalCounting(t, e, 2, "10.000")
	countInventoryToReview(t, e, inv, map[int64]string{
		skus[0]: "8.000",
		skus[1]: "10.000",
	})
	view, err := e.a.getInventoryReviewView(context.Background(), inv.ID)
	if err != nil {
		t.Fatal(err)
	}
	line := reviewBySKU(t, view)[skus[0]]
	if _, err := e.a.saveInventoryRecount(context.Background(), inv.ID, skus[0], "9.000", line.Version, e.userID); err != nil {
		t.Fatalf("operator A recount: %v", err)
	}
	other := secondInventoryOperator(t, e, "recount")
	if _, err := e.a.saveInventoryRecount(context.Background(), inv.ID, skus[0], "9.000", line.Version, other); !errors.Is(err, errInventoryCountConflict) {
		t.Fatalf("stale same-quantity operator B error=%v want conflict", err)
	}
}

func TestCancelledTotalDoesNotClassifyUncountedLineAsDivergent(t *testing.T) {
	e := newInventoryTestEnv(t)
	_, inv, _ := setupTotalCounting(t, e, 2, "10.000")
	history, err := e.a.cancelInventory(context.Background(), inv.ID, e.userID, "Cancelamento antes da primeira contagem.")
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range history.Lines {
		if line.FirstQuantity != nil {
			t.Fatalf("line %d unexpectedly counted: %+v", line.SKUID, line)
		}
		if line.State != nil || line.EffectiveQuantity != nil || line.Difference != nil {
			t.Fatalf("uncounted line %d was classified: %+v", line.SKUID, line)
		}
	}
}

func TestGate08InventoryMigrationsIncrementalAndStartupIdempotent(t *testing.T) {
	if os.Getenv("TEST_DATABASE_URL") == "" {
		t.Skip("TEST_DATABASE_URL is required")
	}
	ctx := context.Background()
	db := openTestDatabase(t)
	migrationsDir := filepath.Join("..", "..", "migrations")
	baseDir := t.TempDir()
	for i := 1; i <= 13; i++ {
		matches, err := filepath.Glob(filepath.Join(migrationsDir, fmt.Sprintf("%03d_*.sql", i)))
		if err != nil || len(matches) != 1 {
			t.Fatalf("migration %03d matches=%v err=%v", i, matches, err)
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
		t.Fatalf("production-base migrations: %v", err)
	}

	var movementsBefore, movementLinesBefore int64
	if err := db.QueryRowContext(ctx, "SELECT count(*) FROM movements").Scan(&movementsBefore); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRowContext(ctx, "SELECT count(*) FROM movement_lines").Scan(&movementLinesBefore); err != nil {
		t.Fatal(err)
	}

	incrementalDir := t.TempDir()
	for i := 14; i <= 18; i++ {
		matches, err := filepath.Glob(filepath.Join(migrationsDir, fmt.Sprintf("%03d_*.sql", i)))
		if err != nil || len(matches) != 1 {
			t.Fatalf("migration %03d matches=%v err=%v", i, matches, err)
		}
		body, err := os.ReadFile(matches[0])
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(incrementalDir, filepath.Base(matches[0])), body, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := runMigrations(ctx, db, incrementalDir); err != nil {
		t.Fatalf("incremental 014-018: %v", err)
	}
	var appliedBefore int
	if err := db.QueryRowContext(ctx, "SELECT count(*) FROM schema_migrations").Scan(&appliedBefore); err != nil {
		t.Fatal(err)
	}
	if err := runMigrations(ctx, db, incrementalDir); err != nil {
		t.Fatalf("idempotent startup rerun: %v", err)
	}
	var appliedAfter int
	if err := db.QueryRowContext(ctx, "SELECT count(*) FROM schema_migrations").Scan(&appliedAfter); err != nil {
		t.Fatal(err)
	}
	if appliedBefore != appliedAfter {
		t.Fatalf("startup reapplied migrations: %d -> %d", appliedBefore, appliedAfter)
	}

	var movementsAfter, movementLinesAfter int64
	if err := db.QueryRowContext(ctx, "SELECT count(*) FROM movements").Scan(&movementsAfter); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRowContext(ctx, "SELECT count(*) FROM movement_lines").Scan(&movementLinesAfter); err != nil {
		t.Fatal(err)
	}
	if movementsBefore != movementsAfter || movementLinesBefore != movementLinesAfter {
		t.Fatalf("inventory migrations changed movement data: movements %d->%d lines %d->%d", movementsBefore, movementsAfter, movementLinesBefore, movementLinesAfter)
	}
}

func inventoryStockBySKUWarehouse(t *testing.T, e inventoryTestEnv) map[string]string {
	t.Helper()
	rows, err := e.db.Query(`
SELECT sku_id::text || ':' || warehouse_id::text,
       sum(CASE WHEN direction='IN' THEN quantity ELSE -quantity END)::text
FROM movement_lines
GROUP BY sku_id,warehouse_id
ORDER BY sku_id,warehouse_id`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var key, qty string
		if err := rows.Scan(&key, &qty); err != nil {
			t.Fatal(err)
		}
		out[key] = qty
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestGate08FullLifecycleDoesNotChangeStockBySKUWarehouse(t *testing.T) {
	e := newInventoryTestEnv(t)
	ctx := context.Background()

	totalWH := e.warehouse(t, "gate08-total", true)
	totalA := e.sku(t, "gate08-total-a", true, true)
	totalB := e.sku(t, "gate08-total-b", true, true)
	e.movementDecimal(t, totalA, totalWH, "IN", "10.000")
	e.movementDecimal(t, totalB, totalWH, "IN", "7.500")

	blindWH := e.warehouse(t, "gate08-blind", true)
	for i := 0; i < 4; i++ {
		sku := e.sku(t, fmt.Sprintf("gate08-blind-%d", i), true, true)
		e.movementDecimal(t, sku, blindWH, "IN", fmt.Sprintf("%d.250", i+1))
	}

	var movementsBefore, linesBefore int64
	if err := e.db.QueryRow("SELECT count(*) FROM movements").Scan(&movementsBefore); err != nil {
		t.Fatal(err)
	}
	if err := e.db.QueryRow("SELECT count(*) FROM movement_lines").Scan(&linesBefore); err != nil {
		t.Fatal(err)
	}
	stockBefore := inventoryStockBySKUWarehouse(t, e)

	total, err := e.a.createTotalInventory(ctx, e.userID, totalWH)
	if err != nil {
		t.Fatal(err)
	}
	countInventoryToReview(t, e, total, map[int64]string{totalA: "8.000", totalB: "7.500"})
	review, err := e.a.getInventoryReviewView(ctx, total.ID)
	if err != nil {
		t.Fatal(err)
	}
	div := reviewBySKU(t, review)[totalA]
	if _, err := e.a.saveInventoryRecount(ctx, total.ID, totalA, "9.000", div.Version, e.userID); err != nil {
		t.Fatal(err)
	}
	if _, err := e.a.completeInventory(ctx, total.ID, e.userID, "Divergência confirmada no Gate 08."); err != nil {
		t.Fatal(err)
	}
	if _, err := e.a.getInventoryHistory(ctx, total.ID); err != nil {
		t.Fatal(err)
	}

	// Keep the stocked TOTAL warehouse active; make only its SKUs ineligible
	// so BLIND selection deterministically chooses the dedicated warehouse.
	if _, err := e.db.Exec("UPDATE product_skus SET active=false WHERE id IN ($1,$2)", totalA, totalB); err != nil {
		t.Fatal(err)
	}
	blind, err := e.a.createBlindInventory(ctx, e.userID)
	if err != nil {
		t.Fatal(err)
	}
	if blind.WarehouseID != blindWH {
		t.Fatalf("blind warehouse=%d want=%d", blind.WarehouseID, blindWH)
	}
	counting, err := e.a.getInventoryCountingView(ctx, blind.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(counting.Lines) == 0 {
		t.Fatal("blind selection is empty")
	}
	line := counting.Lines[0]
	if _, _, err := e.a.saveInventoryCount(ctx, blind.ID, line.SKUID, "0.000", line.Version, e.userID); err != nil {
		t.Fatal(err)
	}
	if _, err := e.a.cancelInventory(ctx, blind.ID, e.userID, "Cancelamento cego durante Gate 08."); err != nil {
		t.Fatal(err)
	}
	if _, err := e.a.getInventoryHistory(ctx, blind.ID); err != nil {
		t.Fatal(err)
	}

	var movementsAfter, linesAfter int64
	if err := e.db.QueryRow("SELECT count(*) FROM movements").Scan(&movementsAfter); err != nil {
		t.Fatal(err)
	}
	if err := e.db.QueryRow("SELECT count(*) FROM movement_lines").Scan(&linesAfter); err != nil {
		t.Fatal(err)
	}
	stockAfter := inventoryStockBySKUWarehouse(t, e)
	if movementsBefore != movementsAfter || linesBefore != linesAfter || !reflect.DeepEqual(stockBefore, stockAfter) {
		t.Fatalf("inventory lifecycle changed stock: movements %d->%d lines %d->%d stock before=%v after=%v", movementsBefore, movementsAfter, linesBefore, linesAfter, stockBefore, stockAfter)
	}
}
