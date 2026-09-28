package main

import (
	"context"
	"database/sql"
	"errors"
	"strings"
)

func (a *app) createEntry(ctx context.Context, req movementRequest, hash string) (operationResult, error) {
	if req.WarehouseID <= 0 || !positiveDecimal(req.Quantity) || !nonNegativeDecimal(req.UnitCost) {
		return operationResult{}, errValidation
	}
	return a.writeMovement(ctx, "ENTRADA", req, hash, func(ctx context.Context, tx *sql.Tx) (int64, error) {
		if err := ensureRefs(ctx, tx, req.SKUID, req.WarehouseID); err != nil {
			return 0, err
		}
		if err := lockStock(ctx, tx, req.SKUID, req.WarehouseID); err != nil {
			return 0, err
		}
		m, err := insertMovement(ctx, tx, "ENTRADA", req, hash)
		if err != nil {
			return 0, err
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO movement_lines (movement_id, sku_id, warehouse_id, direction, quantity, unit_cost) VALUES ($1,$2,$3,'IN',$4,$5)`, m, req.SKUID, req.WarehouseID, req.Quantity, req.UnitCost)
		return m, err
	})
}

type entryLineRequest struct {
	SKUID    int64  `json:"sku_id"`
	Quantity string `json:"quantity"`
	UnitCost string `json:"unit_cost"`
}

func (a *app) createEntryLines(ctx context.Context, req movementRequest, lines []entryLineRequest, hash string, fiscalKey string) (operationResult, error) {
	if req.WarehouseID <= 0 || len(lines) == 0 {
		return operationResult{}, errValidation
	}
	for _, line := range lines {
		if line.SKUID <= 0 || !positiveDecimal(line.Quantity) || !nonNegativeDecimal(line.UnitCost) {
			return operationResult{}, errValidation
		}
	}
	return a.writeMovement(ctx, "ENTRADA", req, hash, func(ctx context.Context, tx *sql.Tx) (int64, error) {
		for _, line := range lines {
			if err := ensureRefs(ctx, tx, line.SKUID, req.WarehouseID); err != nil {
				return 0, err
			}
		}
		for _, line := range lines {
			if err := lockStock(ctx, tx, line.SKUID, req.WarehouseID); err != nil {
				return 0, err
			}
		}
		m, err := insertMovement(ctx, tx, "ENTRADA", req, hash)
		if err != nil {
			return 0, err
		}
		for _, line := range lines {
			if _, err := tx.ExecContext(ctx, `INSERT INTO movement_lines (movement_id, sku_id, warehouse_id, direction, quantity, unit_cost) VALUES ($1,$2,$3,'IN',$4,$5)`, m, line.SKUID, req.WarehouseID, line.Quantity, line.UnitCost); err != nil {
				return 0, err
			}
		}
		if fiscalKey != "" {
			if _, err := tx.ExecContext(ctx, `INSERT INTO fiscal_documents (access_key, operation_id, movement_id) VALUES ($1,$2,$3)`, fiscalKey, req.OperationID, m); err != nil {
				if strings.Contains(err.Error(), "fiscal_documents_access_key_key") {
					return 0, errDuplicateFiscalDocument
				}
				return 0, err
			}
		}
		return m, nil
	})
}

func (a *app) createExit(ctx context.Context, req movementRequest, hash string) (operationResult, error) {
	if req.WarehouseID <= 0 || !positiveDecimal(req.Quantity) {
		return operationResult{}, errValidation
	}
	return a.writeMovement(ctx, "SAIDA", req, hash, func(ctx context.Context, tx *sql.Tx) (int64, error) {
		if err := ensureRefs(ctx, tx, req.SKUID, req.WarehouseID); err != nil {
			return 0, err
		}
		if err := lockStock(ctx, tx, req.SKUID, req.WarehouseID); err != nil {
			return 0, err
		}
		s, err := getStockSnapshot(ctx, tx, req.SKUID, req.WarehouseID)
		if err != nil {
			return 0, err
		}
		if !quantityAtLeast(ctx, tx, s.Quantity, req.Quantity) {
			return 0, insufficientStockError{Available: s.Quantity, Unit: s.Unit}
		}
		m, err := insertMovement(ctx, tx, "SAIDA", req, hash)
		if err != nil {
			return 0, err
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO movement_lines (movement_id, sku_id, warehouse_id, direction, quantity, unit_cost) VALUES ($1,$2,$3,'OUT',$4,$5)`, m, req.SKUID, req.WarehouseID, req.Quantity, s.AvgCost)
		return m, err
	})
}

