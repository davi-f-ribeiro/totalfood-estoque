package main

import (
	"context"
	"database/sql"
	"errors"
	"testing"
)

func TestInventoryAdjustmentCandidateRequiresCompletedDivergence(t *testing.T) {
	db := openTestDB(t)
	seedCanonicalStockTest(t, db)

	ctx := context.Background()
	var userID, warehouseID, skuID int64
	if err := db.QueryRow(`SELECT id FROM users ORDER BY id LIMIT 1`).Scan(&userID); err != nil { t.Fatal(err) }
	if err := db.QueryRow(`SELECT id FROM warehouses WHERE active ORDER BY id LIMIT 1`).Scan(&warehouseID); err != nil { t.Fatal(err) }
	if err := db.QueryRow(`SELECT id FROM product_skus ORDER BY id LIMIT 1`).Scan(&skuID); err != nil { t.Fatal(err) }

	var validationID int64
	if err := db.QueryRow(`
INSERT INTO inventory_validations (type,warehouse_id,status,responsible_user_id,started_at,snapshot_at)
VALUES ('TOTAL',$1,'REVIEW',$2,now(),now()) RETURNING id`, warehouseID, userID).Scan(&validationID); err != nil { t.Fatal(err) }
	if _, err := db.Exec(`
INSERT INTO inventory_validation_lines
(validation_id,sku_id,item_id,system_quantity_snapshot,physical_quantity,status,counted_by_user_id,counted_at)
SELECT $1,s.id,s.item_id,10,8,'COUNTED',$2,now()
FROM product_skus s WHERE s.id=$3`, validationID, userID, skuID); err != nil { t.Fatal(err) }

	tx, err := db.BeginTx(ctx, nil)
	if err != nil { t.Fatal(err) }
	_, err = inventoryAdjustmentCandidateTx(ctx, tx, validationID, skuID)
	_ = tx.Rollback()
	if !errors.Is(err, errInventoryAdjustmentNotEligible) {
		t.Fatalf("REVIEW candidate error=%v, want %v", err, errInventoryAdjustmentNotEligible)
	}

	if _, err := db.Exec(`UPDATE inventory_validations SET status='COMPLETED',completed_at=now(),completed_by_user_id=$2 WHERE id=$1`, validationID, userID); err != nil { t.Fatal(err) }
	tx, err = db.BeginTx(ctx, nil)
	if err != nil { t.Fatal(err) }
	candidate, err := inventoryAdjustmentCandidateTx(ctx, tx, validationID, skuID)
	_ = tx.Rollback()
	if err != nil { t.Fatal(err) }
	if candidate.Difference != "-2.000" {
		t.Fatalf("difference=%q, want -2.000", candidate.Difference)
	}

	if _, err := db.Exec(`UPDATE inventory_validation_lines SET physical_quantity=10 WHERE validation_id=$1 AND sku_id=$2`, validationID, skuID); err != nil { t.Fatal(err) }
	tx, err = db.BeginTx(ctx, nil)
	if err != nil { t.Fatal(err) }
	_, err = inventoryAdjustmentCandidateTx(ctx, tx, validationID, skuID)
	_ = tx.Rollback()
	if !errors.Is(err, errInventoryAdjustmentNotEligible) {
		t.Fatalf("matching candidate error=%v, want %v", err, errInventoryAdjustmentNotEligible)
	}
}

func TestApplyInventoryStockAdjustmentGate3DoesNotMutateStock(t *testing.T) {
	db := openTestDB(t)
	seedCanonicalStockTest(t, db)
	a := &app{db: db}
	ctx := context.Background()

	var userID, warehouseID, skuID int64
	if err := db.QueryRow(`SELECT id FROM users ORDER BY id LIMIT 1`).Scan(&userID); err != nil { t.Fatal(err) }
	if err := db.QueryRow(`SELECT id FROM warehouses WHERE active ORDER BY id LIMIT 1`).Scan(&warehouseID); err != nil { t.Fatal(err) }
	if err := db.QueryRow(`SELECT id FROM product_skus ORDER BY id LIMIT 1`).Scan(&skuID); err != nil { t.Fatal(err) }

	var validationID int64
	if err := db.QueryRow(`
INSERT INTO inventory_validations (type,warehouse_id,status,responsible_user_id,started_at,snapshot_at,completed_at,completed_by_user_id)
VALUES ('TOTAL',$1,'COMPLETED',$2,now(),now(),now(),$2) RETURNING id`, warehouseID, userID).Scan(&validationID); err != nil { t.Fatal(err) }
	if _, err := db.Exec(`
INSERT INTO inventory_validation_lines
(validation_id,sku_id,item_id,system_quantity_snapshot,physical_quantity,status,counted_by_user_id,counted_at)
SELECT $1,s.id,s.item_id,10,8,'COUNTED',$2,now()
FROM product_skus s WHERE s.id=$3`, validationID, userID, skuID); err != nil { t.Fatal(err) }

	var before int
	if err := db.QueryRow(`SELECT count(*) FROM movements`).Scan(&before); err != nil { t.Fatal(err) }
	_, err := a.applyInventoryStockAdjustment(ctx, validationID, skuID, userID)
	if !errors.Is(err, errInventoryAdjustmentExecutionPending) {
		t.Fatalf("apply error=%v, want %v", err, errInventoryAdjustmentExecutionPending)
	}
	var after int
	if err := db.QueryRow(`SELECT count(*) FROM movements`).Scan(&after); err != nil { t.Fatal(err) }
	if after != before {
		t.Fatalf("movements changed in gate 3: before=%d after=%d", before, after)
	}
	var adjustments int
	if err := db.QueryRow(`SELECT count(*) FROM inventory_stock_adjustments`).Scan(&adjustments); err != nil { t.Fatal(err) }
	if adjustments != 0 {
		t.Fatalf("adjustments=%d, want 0", adjustments)
	}
}

var _ *sql.DB
