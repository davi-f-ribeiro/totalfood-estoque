package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"sort"
	"time"
)

var (
	errInventoryWarehouseNotFound   = errors.New("inventory warehouse not found")
	errInventoryWarehouseInactive   = errors.New("inventory warehouse inactive")
	errInventoryNoEligibleSKUs      = errors.New("inventory warehouse has no eligible skus")
	errInventoryNoBlindWarehouse    = errors.New("no warehouse eligible for blind inventory")
	errInventoryValidationNotFound  = errors.New("inventory validation not found")
)

type inventoryQueryer interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

type inventoryEligibilityRow struct {
	SKUID     int64
	Balance   string
	SKUActive bool
	ItemActive bool
	Eligible  bool
	Reason    string
}

type inventorySnapshotLine struct {
	SKUID    int64
	Quantity string
}

type inventorySelection struct {
	ID          int64   `json:"id"`
	Type        string  `json:"type"`
	WarehouseID int64   `json:"warehouse_id"`
	Status      string  `json:"status"`
	SKUIDs      []int64 `json:"sku_ids"`
}

type totalInventoryRequest struct {
	WarehouseID int64 `json:"warehouse_id"`
}

func (a *app) registerInventoryHandlers(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/inventory-validations/total", a.requireAuth("inventory_validation", "write", a.handleCreateTotalInventory))
	mux.HandleFunc("POST /api/inventory-validations/blind", a.requireAuth("inventory_validation", "write", a.handleCreateBlindInventory))
	mux.HandleFunc("GET /api/inventory-validations/{id}", a.requireAuth("inventory_validation", "read", a.handleGetInventoryValidation))
	a.registerInventoryCountingHandlers(mux)
	a.registerInventoryReviewHandlers(mux)
	a.registerInventoryTerminalHandlers(mux)
}

func (a *app) handleCreateTotalInventory(w http.ResponseWriter, r *http.Request) {
	var req totalInventoryRequest
	if !decodeInventoryJSON(w, r, &req, false) {
		return
	}
	if req.WarehouseID <= 0 {
		writeJSONError(w, http.StatusBadRequest, "warehouse_id inválido.")
		return
	}
	res, err := a.createTotalInventory(r.Context(), userFromContext(r.Context()).ID, req.WarehouseID)
	writeInventoryResponse(w, res, err)
}

func (a *app) handleCreateBlindInventory(w http.ResponseWriter, r *http.Request) {
	var req struct{}
	if !decodeInventoryJSON(w, r, &req, true) {
		return
	}
	res, err := a.createBlindInventory(r.Context(), userFromContext(r.Context()).ID)
	writeInventoryResponse(w, res, err)
}

func (a *app) handleGetInventoryValidation(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	res, err := a.getInventoryValidation(r.Context(), id)
	if err != nil {
		if errors.Is(err, errInventoryValidationNotFound) {
			writeJSONError(w, http.StatusNotFound, "Inventário não encontrado.")
			return
		}
		log.Printf("inventory lookup failed validation_id=%d error=%v", id, err)
		writeJSONError(w, http.StatusInternalServerError, "Erro interno.")
		return
	}
	writeJSON(w, http.StatusOK, res)
}

func decodeInventoryJSON(w http.ResponseWriter, r *http.Request, dst any, allowEmpty bool) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	defer r.Body.Close()
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		if allowEmpty && errors.Is(err, io.EOF) {
			return true
		}
		writeJSONError(w, http.StatusBadRequest, "JSON inválido.")
		return false
	}
	var extra any
	if err := dec.Decode(&extra); !errors.Is(err, io.EOF) {
		writeJSONError(w, http.StatusBadRequest, "JSON inválido.")
		return false
	}
	return true
}

