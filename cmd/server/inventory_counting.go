package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"
)

var (
	errInventoryCountConflict = errors.New("inventory count conflict")
	errInventoryReadOnly      = errors.New("inventory is read only")
	errInventoryInvalidCount  = errors.New("invalid physical quantity")
	errInventoryLineNotFound  = errors.New("inventory line not found")
)

var physicalQuantityPattern = regexp.MustCompile(`^(0|[1-9][0-9]{0,10})(\.[0-9]{1,3})?$`)

type inventoryCountingLine struct {
	SKUID            int64      `json:"sku_id"`
	Code             string     `json:"code"`
	Description      string     `json:"description"`
	Unit             string     `json:"unit"`
	Barcode          string     `json:"barcode,omitempty"`
	PhysicalQuantity *string    `json:"physical_quantity"`
	CountedAt        *time.Time `json:"counted_at"`
	CountedBy        string     `json:"counted_by,omitempty"`
	Version          int64      `json:"version"`
}

type inventoryCountingView struct {
	ID            int64                   `json:"id"`
	Type          string                  `json:"type"`
	WarehouseID   int64                   `json:"warehouse_id"`
	Warehouse     string                  `json:"warehouse"`
	Status        string                  `json:"status"`
	SnapshotAt    *time.Time              `json:"snapshot_at,omitempty"`
	Counted       int                     `json:"counted"`
	Total         int                     `json:"total"`
	Progress      int                     `json:"progress_percent"`
	Lines         []inventoryCountingLine `json:"lines"`
}

type inventoryCountRequest struct {
	PhysicalQuantity string `json:"physical_quantity"`
	ExpectedVersion  int64  `json:"expected_version"`
}

type inventoryLookupResult struct {
	Known       bool  `json:"known"`
	InInventory bool  `json:"in_inventory"`
	SKUID       int64 `json:"sku_id,omitempty"`
}

func (a *app) registerInventoryCountingHandlers(mux *http.ServeMux) {
	mux.HandleFunc("GET /inventory", a.uiPage("inventory", "Auditoria de Estoque", "inventory_validation", "read"))
	mux.HandleFunc("GET /api/inventory-validations/{id}/counting", a.requireAuth("inventory_validation", "read", a.handleGetInventoryCounting))
	mux.HandleFunc("PUT /api/inventory-validations/{id}/lines/{sku_id}/count", a.requireAuth("inventory_validation", "write", a.handleSaveInventoryCount))
	mux.HandleFunc("GET /api/inventory-validations/{id}/lookup", a.requireAuth("inventory_validation", "read", a.handleInventoryLookup))
}

func (a *app) handleGetInventoryCounting(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	view, err := a.getInventoryCountingView(r.Context(), id)
	if err != nil {
		if errors.Is(err, errInventoryValidationNotFound) {
			writeJSONError(w, http.StatusNotFound, "Inventário não encontrado.")
			return
		}
		log.Printf("inventory counting lookup failed validation_id=%d error=%v", id, err)
		writeJSONError(w, http.StatusInternalServerError, "Erro interno.")
		return
	}
	writeJSON(w, http.StatusOK, view)
}

func (a *app) handleInventoryLookup(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	barcode := cleanBarcode(r.URL.Query().Get("barcode"))
	if barcode == "" {
		writeJSONError(w, http.StatusBadRequest, "Código inválido.")
		return
	}
	res, err := a.lookupInventoryBarcode(r.Context(), id, barcode)
	if errors.Is(err, errInventoryValidationNotFound) {
		writeJSONError(w, http.StatusNotFound, "Inventário não encontrado.")
		return
	}
	if err != nil {
		log.Printf("inventory barcode lookup failed validation_id=%d error=%v", id, err)
		writeJSONError(w, http.StatusInternalServerError, "Erro interno.")
		return
	}
	writeJSON(w, http.StatusOK, res)
}

func (a *app) handleSaveInventoryCount(w http.ResponseWriter, r *http.Request) {
	validationID, ok := pathID(w, r)
	if !ok {
		return
	}
	skuID, err := strconv.ParseInt(strings.TrimSpace(r.PathValue("sku_id")), 10, 64)
	if err != nil || skuID <= 0 {
		writeJSONError(w, http.StatusBadRequest, "SKU inválido.")
		return
	}
	var req inventoryCountRequest
	if !decodeCountJSON(w, r, &req) {
		return
	}
	req.PhysicalQuantity = normalizeDecimalInput(req.PhysicalQuantity)
	if !validPhysicalQuantity(req.PhysicalQuantity) || req.ExpectedVersion < 0 {
		writeJSONError(w, http.StatusBadRequest, "Quantidade física inválida.")
		return
	}
	line, status, err := a.saveInventoryCount(r.Context(), validationID, skuID, req.PhysicalQuantity, req.ExpectedVersion, userFromContext(r.Context()).ID)
	switch {
	case err == nil:
		writeJSON(w, http.StatusOK, map[string]any{"line": line, "status": status})
	case errors.Is(err, errInventoryValidationNotFound), errors.Is(err, errInventoryLineNotFound):
		writeJSONError(w, http.StatusNotFound, "Linha de inventário não encontrada.")
	case errors.Is(err, errInventoryCountConflict):
		writeJSONError(w, http.StatusConflict, "Esta contagem foi alterada por outro operador. Recarregue antes de salvar novamente.")
	case errors.Is(err, errInventoryReadOnly):
		writeJSONError(w, http.StatusConflict, "Inventário não aceita alterações neste estado.")
	case errors.Is(err, errInventoryInvalidCount):
		writeJSONError(w, http.StatusBadRequest, "Quantidade física inválida.")
	default:
		log.Printf("inventory count save failed validation_id=%d sku_id=%d error=%v", validationID, skuID, err)
		writeJSONError(w, http.StatusInternalServerError, "Erro interno.")
	}
}

