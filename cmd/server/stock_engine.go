package main

import (
	"context"
	"database/sql"
	"errors"
	"sort"
	"strings"
)

func (a *app) createEntry(ctx context.Context, req movementRequest, hash string) (operationResult, error) {
	return a.inMovementTx(ctx, func(tx *sql.Tx) (operationResult, error) { return createEntryTx(ctx, tx, req, hash) })
}

func createEntryTx(ctx context.Context, tx *sql.Tx, req movementRequest, hash string) (operationResult, error) {
	if req.WarehouseID <= 0 {
		return operationResult{}, errMovementWarehouseRequired
	}
	if !positiveDecimal(req.Quantity) {
		return operationResult{}, errMovementQuantityInvalid
	}
	if !nonNegativeDecimal(req.UnitCost) {
		return operationResult{}, errMovementCostInvalid
	}
	return writeMovementTx(ctx, tx, "ENTRADA", req, hash, func(ctx context.Context, tx *sql.Tx) (int64, error) {
		if err := ensureRefs(ctx, tx, req.SKUID, req.WarehouseID); err != nil {
			return 0, err
		}
		if err := lockStock(ctx, tx, req.SKUID, req.WarehouseID); err != nil {
			return 0, err
		}
		quantity, unitCost, err := canonicalMovementValues(ctx, tx, req.QuantitySemantics, req.SKUID, req.Quantity, req.UnitCost, true)
		if err != nil {
			return 0, err
		}
		m, err := insertMovement(ctx, tx, "ENTRADA", req, hash)
		if err != nil {
			return 0, err
		}
		var lineID int64
		if err := tx.QueryRowContext(ctx, `INSERT INTO movement_lines (movement_id, sku_id, warehouse_id, direction, quantity, unit_cost) VALUES ($1,$2,$3,'IN',$4,$5) RETURNING id`, m, req.SKUID, req.WarehouseID, quantity, unitCost).Scan(&lineID); err != nil {
			return 0, err
		}
		if err := createEntryLot(ctx, tx, lineID); err != nil {
			return 0, err
		}
		return m, nil
	})
}

var errDuplicateEntrySKU = errors.New("duplicate sku in entry lines")

type entryLineRequest struct {
	SKUID    int64  `json:"sku_id"`
	Quantity string `json:"quantity"`
	UnitCost string `json:"unit_cost"`
}