func writeInventoryResponse(w http.ResponseWriter, res inventorySelection, err error) {
	if err == nil {
		writeJSON(w, http.StatusCreated, res)
		return
	}
	switch {
	case errors.Is(err, errInventoryWarehouseNotFound):
		writeJSONError(w, http.StatusNotFound, "Warehouse não encontrado.")
	case errors.Is(err, errInventoryWarehouseInactive):
		writeJSONError(w, http.StatusConflict, "Warehouse inativo.")
	case errors.Is(err, errInventoryNoEligibleSKUs):
		writeJSONError(w, http.StatusConflict, "Warehouse sem SKUs elegíveis para inventário.")
	case errors.Is(err, errInventoryNoBlindWarehouse):
		writeJSONError(w, http.StatusConflict, "Nenhum warehouse elegível para inventário BLIND.")
	default:
		log.Printf("inventory create failed error=%v", err)
		writeJSONError(w, http.StatusInternalServerError, "Erro interno.")
	}
}

func (a *app) createTotalInventory(ctx context.Context, responsibleUserID, warehouseID int64) (inventorySelection, error) {
	tx, snapshotAt, err := a.beginInventorySnapshotTx(ctx)
	if err != nil {
		return inventorySelection{}, err
	}
	defer tx.Rollback()

	if err := lockInventorySelection(ctx, tx); err != nil {
		return inventorySelection{}, err
	}
	if err := validateInventoryWarehouse(ctx, tx, warehouseID); err != nil {
		return inventorySelection{}, err
	}
	rows, err := inventoryEligibility(ctx, tx, warehouseID)
	if err != nil {
		return inventorySelection{}, err
	}
	lines := make([]inventorySnapshotLine, 0, len(rows))
	for _, row := range rows {
		if row.Eligible {
			lines = append(lines, inventorySnapshotLine{SKUID: row.SKUID, Quantity: row.Balance})
		}
	}
	if len(lines) == 0 {
		return inventorySelection{}, errInventoryNoEligibleSKUs
	}
	sort.Slice(lines, func(i, j int) bool { return lines[i].SKUID < lines[j].SKUID })

	res, err := persistInventorySelection(ctx, tx, "TOTAL", responsibleUserID, warehouseID, snapshotAt, lines)
	if err != nil {
		return inventorySelection{}, err
	}
	if err := tx.Commit(); err != nil {
		return inventorySelection{}, err
	}
	return res, nil
}

func (a *app) createBlindInventory(ctx context.Context, responsibleUserID int64) (inventorySelection, error) {
	tx, snapshotAt, err := a.beginInventorySnapshotTx(ctx)
	if err != nil {
		return inventorySelection{}, err
	}
	defer tx.Rollback()

	if err := lockInventorySelection(ctx, tx); err != nil {
		return inventorySelection{}, err
	}
	warehouseID, eligibleCount, err := selectBlindWarehouse(ctx, tx)
	if err != nil {
		return inventorySelection{}, err
	}
	sampleSize := blindSampleSize(eligibleCount)
	if sampleSize < 1 {
		return inventorySelection{}, errInventoryNoBlindWarehouse
	}
	lines, err := selectBlindSKUs(ctx, tx, warehouseID, sampleSize)
	if err != nil {
		return inventorySelection{}, err
	}
	if len(lines) != sampleSize {
		return inventorySelection{}, errInventoryNoEligibleSKUs
	}
	res, err := persistInventorySelection(ctx, tx, "BLIND", responsibleUserID, warehouseID, snapshotAt, lines)
	if err != nil {
		return inventorySelection{}, err
	}
	if err := tx.Commit(); err != nil {
		return inventorySelection{}, err
	}
	return res, nil
}

func (a *app) beginInventorySnapshotTx(ctx context.Context) (*sql.Tx, time.Time, error) {
	tx, err := a.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead})
	if err != nil {
		return nil, time.Time{}, err
	}
	var snapshotAt time.Time
	var snapshotID string
	if err := tx.QueryRowContext(ctx, `SELECT statement_timestamp(), pg_current_snapshot()::text`).Scan(&snapshotAt, &snapshotID); err != nil {
		_ = tx.Rollback()
		return nil, time.Time{}, err
	}
	return tx, snapshotAt, nil
}