func decodeCountJSON(w http.ResponseWriter, r *http.Request, dst *inventoryCountRequest) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	defer r.Body.Close()
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
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

func validPhysicalQuantity(v string) bool {
	return physicalQuantityPattern.MatchString(v)
}

func (a *app) getInventoryCountingView(ctx context.Context, id int64) (inventoryCountingView, error) {
	var out inventoryCountingView
	var snapshotAt sql.NullTime
	if err := a.db.QueryRowContext(ctx, `
SELECT v.id,v.type,v.warehouse_id,w.name,v.status,v.snapshot_at
FROM inventory_validations v
JOIN warehouses w ON w.id=v.warehouse_id
WHERE v.id=$1`, id).Scan(&out.ID, &out.Type, &out.WarehouseID, &out.Warehouse, &out.Status, &snapshotAt); errors.Is(err, sql.ErrNoRows) {
		return inventoryCountingView{}, errInventoryValidationNotFound
	} else if err != nil {
		return inventoryCountingView{}, err
	}
	if snapshotAt.Valid {
		t := snapshotAt.Time
		out.SnapshotAt = &t
	}
	rows, err := a.db.QueryContext(ctx, `
SELECT l.sku_id,
       COALESCE(NULLIF(s.name,''),NULLIF(s.brand,''),s.id::text) AS code,
       i.name,
       i.unit_code,
       COALESCE(s.barcode,''),
       l.physical_quantity::text,
       l.counted_at,
       COALESCE(u.login,''),
       l.count_version
FROM inventory_validation_lines l
JOIN product_skus s ON s.id=l.sku_id
JOIN inventory_items i ON i.id=s.item_id
LEFT JOIN users u ON u.id=l.counted_by_user_id
WHERE l.validation_id=$1
ORDER BY i.name,code,l.sku_id`, id)
	if err != nil {
		return inventoryCountingView{}, err
	}
	defer rows.Close()
	out.Lines = []inventoryCountingLine{}
	for rows.Next() {
		var line inventoryCountingLine
		var qty sql.NullString
		var countedAt sql.NullTime
		if err := rows.Scan(&line.SKUID, &line.Code, &line.Description, &line.Unit, &line.Barcode, &qty, &countedAt, &line.CountedBy, &line.Version); err != nil {
			return inventoryCountingView{}, err
		}
		if qty.Valid {
			q := qty.String
			line.PhysicalQuantity = &q
			out.Counted++
		}
		if countedAt.Valid {
			t := countedAt.Time
			line.CountedAt = &t
		}
		out.Lines = append(out.Lines, line)
	}
	if err := rows.Err(); err != nil {
		return inventoryCountingView{}, err
	}
	out.Total = len(out.Lines)
	if out.Total > 0 {
		out.Progress = out.Counted * 100 / out.Total
	}
	return out, nil
}

func (a *app) lookupInventoryBarcode(ctx context.Context, validationID int64, barcode string) (inventoryLookupResult, error) {
	var exists bool
	if err := a.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM inventory_validations WHERE id=$1)`, validationID).Scan(&exists); err != nil {
		return inventoryLookupResult{}, err
	}
	if !exists {
		return inventoryLookupResult{}, errInventoryValidationNotFound
	}
	var skuID int64
	err := a.db.QueryRowContext(ctx, `SELECT id FROM product_skus WHERE barcode=$1`, barcode).Scan(&skuID)
	if errors.Is(err, sql.ErrNoRows) {
		return inventoryLookupResult{Known: false}, nil
	}
	if err != nil {
		return inventoryLookupResult{}, err
	}
	var inInventory bool
	if err := a.db.QueryRowContext(ctx, `
SELECT EXISTS(
    SELECT 1 FROM inventory_validation_lines
    WHERE validation_id=$1 AND sku_id=$2
)`, validationID, skuID).Scan(&inInventory); err != nil {
		return inventoryLookupResult{}, err
	}
	return inventoryLookupResult{Known: true, InInventory: inInventory, SKUID: skuID}, nil
}

func (a *app) saveInventoryCount(ctx context.Context, validationID, skuID int64, quantity string, expectedVersion, userID int64) (inventoryCountingLine, string, error) {
	if !validPhysicalQuantity(quantity) {
		return inventoryCountingLine{}, "", errInventoryInvalidCount
	}
	tx, err := a.db.BeginTx(ctx, nil)
	if err != nil {
		return inventoryCountingLine{}, "", err
	}
	defer tx.Rollback()

	var validationStatus string
	if err := tx.QueryRowContext(ctx, `
