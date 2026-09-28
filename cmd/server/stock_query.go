package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
)

func getStockSnapshot(ctx context.Context, q interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, skuID, warehouseID int64) (stockSnapshot, error) {
	var s stockSnapshot
	err := q.QueryRowContext(ctx, `
SELECT
  COALESCE(sum(CASE WHEN direction='IN' THEN quantity ELSE -quantity END),0)::text AS quantity,
  COALESCE(sum(CASE WHEN direction='IN' THEN quantity*unit_cost ELSE -quantity*unit_cost END),0)::text AS value,
  CASE WHEN COALESCE(sum(CASE WHEN direction='IN' THEN quantity ELSE -quantity END),0) = 0
       THEN '0'
       ELSE (COALESCE(sum(CASE WHEN direction='IN' THEN quantity*unit_cost ELSE -quantity*unit_cost END),0) / COALESCE(sum(CASE WHEN direction='IN' THEN quantity ELSE -quantity END),0))::numeric(14,4)::text
  END AS avg_cost
FROM movement_lines WHERE sku_id=$1 AND warehouse_id=$2`, skuID, warehouseID).Scan(&s.Quantity, &s.Value, &s.AvgCost)
	if err != nil {
		return s, err
	}
	err = q.QueryRowContext(ctx, `
SELECT i.unit_code
FROM product_skus p
JOIN inventory_items i ON i.id=p.item_id
WHERE p.id=$1`, skuID).Scan(&s.Unit)
	return s, err
}

func quantityAtLeast(ctx context.Context, tx *sql.Tx, have, want string) bool {
	var ok bool
	if err := tx.QueryRowContext(ctx, `SELECT $1::numeric >= $2::numeric`, have, want).Scan(&ok); err != nil {
		return false
	}
	return ok
}

func (a *app) getOperation(ctx context.Context, operationID string) (operationResult, error) {
	var r operationResult
	err := a.db.QueryRowContext(ctx, `SELECT operation_id, id, 'confirmed', movement_type FROM movements WHERE operation_id=$1`, operationID).Scan(&r.OperationID, &r.MovementID, &r.Status, &r.Type)
	if errors.Is(err, sql.ErrNoRows) {
		return r, errOperationNotFound
	}
	return r, err
}

func (a *app) writeOperationResponse(w http.ResponseWriter, typ, operationID string, res operationResult, err error) {
	if err == nil {
		writeJSON(w, http.StatusOK, res)
		return
	}
	status := http.StatusInternalServerError
	msg := "internal error"
	switch {
	case errors.Is(err, errValidation):
		status, msg = http.StatusBadRequest, "validation rejected"
	case errors.Is(err, errNotFound):
		status, msg = http.StatusNotFound, "resource not found"
	case errors.Is(err, errInsufficientStock):
		status, msg = http.StatusConflict, "Estoque insuficiente."
		var e insufficientStockError
		if errors.As(err, &e) && e.Available != "" && e.Unit != "" {
			msg = fmt.Sprintf("Estoque insuficiente. Disponível: %s %s.", e.Available, e.Unit)
		}
	case errors.Is(err, errOperationConflict):
		status, msg = http.StatusConflict, "operation_id conflict"
	case errors.Is(err, errDuplicateFiscalDocument):
		status, msg = http.StatusConflict, "Nota fiscal já confirmada."
	}
	if status == http.StatusInternalServerError {
		log.Printf("operation_id=%s type=%s stage=write error=%v", operationID, typ, err)
	}
	writeJSONError(w, status, msg)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeJSONError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}
