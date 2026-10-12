package main

import (
	"context"
	"database/sql"
	"errors"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"
)

var (
	errInventoryNotInReview    = errors.New("inventory is not in review")
	errInventoryNotDivergent   = errors.New("inventory line is not divergent")
	errInventoryRecountExists  = errors.New("inventory recount already exists")
)

type inventoryReviewLine struct {
	SKUID             int64      `json:"sku_id"`
	Code              string     `json:"code"`
	Description       string     `json:"description"`
	Unit              string     `json:"unit"`
	Barcode           string     `json:"barcode,omitempty"`
	FirstQuantity     string     `json:"first_quantity"`
	FirstCountedAt    *time.Time `json:"first_counted_at"`
	FirstCountedBy    string     `json:"first_counted_by,omitempty"`
	SnapshotQuantity  string     `json:"reference_quantity"`
	EffectiveQuantity string     `json:"effective_quantity"`
	Difference        string     `json:"difference"`
	State             string     `json:"state"`
	RecountQuantity   *string    `json:"recount_quantity"`
	RecountedAt       *time.Time `json:"recounted_at"`
	RecountedBy       string     `json:"recounted_by,omitempty"`
	Version           int64      `json:"version"`
	RecountAvailable  bool       `json:"recount_available"`
}

type inventoryReviewView struct {
	ID          int64                 `json:"id"`
	Type        string                `json:"type"`
	WarehouseID int64                 `json:"warehouse_id"`
	Warehouse   string                `json:"warehouse"`
	Status      string                `json:"status"`
	Lines       []inventoryReviewLine `json:"lines"`
}

type inventoryRecountRequest struct {
	PhysicalQuantity string `json:"physical_quantity"`
	ExpectedVersion  int64  `json:"expected_version"`
}

func (a *app) registerInventoryReviewHandlers(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/inventory-validations/{id}/review", a.requireAuth("inventory_validation", "read", a.handleGetInventoryReview))
	mux.HandleFunc("PUT /api/inventory-validations/{id}/lines/{sku_id}/recount", a.requireAuth("inventory_validation", "write", a.handleSaveInventoryRecount))
}

func (a *app) handleGetInventoryReview(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	view, err := a.getInventoryReviewView(r.Context(), id)
	switch {
	case err == nil:
		writeJSON(w, http.StatusOK, view)
	case errors.Is(err, errInventoryValidationNotFound):
		writeJSONError(w, http.StatusNotFound, "Contagem não encontrada.")
	case errors.Is(err, errInventoryNotInReview):
		writeJSONError(w, http.StatusConflict, "A contagem ainda não está em revisão.")
	default:
		log.Printf("inventory review lookup failed validation_id=%d error=%v", id, err)
		writeJSONError(w, http.StatusInternalServerError, "Erro interno.")
	}
}

func (a *app) handleSaveInventoryRecount(w http.ResponseWriter, r *http.Request) {
	validationID, ok := pathID(w, r)
	if !ok {
		return
	}
	skuID, err := strconv.ParseInt(strings.TrimSpace(r.PathValue("sku_id")), 10, 64)
	if err != nil || skuID <= 0 {
		writeJSONError(w, http.StatusBadRequest, "Produto inválido.")
		return
	}
	var req inventoryRecountRequest
	if !decodeInventoryJSON(w, r, &req, false) {
		return
	}
	req.PhysicalQuantity = normalizeDecimalInput(req.PhysicalQuantity)
	if !validPhysicalQuantity(req.PhysicalQuantity) || req.ExpectedVersion < 0 {
		writeJSONError(w, http.StatusBadRequest, "Quantidade contada inválida.")
		return
	}
	line, err := a.saveInventoryRecount(r.Context(), validationID, skuID, req.PhysicalQuantity, req.ExpectedVersion, userFromContext(r.Context()).ID)
	switch {
	case err == nil:
		writeJSON(w, http.StatusOK, map[string]any{"line": line})
	case errors.Is(err, errInventoryValidationNotFound), errors.Is(err, errInventoryLineNotFound):
		writeJSONError(w, http.StatusNotFound, "Item da contagem não encontrado.")
	case errors.Is(err, errInventoryNotInReview):
		writeJSONError(w, http.StatusConflict, "A contagem ainda não está em revisão.")
	case errors.Is(err, errInventoryNotDivergent):
		writeJSONError(w, http.StatusConflict, "Só itens com divergência podem ser recontados.")
	case errors.Is(err, errInventoryRecountExists):
		writeJSONError(w, http.StatusConflict, "Este item já foi recontado.")
	case errors.Is(err, errInventoryCountConflict):
		writeJSONError(w, http.StatusConflict, "Outra pessoa alterou este item. Atualize a página antes de salvar.")
	default:
		log.Printf("inventory recount save failed validation_id=%d sku_id=%d error=%v", validationID, skuID, err)
		writeJSONError(w, http.StatusInternalServerError, "Erro interno.")
	}
}

