package main

import (
	"context"
	"errors"
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

func TestApplyInventoryStockAdjustmentGate3DoesNotMutateStock(t *testing.T) {
	e, inv, sku := completedDivergentAdjustmentInventory(t, "no-mutation")
	ctx := context.Background()

	var before int
	if err := e.db.QueryRow(`SELECT count(*) FROM movements`).Scan(&before); err != nil {
		t.Fatal(err)
	}
	_, err := e.a.applyInventoryStockAdjustment(ctx, inv.ID, sku, e.userID)
	if !errors.Is(err, errInventoryAdjustmentExecutionPending) {
		t.Fatalf("apply error=%v, want %v", err, errInventoryAdjustmentExecutionPending)
	}
	var after int
	if err := e.db.QueryRow(`SELECT count(*) FROM movements`).Scan(&after); err != nil {
		t.Fatal(err)
	}
	if after != before {
		t.Fatalf("movements changed in gate 3: before=%d after=%d", before, after)
	}
	var adjustments int
	if err := e.db.QueryRow(`SELECT count(*) FROM inventory_stock_adjustments`).Scan(&adjustments); err != nil {
		t.Fatal(err)
	}
	if adjustments != 0 {
		t.Fatalf("adjustments=%d, want 0", adjustments)
	}
}