func lockInventorySelection(ctx context.Context, tx *sql.Tx) error {
	_, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtext('inventory_validation_selection'))`)
	return err
}

func validateInventoryWarehouse(ctx context.Context, q interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, warehouseID int64) error {
	var active bool
	err := q.QueryRowContext(ctx, `SELECT active FROM warehouses WHERE id=$1`, warehouseID).Scan(&active)
	if errors.Is(err, sql.ErrNoRows) {
		return errInventoryWarehouseNotFound
	}
	if err != nil {
		return err
	}
	if !active {
		return errInventoryWarehouseInactive
	}
	return nil
}

func inventoryEligibility(ctx context.Context, q inventoryQueryer, warehouseID int64) ([]inventoryEligibilityRow, error) {
	rows, err := q.QueryContext(ctx, `
SELECT s.id,
       COALESCE(sum(CASE WHEN ml.direction='IN' THEN ml.quantity ELSE -ml.quantity END),0)::text AS balance,
       s.active,
       i.active,
       COALESCE(sum(CASE WHEN ml.direction='IN' THEN ml.quantity ELSE -ml.quantity END),0) > 0 AS positive_balance,
       COALESCE(sum(CASE WHEN ml.direction='IN' THEN ml.quantity ELSE -ml.quantity END),0) < 0 AS negative_balance
FROM product_skus s
JOIN inventory_items i ON i.id=s.item_id
LEFT JOIN movement_lines ml ON ml.sku_id=s.id AND ml.warehouse_id=$1
GROUP BY s.id,i.id
ORDER BY s.id`, warehouseID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []inventoryEligibilityRow{}
	for rows.Next() {
		var row inventoryEligibilityRow
		var positive, negative bool
		if err := rows.Scan(&row.SKUID, &row.Balance, &row.SKUActive, &row.ItemActive, &positive, &negative); err != nil {
			return nil, err
		}
		switch {
		case !row.SKUActive:
			row.Reason = "sku_inactive"
		case !row.ItemActive:
			row.Reason = "item_inactive"
		case negative:
			row.Reason = "negative_balance"
		case !positive:
			row.Reason = "zero_balance"
		default:
			row.Eligible = true
			row.Reason = "eligible"
		}
		out = append(out, row)
	}
	return out, rows.Err()
}

func blindSampleSize(eligibleCount int) int {
	if eligibleCount < 2 {
		return 0
	}
	target := 40
	switch {
	case eligibleCount <= 10:
		target = eligibleCount / 2
	case eligibleCount <= 30:
		target = 10
	case eligibleCount <= 60:
		target = 15
	case eligibleCount <= 100:
		target = 20
	case eligibleCount <= 200:
		target = 30
	default:
		target = 40
	}
	maxSample := eligibleCount / 2
	if target > maxSample {
		return maxSample
	}
	return target
}

func selectBlindWarehouse(ctx context.Context, tx *sql.Tx) (int64, int, error) {
	var warehouseID int64
	var eligibleCount int
	err := tx.QueryRowContext(ctx, `
WITH balances AS (
    SELECT w.id AS warehouse_id,
           s.id AS sku_id,
           s.active AS sku_active,
           i.active AS item_active,
           COALESCE(sum(CASE WHEN ml.direction='IN' THEN ml.quantity ELSE -ml.quantity END),0) AS balance
    FROM warehouses w
    CROSS JOIN product_skus s
    JOIN inventory_items i ON i.id=s.item_id
    LEFT JOIN movement_lines ml ON ml.sku_id=s.id AND ml.warehouse_id=w.id
    WHERE w.active=true
    GROUP BY w.id,s.id,i.id
),
eligible_counts AS (
    SELECT warehouse_id, count(*)::int AS eligible_count
    FROM balances
    WHERE sku_active=true AND item_active=true AND balance>0
    GROUP BY warehouse_id
    HAVING count(*) >= 2
),
history AS (
    SELECT warehouse_id, max(created_at) AS last_blind
    FROM inventory_validations
    WHERE type='BLIND'
    GROUP BY warehouse_id
)
SELECT e.warehouse_id,e.eligible_count
FROM eligible_counts e
LEFT JOIN history h ON h.warehouse_id=e.warehouse_id
ORDER BY (h.last_blind IS NOT NULL), h.last_blind ASC NULLS FIRST, random()
LIMIT 1`).Scan(&warehouseID, &eligibleCount)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, 0, errInventoryNoBlindWarehouse
	}
	return warehouseID, eligibleCount, err
}

func selectBlindSKUs(ctx context.Context, tx *sql.Tx, warehouseID int64, sampleSize int) ([]inventorySnapshotLine, error) {
	rows, err := tx.QueryContext(ctx, `