func (a *app) createEntryLines(ctx context.Context, req movementRequest, lines []entryLineRequest, hash string, fiscalKey string) (operationResult, error) {
	if req.WarehouseID <= 0 {
		return operationResult{}, errMovementWarehouseRequired
	}
	if len(lines) == 0 {
		return operationResult{}, errMovementProductRequired
	}
	for i := range lines {
		lines[i].Quantity = normalizeDecimalInput(lines[i].Quantity)
		lines[i].UnitCost = normalizeDecimalInput(lines[i].UnitCost)
	}
	seen := make(map[int64]struct{}, len(lines))
	skuIDs := make([]int64, 0, len(lines))
	for _, line := range lines {
		if line.SKUID <= 0 {
			return operationResult{}, errMovementProductRequired
		}
		if !positiveDecimal(line.Quantity) {
			return operationResult{}, errMovementQuantityInvalid
		}
		if !nonNegativeDecimal(line.UnitCost) {
			return operationResult{}, errMovementCostInvalid
		}
		if _, exists := seen[line.SKUID]; exists {
			return operationResult{}, errDuplicateEntrySKU
		}
		seen[line.SKUID] = struct{}{}
		skuIDs = append(skuIDs, line.SKUID)
	}
	sort.Slice(skuIDs, func(i, j int) bool { return skuIDs[i] < skuIDs[j] })
	return a.writeMovement(ctx, "ENTRADA", req, hash, func(ctx context.Context, tx *sql.Tx) (int64, error) {
		type stockIdentity struct {
			ItemID  int64
			BrandID int64
			SKUID   int64
		}
		identities := make([]stockIdentity, 0, len(skuIDs))
		seenIdentities := make(map[[2]int64]struct{}, len(skuIDs))
		for _, skuID := range skuIDs {
			if err := ensureRefs(ctx, tx, skuID, req.WarehouseID); err != nil {
				return 0, err
			}
			var itemID, brandID int64
			if err := tx.QueryRowContext(ctx, `SELECT item_id,brand_id FROM product_skus WHERE id=$1`, skuID).Scan(&itemID,&brandID); err != nil {
				return 0, err
			}
			key := [2]int64{itemID,brandID}
			if _, exists := seenIdentities[key]; !exists {
				seenIdentities[key] = struct{}{}
				identities = append(identities, stockIdentity{ItemID:itemID,BrandID:brandID,SKUID:skuID})
			}
		}
		sort.Slice(identities, func(i,j int) bool {
			if identities[i].ItemID==identities[j].ItemID { return identities[i].BrandID<identities[j].BrandID }
			return identities[i].ItemID<identities[j].ItemID
		})
		for _, identity := range identities {
			if err := lockStock(ctx, tx, identity.SKUID, req.WarehouseID); err != nil {
				return 0, err
			}
		}
		m, err := insertMovement(ctx, tx, "ENTRADA", req, hash)
		if err != nil {
			return 0, err
		}
		for _, line := range lines {
			quantity, unitCost, err := canonicalMovementValues(ctx, tx, req.QuantitySemantics, line.SKUID, line.Quantity, line.UnitCost, true)
			if err != nil {
				return 0, err
			}
			var lineID int64
			if err := tx.QueryRowContext(ctx, `INSERT INTO movement_lines (movement_id, sku_id, warehouse_id, direction, quantity, unit_cost) VALUES ($1,$2,$3,'IN',$4,$5) RETURNING id`, m, line.SKUID, req.WarehouseID, quantity, unitCost).Scan(&lineID); err != nil {
				return 0, err
			}
			if err := createEntryLot(ctx, tx, lineID); err != nil {
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
	return a.inMovementTx(ctx, func(tx *sql.Tx) (operationResult, error) { return createExitTx(ctx, tx, req, hash) })
}

func createExitTx(ctx context.Context, tx *sql.Tx, req movementRequest, hash string) (operationResult, error) {
	if req.WarehouseID <= 0 {
		return operationResult{}, errMovementWarehouseRequired
	}
	if !positiveDecimal(req.Quantity) {
		return operationResult{}, errMovementQuantityInvalid
	}
	return writeMovementTx(ctx, tx, "SAIDA", req, hash, func(ctx context.Context, tx *sql.Tx) (int64, error) {
		if err := ensureRefs(ctx, tx, req.SKUID, req.WarehouseID); err != nil {
			return 0, err
		}
		if err := lockStock(ctx, tx, req.SKUID, req.WarehouseID); err != nil {
			return 0, err
		}
		quantity, _, err := canonicalMovementValues(ctx, tx, req.QuantitySemantics, req.SKUID, req.Quantity, "", false)
		if err != nil {
			return 0, err
		}
		s, err := getStockSnapshot(ctx, tx, req.SKUID, req.WarehouseID)
		if err != nil {
			return 0, err
		}
		if !quantityAtLeast(ctx, tx, s.Quantity, quantity) {
			return 0, insufficientStockError{Available: s.Quantity, Unit: s.Unit}
		}
		m, err := insertMovement(ctx, tx, "SAIDA", req, hash)
		if err != nil {
			return 0, err
		}
		var lineID int64
		if err := tx.QueryRowContext(ctx, `INSERT INTO movement_lines (movement_id, sku_id, warehouse_id, direction, quantity, unit_cost) VALUES ($1,$2,$3,'OUT',$4,$5) RETURNING id`, m, req.SKUID, req.WarehouseID, quantity, s.AvgCost).Scan(&lineID); err != nil {
			return 0, err
		}
		if err := allocateLots(ctx, tx, lineID); err != nil {
			return 0, err
		}
		return m, nil
	})
}

func (a *app) createTransfer(ctx context.Context, req movementRequest, hash string) (operationResult, error) {
	return a.inMovementTx(ctx, func(tx *sql.Tx) (operationResult, error) { return createTransferTx(ctx, tx, req, hash) })
}

func createTransferTx(ctx context.Context, tx *sql.Tx, req movementRequest, hash string) (operationResult, error) {
	if req.FromWarehouseID <= 0 || req.ToWarehouseID <= 0 {
		return operationResult{}, errTransferWarehousesRequired
	}
	if req.FromWarehouseID == req.ToWarehouseID {
		return operationResult{}, errTransferSameWarehouse
	}
	if !positiveDecimal(req.Quantity) {
		return operationResult{}, errMovementQuantityInvalid
	}
	return writeMovementTx(ctx, tx, "TRANSFERENCIA", req, hash, func(ctx context.Context, tx *sql.Tx) (int64, error) {
		if err := ensureSKU(ctx, tx, req.SKUID); err != nil {
			return 0, err
		}
		if err := lockActiveWarehouses(ctx, tx, req.FromWarehouseID, req.ToWarehouseID); err != nil {
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
		quantity, _, err := canonicalMovementValues(ctx, tx, req.QuantitySemantics, req.SKUID, req.Quantity, "", false)
		if err != nil {
			return 0, err
		}
		s, err := getStockSnapshot(ctx, tx, req.SKUID, req.FromWarehouseID)
		if err != nil {
			return 0, err
		}
		if !quantityAtLeast(ctx, tx, s.Quantity, quantity) {
			return 0, insufficientStockError{Available: s.Quantity, Unit: s.Unit}
		}
		req.WarehouseID = req.FromWarehouseID
		m, err := insertMovement(ctx, tx, "TRANSFERENCIA", req, hash)
		if err != nil {
			return 0, err
		}
		var outLineID, inLineID int64
		if err := tx.QueryRowContext(ctx, `INSERT INTO movement_lines (movement_id, sku_id, warehouse_id, direction, quantity, unit_cost) VALUES ($1,$2,$3,'OUT',$4,$5) RETURNING id`, m, req.SKUID, req.FromWarehouseID, quantity, s.AvgCost).Scan(&outLineID); err != nil {
			return 0, err
		}
		if err := tx.QueryRowContext(ctx, `INSERT INTO movement_lines (movement_id, sku_id, warehouse_id, direction, quantity, unit_cost) VALUES ($1,$2,$3,'IN',$4,$5) RETURNING id`, m, req.SKUID, req.ToWarehouseID, quantity, s.AvgCost).Scan(&inLineID); err != nil {
			return 0, err
		}
		if err := allocateLots(ctx, tx, outLineID); err != nil {
			return 0, err
		}
		if err := createTransferLots(ctx, tx, outLineID, inLineID); err != nil {
			return 0, err
		}
		return m, nil
	})
}

func (a *app) writeMovement(ctx context.Context, typ string, req movementRequest, hash string, fn func(context.Context, *sql.Tx) (int64, error)) (operationResult, error) {
	tx, err := a.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	if err != nil {
		return operationResult{}, err
	}
	defer tx.Rollback()
	res, err := writeMovementTx(ctx, tx, typ, req, hash, fn)
	if err != nil {
		return operationResult{}, err
	}
	if err := tx.Commit(); err != nil {
		return operationResult{}, err
	}
	return res, nil
}

func writeMovementTx(ctx context.Context, tx *sql.Tx, typ string, req movementRequest, hash string, fn func(context.Context, *sql.Tx) (int64, error)) (operationResult, error) {
	var existingHash string
	var existingID int64
	var existingUser sql.NullInt64
	err := tx.QueryRowContext(ctx, `SELECT id, request_hash, user_id FROM movements WHERE operation_id=$1`, req.OperationID).Scan(&existingID, &existingHash, &existingUser)
	if err == nil {
		if existingHash != hash || (req.UserID > 0 && existingUser.Valid && existingUser.Int64 != req.UserID) {
			return operationResult{}, errOperationConflict
		}
		return operationResult{OperationID: req.OperationID, MovementID: existingID, Status: "confirmed", Type: typ, UserID: existingUser.Int64}, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return operationResult{}, err
	}
	movementID, err := fn(ctx, tx)
	if err != nil {
		return operationResult{}, err
	}
	return operationResult{OperationID: req.OperationID, MovementID: movementID, Status: "confirmed", Type: typ, UserID: req.UserID}, nil
}

func insertMovement(ctx context.Context, tx *sql.Tx, typ string, req movementRequest, hash string) (int64, error) {
	var id int64
	err := tx.QueryRowContext(ctx, `INSERT INTO movements (operation_id, request_hash, movement_type, document_number, supplier_name, user_id, notes) VALUES ($1,$2,$3,NULLIF($4,''),NULLIF($5,''),NULLIF($6,0),NULLIF($7,'')) RETURNING id`, req.OperationID, hash, typ, req.DocumentNumber, req.SupplierName, req.UserID, req.Notes).Scan(&id)
	return id, err
}

func createEntryLot(ctx context.Context, tx *sql.Tx, movementLineID int64) error {
	_, err := tx.ExecContext(ctx, `
INSERT INTO stock_lots (item_id,brand_id,warehouse_id,source_movement_line_id,initial_quantity)
SELECT ml.item_id,s.brand_id,ml.warehouse_id,ml.id,ml.quantity
FROM movement_lines ml
JOIN product_skus s ON s.id=ml.sku_id
WHERE ml.id=$1 AND ml.direction='IN'`, movementLineID)
	return err
}

func allocateLots(ctx context.Context, tx *sql.Tx, movementLineID int64) error {
	res, err := tx.ExecContext(ctx, `
WITH target AS (
    SELECT ml.item_id,s.brand_id,ml.warehouse_id,ml.quantity
    FROM movement_lines ml
    JOIN product_skus s ON s.id=ml.sku_id
    WHERE ml.id=$1 AND ml.direction='OUT'
),
lot_balances AS (
    SELECT l.id,l.received_at,l.expires_at,
           l.initial_quantity-COALESCE(sum(a.quantity),0) AS remaining
    FROM stock_lots l
    JOIN target t ON t.item_id=l.item_id AND t.brand_id=l.brand_id AND t.warehouse_id=l.warehouse_id
    LEFT JOIN stock_lot_allocations a ON a.stock_lot_id=l.id
    GROUP BY l.id,l.received_at,l.expires_at,l.initial_quantity
    HAVING l.initial_quantity-COALESCE(sum(a.quantity),0) > 0
),
ordered AS (
    SELECT id,remaining,
           COALESCE(sum(remaining) OVER (
               ORDER BY (expires_at IS NULL),expires_at,received_at,id
               ROWS BETWEEN UNBOUNDED PRECEDING AND 1 PRECEDING
           ),0) AS consumed_before
    FROM lot_balances
),
chosen AS (
    SELECT o.id AS stock_lot_id,
           LEAST(o.remaining,GREATEST(0,t.quantity-o.consumed_before)) AS quantity
    FROM ordered o CROSS JOIN target t
    WHERE o.consumed_before < t.quantity
)
INSERT INTO stock_lot_allocations (movement_line_id,stock_lot_id,quantity)
SELECT $1,stock_lot_id,quantity
FROM chosen
WHERE quantity > 0`, movementLineID)
	if err != nil {
		return err
	}
	_ = res

	var requested, allocated string
	if err := tx.QueryRowContext(ctx, `
SELECT ml.quantity::text,
       COALESCE(sum(a.quantity),0)::text
FROM movement_lines ml
LEFT JOIN stock_lot_allocations a ON a.movement_line_id=ml.id
WHERE ml.id=$1
GROUP BY ml.id`, movementLineID).Scan(&requested, &allocated); err != nil {
		return err
	}
	var complete bool
	if err := tx.QueryRowContext(ctx, `SELECT $1::numeric=$2::numeric`, requested, allocated).Scan(&complete); err != nil {
		return err
	}
	if !complete {
		return errors.New("lot allocation does not cover movement quantity")
	}
	return nil
}

func createTransferLots(ctx context.Context, tx *sql.Tx, outLineID, inLineID int64) error {
	_, err := tx.ExecContext(ctx, `
INSERT INTO stock_lots (
    item_id,brand_id,warehouse_id,source_movement_line_id,source_lot_id,initial_quantity,
    manufacturer_lot_code,manufactured_at,expires_at,received_at
)
SELECT dest.item_id,src.brand_id,dest.warehouse_id,dest.id,a.stock_lot_id,a.quantity,
       src.manufacturer_lot_code,src.manufactured_at,src.expires_at,src.received_at
FROM stock_lot_allocations a
JOIN stock_lots src ON src.id=a.stock_lot_id
JOIN movement_lines dest ON dest.id=$2 AND dest.direction='IN'
WHERE a.movement_line_id=$1`, outLineID, inLineID)
	return err
}

func canonicalMovementValues(ctx context.Context, tx *sql.Tx, semantics string, skuID int64, quantity, unitCost string, withCost bool) (string, string, error) {
	if semantics == "" {
		return quantity, unitCost, nil
	}
	if semantics != presentationQuantitySemantics {
		return "", "", errValidation
	}
	return normalizePresentation(ctx, tx, skuID, quantity, unitCost, withCost)
}

func normalizePresentation(ctx context.Context, tx *sql.Tx, skuID int64, quantity, unitCost string, withCost bool) (string, string, error) {
	var canonicalQuantity, canonicalUnitCost string
	if withCost {
		err := tx.QueryRowContext(ctx, `
SELECT
  ($2::numeric * content_quantity)::numeric(14,3)::text,
  CASE WHEN $3::numeric = 0 THEN '0.0000'
       ELSE ($3::numeric / content_quantity)::numeric(14,4)::text
  END
FROM product_skus
WHERE id=$1`, skuID, quantity, unitCost).Scan(&canonicalQuantity, &canonicalUnitCost)
		if errors.Is(err, sql.ErrNoRows) {
			return "", "", errNotFound
		}
		return canonicalQuantity, canonicalUnitCost, err
	}
	err := tx.QueryRowContext(ctx, `
SELECT ($2::numeric * content_quantity)::numeric(14,3)::text
FROM product_skus
WHERE id=$1`, skuID, quantity).Scan(&canonicalQuantity)
	if errors.Is(err, sql.ErrNoRows) {
		return "", "", errNotFound
	}
	return canonicalQuantity, "", err
}

func lockStock(ctx context.Context, tx *sql.Tx, skuID, warehouseID int64) error {
	var itemID, brandID int64
	if err := tx.QueryRowContext(ctx, `SELECT item_id,brand_id FROM product_skus WHERE id=$1`, skuID).Scan(&itemID,&brandID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return errNotFound
		}
		return err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO stock_locks (item_id,brand_id,warehouse_id) VALUES ($1,$2,$3) ON CONFLICT DO NOTHING`, itemID,brandID,warehouseID); err != nil {
		return err
	}
	var x int
	return tx.QueryRowContext(ctx, `SELECT 1 FROM stock_locks WHERE item_id=$1 AND brand_id=$2 AND warehouse_id=$3 FOR UPDATE`, itemID,brandID,warehouseID).Scan(&x)
}

func ensureRefs(ctx context.Context, tx *sql.Tx, skuID, warehouseID int64) error {
	if err := ensureSKU(ctx, tx, skuID); err != nil {
		return err
	}
	return lockActiveWarehouses(ctx, tx, warehouseID)
}

func ensureSKU(ctx context.Context, tx *sql.Tx, skuID int64) error {
	var ok bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM product_skus WHERE id=$1)`, skuID).Scan(&ok); err != nil {
		return err
	}
	if !ok {
		return errNotFound
	}
	return nil
}

func lockActiveWarehouses(ctx context.Context, tx *sql.Tx, warehouseIDs ...int64) error {
	ids := append([]int64(nil), warehouseIDs...)
	if len(ids) == 2 && ids[0] > ids[1] {
		ids[0], ids[1] = ids[1], ids[0]
	}
	for _, id := range ids {
		var active bool
		err := tx.QueryRowContext(ctx, `SELECT active FROM warehouses WHERE id=$1 FOR UPDATE`, id).Scan(&active)
		if errors.Is(err, sql.ErrNoRows) {
			return errNotFound
		}
		if err != nil {
			return err
		}
		if !active {
			return errWarehouseInactive
		}
	}
	return nil
}

// inMovementTx executa fn em uma transação; qualquer erro desfaz tudo.
func (a *app) inMovementTx(ctx context.Context, fn func(*sql.Tx) (operationResult, error)) (operationResult, error) {
	tx, err := a.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	if err != nil {
		return operationResult{}, err
	}
	defer tx.Rollback()
	res, err := fn(tx)
	if err != nil {
		return operationResult{}, err
	}
	if err := tx.Commit(); err != nil {
		return operationResult{}, err
	}
	return res, nil
}