func (a *app) getInventoryReviewView(ctx context.Context, id int64) (inventoryReviewView, error) {
	var out inventoryReviewView
	if err := a.db.QueryRowContext(ctx, `
SELECT v.id,v.type,v.warehouse_id,w.name,v.status
FROM inventory_validations v
JOIN warehouses w ON w.id=v.warehouse_id
WHERE v.id=$1`, id).Scan(&out.ID, &out.Type, &out.WarehouseID, &out.Warehouse, &out.Status); errors.Is(err, sql.ErrNoRows) {
		return inventoryReviewView{}, errInventoryValidationNotFound
	} else if err != nil {
		return inventoryReviewView{}, err
	}
	if out.Status != "REVIEW" {
		return inventoryReviewView{}, errInventoryNotInReview
	}
	rows, err := a.db.QueryContext(ctx, reviewLinesSQL, id)
	if err != nil {
		return inventoryReviewView{}, err
	}
	defer rows.Close()
	out.Lines = []inventoryReviewLine{}
	for rows.Next() {
		line, err := scanInventoryReviewLine(rows)
		if err != nil {
			return inventoryReviewView{}, err
		}
		out.Lines = append(out.Lines, line)
	}
	return out, rows.Err()
}

const reviewLinesSQL = `
SELECT l.sku_id,
       COALESCE(NULLIF(s.name,''),NULLIF(s.brand,''),s.id::text),
       i.name,
       i.unit_code,
       COALESCE(s.barcode,''),
       l.physical_quantity::text,
       l.counted_at,
       COALESCE(cu.login,''),
       l.system_quantity_snapshot::text,
       COALESCE(r.quantity,l.physical_quantity)::text,
       (COALESCE(r.quantity,l.physical_quantity)-l.system_quantity_snapshot)::text,
       CASE WHEN COALESCE(r.quantity,l.physical_quantity)=l.system_quantity_snapshot THEN 'MATCH' ELSE 'DIVERGENT' END,
       r.quantity::text,
       r.recounted_at,
       COALESCE(ru.login,''),
       l.count_version,
       (COALESCE(r.quantity,l.physical_quantity)<>l.system_quantity_snapshot AND r.quantity IS NULL)
FROM inventory_validation_lines l
JOIN product_skus s ON s.id=l.sku_id
JOIN inventory_items i ON i.id=l.item_id
LEFT JOIN users cu ON cu.id=l.counted_by_user_id
LEFT JOIN inventory_validation_recounts r ON r.validation_id=l.validation_id AND r.sku_id=l.sku_id
LEFT JOIN users ru ON ru.id=r.recounted_by_user_id
WHERE l.validation_id=$1
ORDER BY
  CASE WHEN COALESCE(r.quantity,l.physical_quantity)<>l.system_quantity_snapshot THEN 0 ELSE 1 END,
  i.name,
  l.sku_id`

type reviewScanner interface {
	Scan(...any) error
}

func scanInventoryReviewLine(s reviewScanner) (inventoryReviewLine, error) {
	var line inventoryReviewLine
	var firstAt, recountAt sql.NullTime
	var recountQty sql.NullString
	if err := s.Scan(
		&line.SKUID,
		&line.Code,
		&line.Description,
		&line.Unit,
		&line.Barcode,
		&line.FirstQuantity,
		&firstAt,
		&line.FirstCountedBy,
		&line.SnapshotQuantity,
		&line.EffectiveQuantity,
		&line.Difference,
		&line.State,
		&recountQty,
		&recountAt,
		&line.RecountedBy,
		&line.Version,
		&line.RecountAvailable,
	); err != nil {
		return inventoryReviewLine{}, err
	}
	if firstAt.Valid {
		t := firstAt.Time
		line.FirstCountedAt = &t
	}
	if recountQty.Valid {
		q := recountQty.String
		line.RecountQuantity = &q
	}
	if recountAt.Valid {
		t := recountAt.Time
		line.RecountedAt = &t
	}
	return line, nil
}

