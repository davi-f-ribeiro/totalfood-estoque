package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"time"
)

func getStockSnapshot(ctx context.Context, q interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, skuID, warehouseID int64) (stockSnapshot, error) {
	return getStockSnapshotAt(ctx, q, skuID, warehouseID, nil)
}

func getStockSnapshotAt(ctx context.Context, q interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, skuID, warehouseID int64, before *time.Time) (stockSnapshot, error) {
	var s stockSnapshot
	err := q.QueryRowContext(ctx, `
SELECT
  COALESCE(sum(CASE WHEN ml.direction='IN' THEN ml.quantity ELSE -ml.quantity END),0)::text AS quantity,
  COALESCE(sum(CASE WHEN ml.direction='IN' THEN ml.quantity*ml.unit_cost ELSE -ml.quantity*ml.unit_cost END),0)::text AS value,
  CASE WHEN COALESCE(sum(CASE WHEN ml.direction='IN' THEN ml.quantity ELSE -ml.quantity END),0) = 0
       THEN '0'
       ELSE (COALESCE(sum(CASE WHEN ml.direction='IN' THEN ml.quantity*ml.unit_cost ELSE -ml.quantity*ml.unit_cost END),0) / COALESCE(sum(CASE WHEN ml.direction='IN' THEN ml.quantity ELSE -ml.quantity END),0))::numeric(14,4)::text
  END AS avg_cost
FROM movement_lines ml
JOIN product_skus movement_sku ON movement_sku.id=ml.sku_id
JOIN product_skus requested_sku ON requested_sku.id=$1
JOIN movements m ON m.id=ml.movement_id
WHERE ml.item_id=requested_sku.item_id
  AND movement_sku.brand_id=requested_sku.brand_id
  AND ml.warehouse_id=$2
  AND ($3::timestamptz IS NULL OR m.occurred_at < $3)`, skuID, warehouseID, before).Scan(&s.Quantity, &s.Value, &s.AvgCost)
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
	err := a.db.QueryRowContext(ctx, `SELECT operation_id, id, 'confirmed', movement_type, COALESCE(user_id,0) FROM movements WHERE operation_id=$1`, operationID).Scan(&r.OperationID, &r.MovementID, &r.Status, &r.Type, &r.UserID)
	if errors.Is(err, sql.ErrNoRows) {
		// Lote: o envio é confirmado quando a primeira linha existe (tudo ou nada).
		err = a.db.QueryRowContext(ctx, `SELECT $1::text, id, 'confirmed', movement_type, COALESCE(user_id,0) FROM movements WHERE operation_id=$2`, operationID, lineOperationID(operationID, 1)).Scan(&r.OperationID, &r.MovementID, &r.Status, &r.Type, &r.UserID)
	}
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
	case errors.Is(err, errMovementOperationIDMissing):
		status, msg = http.StatusBadRequest, "Não foi possível identificar este envio. Atualize a página e tente de novo."
	case errors.Is(err, errMovementProductRequired):
		status, msg = http.StatusBadRequest, "Escolha um produto antes de registrar o lançamento."
	case errors.Is(err, errMovementQuantityInvalid):
		status, msg = http.StatusBadRequest, "Digite uma quantidade maior que zero, com até 4 casas decimais."
	case errors.Is(err, errMovementCostInvalid):
		status, msg = http.StatusBadRequest, "Digite um custo válido, de zero ou mais, com até 4 casas decimais."
	case errors.Is(err, errMovementWarehouseRequired):
		status, msg = http.StatusBadRequest, "Escolha um depósito antes de registrar o lançamento."
	case errors.Is(err, errTransferWarehousesRequired):
		status, msg = http.StatusBadRequest, "Escolha o depósito de origem e o de destino."
	case errors.Is(err, errTransferSameWarehouse):
		status, msg = http.StatusBadRequest, "O destino precisa ser diferente da origem."
	case errors.Is(err, errValidation):
		status, msg = http.StatusBadRequest, "Dados inválidos. Confira os campos e tente de novo."
	case errors.Is(err, errNotFound):
		status, msg = http.StatusNotFound, "resource not found"
	case errors.Is(err, errInsufficientStock):
		status, msg = http.StatusConflict, "Estoque insuficiente."
		var e insufficientStockError
		if errors.As(err, &e) && e.Available != "" && e.Unit != "" {
			msg = fmt.Sprintf("Estoque insuficiente. Só há %s %s.", e.Available, e.Unit)
		}
	case errors.Is(err, errOperationConflict):
		status, msg = http.StatusConflict, "operation_id conflict"
	case errors.Is(err, errWarehouseInactive):
		status, msg = http.StatusConflict, "Depósito desativado."
	case errors.Is(err, errDuplicateFiscalDocument):
		status, msg = http.StatusConflict, "Esta nota já foi confirmada."
	case errors.Is(err, errDuplicateEntrySKU):
		status, msg = http.StatusBadRequest, "Um produto não pode aparecer duas vezes na nota."
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