WITH balances AS (
    SELECT s.id AS sku_id,
           s.active AS sku_active,
           i.active AS item_active,
           COALESCE(sum(CASE WHEN ml.direction='IN' THEN ml.quantity ELSE -ml.quantity END),0) AS balance
    FROM product_skus s
    JOIN inventory_items i ON i.id=s.item_id
    LEFT JOIN movement_lines ml ON ml.sku_id=s.id AND ml.warehouse_id=$1
    GROUP BY s.id,i.id
),
history AS (
    SELECT l.sku_id, max(v.created_at) AS last_selected
    FROM inventory_validation_lines l
    JOIN inventory_validations v ON v.id=l.validation_id
    GROUP BY l.sku_id
)
SELECT b.sku_id, b.balance::text
FROM balances b
LEFT JOIN history h ON h.sku_id=b.sku_id
WHERE b.sku_active=true AND b.item_active=true AND b.balance>0
ORDER BY (h.last_selected IS NOT NULL), h.last_selected ASC NULLS FIRST, random()
LIMIT $2`, warehouseID, sampleSize)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]inventorySnapshotLine, 0, sampleSize)
	for rows.Next() {
		var line inventorySnapshotLine
		if err := rows.Scan(&line.SKUID, &line.Quantity); err != nil {
			return nil, err
		}
		out = append(out, line)
	}
	return out, rows.Err()
}

func persistInventorySelection(ctx context.Context, tx *sql.Tx, typ string, responsibleUserID, warehouseID int64, snapshotAt time.Time, lines []inventorySnapshotLine) (inventorySelection, error) {
	if len(lines) == 0 {
		return inventorySelection{}, errInventoryNoEligibleSKUs
	}
	var id int64
	if err := tx.QueryRowContext(ctx, `
INSERT INTO inventory_validations (type,warehouse_id,status,responsible_user_id,started_at,snapshot_at)
VALUES ($1,$2,'OPEN',$3,$4,$4)
RETURNING id`, typ, warehouseID, responsibleUserID, snapshotAt).Scan(&id); err != nil {
		return inventorySelection{}, err
	}
	skuIDs := make([]int64, 0, len(lines))
	for _, line := range lines {
		if line.Quantity == "" {
			return inventorySelection{}, errors.New("inventory snapshot quantity is empty")
		}
		if _, err := tx.ExecContext(ctx, `
INSERT INTO inventory_validation_lines (validation_id,sku_id,system_quantity_snapshot,status)
VALUES ($1,$2,$3::numeric,'OPEN')`, id, line.SKUID, line.Quantity); err != nil {
			return inventorySelection{}, err
		}
		skuIDs = append(skuIDs, line.SKUID)
	}
	return inventorySelection{ID: id, Type: typ, WarehouseID: warehouseID, Status: "OPEN", SKUIDs: skuIDs}, nil
}

func (a *app) getInventoryValidation(ctx context.Context, id int64) (inventorySelection, error) {
	var res inventorySelection
	if err := a.db.QueryRowContext(ctx, `
SELECT id,type,warehouse_id,status
FROM inventory_validations
WHERE id=$1`, id).Scan(&res.ID, &res.Type, &res.WarehouseID, &res.Status); errors.Is(err, sql.ErrNoRows) {
		return inventorySelection{}, errInventoryValidationNotFound
	} else if err != nil {
		return inventorySelection{}, err
	}
	rows, err := a.db.QueryContext(ctx, `
SELECT sku_id FROM inventory_validation_lines
WHERE validation_id=$1 ORDER BY id`, id)
	if err != nil {
		return inventorySelection{}, err
	}
	defer rows.Close()
	for rows.Next() {
		var skuID int64
		if err := rows.Scan(&skuID); err != nil {
			return inventorySelection{}, err
		}
		res.SKUIDs = append(res.SKUIDs, skuID)
	}
	return res, rows.Err()
}
