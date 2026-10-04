package main

import (
	"context"
	"database/sql"
	"errors"
)

var (
	errInventoryAdjustmentNotEligible = errors.New("inventory adjustment is not eligible")
	errInventoryAdjustmentAlreadyApplied = errors.New("inventory adjustment already applied")
	errInventoryAdjustmentExecutionPending = errors.New("inventory adjustment execution not implemented")
)

type inventoryAdjustmentCandidate struct {
	ValidationID      int64
	SKUID             int64
	WarehouseID       int64
	SnapshotQuantity  string
	PhysicalQuantity  string
	Difference        string
}

func (a *app) applyInventoryStockAdjustment(ctx context.Context, validationID, skuID, userID int64) (operationResult, error) {
	tx, err := a.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	if err != nil {
		return operationResult{}, err
	}
	defer tx.Rollback()

	candidate, err := inventoryAdjustmentCandidateTx(ctx, tx, validationID, skuID)
	if err != nil {
		return operationResult{}, err
	}
	if err := lockStock(ctx, tx, candidate.SKUID, candidate.WarehouseID); err != nil {
		return operationResult{}, err
	}

	var adjustmentID int64
	err = tx.QueryRowContext(ctx, `
SELECT id
FROM inventory_stock_adjustments
WHERE validation_id=$1 AND sku_id=$2
FOR UPDATE`, validationID, skuID).Scan(&adjustmentID)
	switch {
	case err == nil:
		return operationResult{}, errInventoryAdjustmentAlreadyApplied
	case !errors.Is(err, sql.ErrNoRows):
		return operationResult{}, err
	}

	// Gate 4 owns the actual IN/OUT movement and lot mutation. Keeping this
	// transaction uncommitted here proves eligibility without changing stock.
	_ = userID
	return operationResult{}, errInventoryAdjustmentExecutionPending
}

func inventoryAdjustmentCandidateTx(ctx context.Context, tx *sql.Tx, validationID, skuID int64) (inventoryAdjustmentCandidate, error) {
	var candidate inventoryAdjustmentCandidate
	var status string
	if err := tx.QueryRowContext(ctx, `
SELECT status,warehouse_id
FROM inventory_validations
WHERE id=$1
FOR UPDATE`, validationID).Scan(&status, &candidate.WarehouseID); errors.Is(err, sql.ErrNoRows) {
		return inventoryAdjustmentCandidate{}, errInventoryValidationNotFound
	} else if err != nil {
		return inventoryAdjustmentCandidate{}, err
	}
	if status != "COMPLETED" {
		return inventoryAdjustmentCandidate{}, errInventoryAdjustmentNotEligible
	}

	candidate.ValidationID = validationID
	candidate.SKUID = skuID
	if err := tx.QueryRowContext(ctx, `
SELECT l.system_quantity_snapshot::text,
       COALESCE(r.quantity,l.physical_quantity)::text,
       (COALESCE(r.quantity,l.physical_quantity)-l.system_quantity_snapshot)::text
FROM inventory_validation_lines l
LEFT JOIN inventory_validation_recounts r
  ON r.validation_id=l.validation_id AND r.sku_id=l.sku_id
WHERE l.validation_id=$1 AND l.sku_id=$2
FOR UPDATE OF l`, validationID, skuID).Scan(
		&candidate.SnapshotQuantity,
		&candidate.PhysicalQuantity,
		&candidate.Difference,
	); errors.Is(err, sql.ErrNoRows) {
		return inventoryAdjustmentCandidate{}, errInventoryLineNotFound
	} else if err != nil {
		return inventoryAdjustmentCandidate{}, err
	}

	var divergent bool
	if err := tx.QueryRowContext(ctx, `SELECT $1::numeric <> 0`, candidate.Difference).Scan(&divergent); err != nil {
		return inventoryAdjustmentCandidate{}, err
	}
	if !divergent {
		return inventoryAdjustmentCandidate{}, errInventoryAdjustmentNotEligible
	}
	return candidate, nil
}