SELECT status FROM inventory_validations WHERE id=$1 FOR UPDATE`, validationID).Scan(&validationStatus); errors.Is(err, sql.ErrNoRows) {
		return inventoryCountingLine{}, "", errInventoryValidationNotFound
	} else if err != nil {
		return inventoryCountingLine{}, "", err
	}
	if validationStatus != "OPEN" && validationStatus != "COUNTING" {
		return inventoryCountingLine{}, validationStatus, errInventoryReadOnly
	}

	var currentQty sql.NullString
	var currentCountedBy sql.NullInt64
	var currentVersion int64
	if err := tx.QueryRowContext(ctx, `
SELECT physical_quantity::text,counted_by_user_id,count_version
FROM inventory_validation_lines
WHERE validation_id=$1 AND sku_id=$2
FOR UPDATE`, validationID, skuID).Scan(&currentQty, &currentCountedBy, &currentVersion); errors.Is(err, sql.ErrNoRows) {
		return inventoryCountingLine{}, validationStatus, errInventoryLineNotFound
	} else if err != nil {
		return inventoryCountingLine{}, validationStatus, err
	}

	if currentVersion != expectedVersion {
		if currentQty.Valid && currentQty.String == quantity && currentCountedBy.Valid && currentCountedBy.Int64 == userID && currentVersion == expectedVersion+1 {
			line, err := inventoryCountingLineTx(ctx, tx, validationID, skuID)
			if err != nil {
				return inventoryCountingLine{}, validationStatus, err
			}
			if err := tx.Commit(); err != nil {
				return inventoryCountingLine{}, validationStatus, err
			}
			return line, validationStatus, nil
		}
		return inventoryCountingLine{}, validationStatus, errInventoryCountConflict
	}

	if _, err := tx.ExecContext(ctx, `
UPDATE inventory_validation_lines
SET physical_quantity=$1::numeric,
    counted_by_user_id=$2,
    counted_at=statement_timestamp(),
    status='COUNTED',
    count_version=count_version+1
WHERE validation_id=$3 AND sku_id=$4 AND count_version=$5`,
		quantity, userID, validationID, skuID, expectedVersion); err != nil {
		return inventoryCountingLine{}, validationStatus, err
	}

	var pending int
	if err := tx.QueryRowContext(ctx, `
SELECT count(*) FROM inventory_validation_lines
WHERE validation_id=$1 AND physical_quantity IS NULL`, validationID).Scan(&pending); err != nil {
		return inventoryCountingLine{}, validationStatus, err
	}
	nextStatus := "COUNTING"
	if pending == 0 {
		nextStatus = "REVIEW"
	}
	if _, err := tx.ExecContext(ctx, `
UPDATE inventory_validations
SET status=$2
WHERE id=$1 AND status IN ('OPEN','COUNTING')`, validationID, nextStatus); err != nil {
		return inventoryCountingLine{}, validationStatus, err
	}

	line, err := inventoryCountingLineTx(ctx, tx, validationID, skuID)
	if err != nil {
		return inventoryCountingLine{}, validationStatus, err
	}
	if err := tx.Commit(); err != nil {
		return inventoryCountingLine{}, validationStatus, err
	}
	return line, nextStatus, nil
}

func inventoryCountingLineTx(ctx context.Context, tx *sql.Tx, validationID, skuID int64) (inventoryCountingLine, error) {
	var line inventoryCountingLine
	var qty sql.NullString
	var countedAt sql.NullTime
	if err := tx.QueryRowContext(ctx, `
SELECT l.sku_id,
       COALESCE(NULLIF(s.name,''),NULLIF(s.brand,''),s.id::text),
       i.name,
       i.unit_code,
       COALESCE(s.barcode,''),
       l.physical_quantity::text,
       l.counted_at,
       COALESCE(u.login,''),
       l.count_version
FROM inventory_validation_lines l
JOIN product_skus s ON s.id=l.sku_id
JOIN inventory_items i ON i.id=s.item_id
LEFT JOIN users u ON u.id=l.counted_by_user_id
WHERE l.validation_id=$1 AND l.sku_id=$2`, validationID, skuID).
		Scan(&line.SKUID, &line.Code, &line.Description, &line.Unit, &line.Barcode, &qty, &countedAt, &line.CountedBy, &line.Version); err != nil {
		return inventoryCountingLine{}, err
	}
	if qty.Valid {
		q := qty.String
		line.PhysicalQuantity = &q
	}
	if countedAt.Valid {
		t := countedAt.Time
		line.CountedAt = &t
	}
	return line, nil
}
