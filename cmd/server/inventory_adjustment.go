package main

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
)

var (
	errInventoryAdjustmentNotEligible = errors.New("inventory adjustment is not eligible")
	errInventoryAdjustmentAlreadyApplied = errors.New("inventory adjustment already applied")
	errInventoryAdjustmentCostUnavailable = errors.New("inventory adjustment cost basis unavailable")
)

type inventoryAdjustmentCandidate struct {
	ValidationID      int64
	SKUID             int64
	WarehouseID       int64
	SnapshotQuantity  string
	PhysicalQuantity  string
	Difference        string
}

func (a *app) applyInventoryStockAdjustment(ctx context.Context, validationID, skuID, userID int64, operationID string) (operationResult, error) {
	operationID = strings.TrimSpace(operationID)
	if operationID == "" {
		return operationResult{}, errMovementOperationIDMissing
	}
	tx, err := a.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	if err != nil {
		return operationResult{}, err
	}
	defer tx.Rollback()

	candidate, err := inventoryAdjustmentCandidateTx(ctx, tx, validationID, skuID)
	if err != nil {
		return operationResult{}, err
	}
	if err := lockActiveWarehouses(ctx, tx, candidate.WarehouseID); err != nil {
		return operationResult{}, err
	}
	if err := lockStock(ctx, tx, candidate.SKUID, candidate.WarehouseID); err != nil {
		return operationResult{}, err
	}

	var existingMovementID int64
	var existingOperationID string
	var existingUserID sql.NullInt64
	err = tx.QueryRowContext(ctx, `
SELECT a.movement_id,m.operation_id,m.user_id
FROM inventory_stock_adjustments a
JOIN movements m ON m.id=a.movement_id
WHERE a.validation_id=$1 AND a.sku_id=$2
FOR UPDATE OF a`, validationID, skuID).Scan(&existingMovementID, &existingOperationID, &existingUserID)
	switch {
	case err == nil:
		if existingOperationID == operationID && (!existingUserID.Valid || existingUserID.Int64 == userID) {
			return operationResult{OperationID: operationID, MovementID: existingMovementID, Status: "confirmed", Type: "AJUSTE", UserID: existingUserID.Int64}, tx.Commit()
		}
		return operationResult{}, errInventoryAdjustmentAlreadyApplied
	case !errors.Is(err, sql.ErrNoRows):
		return operationResult{}, err
	}

	var positive bool
	var quantity string
	if err := tx.QueryRowContext(ctx,
		`SELECT $1::numeric > 0, abs($1::numeric)::numeric(14,3)::text`,
		candidate.Difference).Scan(&positive, &quantity); err != nil {
		return operationResult{}, err
	}

	snapshot, err := getStockSnapshot(ctx, tx, candidate.SKUID, candidate.WarehouseID)
	if err != nil {
		return operationResult{}, err
	}
	if !positive && !quantityAtLeast(ctx, tx, snapshot.Quantity, quantity) {
		return operationResult{}, insufficientStockError{Available: snapshot.Quantity, Unit: snapshot.Unit}
	}

	unitCost := snapshot.AvgCost
	if positive {
		unitCost, err = adjustmentUnitCostTx(ctx, tx, candidate.SKUID, candidate.WarehouseID, snapshot)
		if err != nil {
			return operationResult{}, err
		}
	}

	hashBytes := sha256.Sum256([]byte(fmt.Sprintf("inventory-adjustment:%d:%d:%s", validationID, skuID, candidate.Difference)))
	hash := hex.EncodeToString(hashBytes[:])
	req := movementRequest{
		OperationID: operationID,
		SKUID: candidate.SKUID,
		WarehouseID: candidate.WarehouseID,
		Quantity: quantity,
		UnitCost: unitCost,
		UserID: userID,
		Notes: fmt.Sprintf("Ajuste da auditoria de estoque #%d.", validationID),
	}
	res, err := writeMovementTx(ctx, tx, "AJUSTE", req, hash, func(ctx context.Context, tx *sql.Tx) (int64, error) {
		movementID, err := insertMovement(ctx, tx, "AJUSTE", req, hash)
		if err != nil {
			return 0, err
		}
		direction := "OUT"
		if positive {
			direction = "IN"
		}
		var lineID int64
		if err := tx.QueryRowContext(ctx, `
INSERT INTO movement_lines (movement_id,sku_id,warehouse_id,direction,quantity,unit_cost)
VALUES ($1,$2,$3,$4,$5,$6)
RETURNING id`, movementID, candidate.SKUID, candidate.WarehouseID, direction, quantity, unitCost).Scan(&lineID); err != nil {
			return 0, err
		}
		if positive {
			if err := createEntryLot(ctx, tx, lineID); err != nil {
				return 0, err
			}
		} else if err := allocateLots(ctx, tx, lineID); err != nil {
			return 0, err
		}
		if _, err := tx.ExecContext(ctx, `
INSERT INTO inventory_stock_adjustments
(validation_id,sku_id,movement_id,approved_by_user_id)
VALUES ($1,$2,$3,$4)`, validationID, skuID, movementID, userID); err != nil {
			return 0, err
		}
		return movementID, nil
	})
	if err != nil {
		return operationResult{}, err
	}
	if err := tx.Commit(); err != nil {
		return operationResult{}, err
	}
	return res, nil
}

func adjustmentUnitCostTx(ctx context.Context, tx *sql.Tx, skuID, warehouseID int64, snapshot stockSnapshot) (string, error) {
	var usable bool
	if err := tx.QueryRowContext(ctx, `SELECT $1::numeric > 0 AND $2::numeric > 0`, snapshot.Quantity, snapshot.AvgCost).Scan(&usable); err != nil {
		return "", err
	}
	if usable {
		return snapshot.AvgCost, nil
	}
	var unitCost string
	err := tx.QueryRowContext(ctx, `
SELECT ml.unit_cost::numeric(14,4)::text
FROM movement_lines ml
JOIN movements m ON m.id=ml.movement_id
JOIN product_skus movement_sku ON movement_sku.id=ml.sku_id
JOIN product_skus requested_sku ON requested_sku.id=$1
WHERE m.movement_type='ENTRADA'
  AND ml.direction='IN'
  AND ml.unit_cost > 0
  AND ml.item_id=requested_sku.item_id
  AND movement_sku.brand_id=requested_sku.brand_id
ORDER BY (ml.warehouse_id=$2) DESC,m.occurred_at DESC,ml.id DESC
LIMIT 1`, skuID, warehouseID).Scan(&unitCost)
	if errors.Is(err, sql.ErrNoRows) {
		return "", errInventoryAdjustmentCostUnavailable
	}
	return unitCost, err
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