func (a *app) createTransfer(ctx context.Context, req movementRequest, hash string) (operationResult, error) {
	if req.FromWarehouseID <= 0 || req.ToWarehouseID <= 0 || req.FromWarehouseID == req.ToWarehouseID || !positiveDecimal(req.Quantity) {
		return operationResult{}, errValidation
	}
	return a.writeMovement(ctx, "TRANSFERENCIA", req, hash, func(ctx context.Context, tx *sql.Tx) (int64, error) {
		if err := ensureRefs(ctx, tx, req.SKUID, req.FromWarehouseID); err != nil {
			return 0, err
		}
		if err := ensureRefs(ctx, tx, req.SKUID, req.ToWarehouseID); err != nil {
			return 0, err
		}
		if req.FromWarehouseID < req.ToWarehouseID {
			if err := lockStock(ctx, tx, req.SKUID, req.FromWarehouseID); err != nil {
				return 0, err
			}
			if err := lockStock(ctx, tx, req.SKUID, req.ToWarehouseID); err != nil {
				return 0, err
			}
		} else {
			if err := lockStock(ctx, tx, req.SKUID, req.ToWarehouseID); err != nil {
				return 0, err
			}
			if err := lockStock(ctx, tx, req.SKUID, req.FromWarehouseID); err != nil {
				return 0, err
			}
		}
		s, err := getStockSnapshot(ctx, tx, req.SKUID, req.FromWarehouseID)
		if err != nil {
			return 0, err
		}
		if !quantityAtLeast(ctx, tx, s.Quantity, req.Quantity) {
			return 0, insufficientStockError{Available: s.Quantity, Unit: s.Unit}
		}
		req.WarehouseID = req.FromWarehouseID
		m, err := insertMovement(ctx, tx, "TRANSFERENCIA", req, hash)
		if err != nil {
			return 0, err
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO movement_lines (movement_id, sku_id, warehouse_id, direction, quantity, unit_cost) VALUES ($1,$2,$3,'OUT',$4,$5),($1,$2,$6,'IN',$4,$5)`, m, req.SKUID, req.FromWarehouseID, req.Quantity, s.AvgCost, req.ToWarehouseID)
		return m, err
	})
}

func (a *app) writeMovement(ctx context.Context, typ string, req movementRequest, hash string, fn func(context.Context, *sql.Tx) (int64, error)) (operationResult, error) {
	tx, err := a.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	if err != nil {
		return operationResult{}, err
	}
	defer tx.Rollback()
	var existingHash string
	var existingID int64
	err = tx.QueryRowContext(ctx, `SELECT id, request_hash FROM movements WHERE operation_id=$1`, req.OperationID).Scan(&existingID, &existingHash)
	if err == nil {
		if existingHash != hash {
			return operationResult{}, errOperationConflict
		}
		return operationResult{OperationID: req.OperationID, MovementID: existingID, Status: "confirmed", Type: typ}, tx.Commit()
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return operationResult{}, err
	}
	movementID, err := fn(ctx, tx)
	if err != nil {
		return operationResult{}, err
	}
	if err := tx.Commit(); err != nil {
		return operationResult{}, err
	}
	return operationResult{OperationID: req.OperationID, MovementID: movementID, Status: "confirmed", Type: typ}, nil
}

func insertMovement(ctx context.Context, tx *sql.Tx, typ string, req movementRequest, hash string) (int64, error) {
	var id int64
	err := tx.QueryRowContext(ctx, `INSERT INTO movements (operation_id, request_hash, movement_type, document_number, supplier_name, user_id, notes) VALUES ($1,$2,$3,NULLIF($4,''),NULLIF($5,''),NULLIF($6,0),NULLIF($7,'')) RETURNING id`, req.OperationID, hash, typ, req.DocumentNumber, req.SupplierName, req.UserID, req.Notes).Scan(&id)
	return id, err
}

func lockStock(ctx context.Context, tx *sql.Tx, skuID, warehouseID int64) error {
	if _, err := tx.ExecContext(ctx, `INSERT INTO stock_locks (sku_id, warehouse_id) VALUES ($1,$2) ON CONFLICT DO NOTHING`, skuID, warehouseID); err != nil {
		return err
	}
	var x int
	return tx.QueryRowContext(ctx, `SELECT 1 FROM stock_locks WHERE sku_id=$1 AND warehouse_id=$2 FOR UPDATE`, skuID, warehouseID).Scan(&x)
}

func ensureRefs(ctx context.Context, tx *sql.Tx, skuID, warehouseID int64) error {
	var ok bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM product_skus WHERE id=$1)`, skuID).Scan(&ok); err != nil || !ok {
		return errNotFound
	}
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM warehouses WHERE id=$1)`, warehouseID).Scan(&ok); err != nil || !ok {
		return errNotFound
	}
	return nil
}
