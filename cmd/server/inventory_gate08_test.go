package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
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