func (a *app) saveInventoryRecount(ctx context.Context, validationID, skuID int64, quantity string, expectedVersion, userID int64) (inventoryReviewLine, error) {
	if !validPhysicalQuantity(quantity) {
		return inventoryReviewLine{}, errInventoryInvalidCount
	}
	tx, err := a.db.BeginTx(ctx, nil)
	if err != nil {
		return inventoryReviewLine{}, err
	}
	defer tx.Rollback()

	var status string
	if err := tx.QueryRowContext(ctx, `SELECT status FROM inventory_validations WHERE id=$1 FOR UPDATE`, validationID).Scan(&status); errors.Is(err, sql.ErrNoRows) {
		return inventoryReviewLine{}, errInventoryValidationNotFound
	} else if err != nil {
		return inventoryReviewLine{}, err
	}
	if status != "REVIEW" {
		return inventoryReviewLine{}, errInventoryNotInReview
	}

	var firstQty, snapshot string
	var version int64
	if err := tx.QueryRowContext(ctx, `
SELECT physical_quantity::text,system_quantity_snapshot::text,count_version
FROM inventory_validation_lines
WHERE validation_id=$1 AND sku_id=$2
FOR UPDATE`, validationID, skuID).Scan(&firstQty, &snapshot, &version); errors.Is(err, sql.ErrNoRows) {
		return inventoryReviewLine{}, errInventoryLineNotFound
	} else if err != nil {
		return inventoryReviewLine{}, err
	}

	var existingQty sql.NullString
	var existingUserID int64
	err = tx.QueryRowContext(ctx, `
SELECT quantity::text,recounted_by_user_id
FROM inventory_validation_recounts
WHERE validation_id=$1 AND sku_id=$2`, validationID, skuID).Scan(&existingQty, &existingUserID)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return inventoryReviewLine{}, err
	}
	if existingQty.Valid {
		if version == expectedVersion+1 && existingQty.String == quantity && existingUserID == userID {
			line, err := inventoryReviewLineTx(ctx, tx, validationID, skuID)
			if err != nil {
				return inventoryReviewLine{}, err
			}
			if err := tx.Commit(); err != nil {
				return inventoryReviewLine{}, err
			}
			return line, nil
		}
		if version != expectedVersion {
			return inventoryReviewLine{}, errInventoryCountConflict
		}
		return inventoryReviewLine{}, errInventoryRecountExists
	}
	if version != expectedVersion {
		return inventoryReviewLine{}, errInventoryCountConflict
	}
	var divergent bool
	if err := tx.QueryRowContext(ctx, `SELECT $1::numeric <> $2::numeric`, firstQty, snapshot).Scan(&divergent); err != nil {
		return inventoryReviewLine{}, err
	}
	if !divergent {
		return inventoryReviewLine{}, errInventoryNotDivergent
	}

	if _, err := tx.ExecContext(ctx, `
INSERT INTO inventory_validation_recounts
    (validation_id,sku_id,quantity,recounted_by_user_id,recounted_at)
VALUES ($1,$2,$3::numeric,$4,statement_timestamp())`, validationID, skuID, quantity, userID); err != nil {
		return inventoryReviewLine{}, err
	}
	res, err := tx.ExecContext(ctx, `
UPDATE inventory_validation_lines
SET count_version=count_version+1
WHERE validation_id=$1 AND sku_id=$2 AND count_version=$3`, validationID, skuID, expectedVersion)
	if err != nil {
		return inventoryReviewLine{}, err
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return inventoryReviewLine{}, errInventoryCountConflict
	}

	line, err := inventoryReviewLineTx(ctx, tx, validationID, skuID)
	if err != nil {
		return inventoryReviewLine{}, err
	}
	if err := tx.Commit(); err != nil {
		return inventoryReviewLine{}, err
	}
	return line, nil
}

func inventoryReviewLineTx(ctx context.Context, tx *sql.Tx, validationID, skuID int64) (inventoryReviewLine, error) {
	q := strings.Replace(reviewLinesSQL, "WHERE l.validation_id=$1", "WHERE l.validation_id=$1 AND l.sku_id=$2", 1)
	row := tx.QueryRowContext(ctx, q, validationID, skuID)
	return scanInventoryReviewLine(row)
}
