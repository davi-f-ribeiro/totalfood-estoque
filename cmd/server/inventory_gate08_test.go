package main

import (
	"context"
	"errors"
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
