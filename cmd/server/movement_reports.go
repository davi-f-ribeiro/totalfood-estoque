package main

import (
	"context"
	"database/sql"
	"net/http"
	"strconv"
	"strings"
	"time"
)

type movementReportRow struct {
	OccurredAt     time.Time `json:"occurred_at"`
	MovementID     int64     `json:"movement_id"`
	Type           string    `json:"type"`
	Item           string    `json:"item"`
	SKU            string    `json:"sku"`
	Barcode        string    `json:"barcode"`
	WarehouseID    int64     `json:"warehouse_id"`
	Warehouse      string    `json:"warehouse"`
	Quantity       string    `json:"quantity"`
	Unit           string    `json:"unit"`
	UnitCost       string    `json:"unit_cost"`
	DocumentNumber string    `json:"document_number"`
	SupplierName   string    `json:"supplier_name"`
	Responsible    string    `json:"responsible"`
}

func (a *app) listMovementReport(ctx context.Context, typ string, start, end time.Time, warehouseID int64) ([]movementReportRow, error) {
	rows, err := a.db.QueryContext(ctx, `
SELECT m.occurred_at,m.id,m.movement_type,i.name,
       COALESCE(NULLIF(s.name,''),NULLIF(s.brand,''),''),
       COALESCE(s.barcode,''),w.id,w.name,ml.quantity::text,i.unit_code,
       ml.unit_cost::numeric(14,4)::text,
       COALESCE(m.document_number,''),COALESCE(m.supplier_name,''),
       COALESCE(u.login,'')
FROM movements m
JOIN movement_lines ml ON ml.movement_id=m.id
JOIN product_skus s ON s.id=ml.sku_id
JOIN inventory_items i ON i.id=s.item_id
JOIN warehouses w ON w.id=ml.warehouse_id
LEFT JOIN users u ON u.id=m.user_id
WHERE m.movement_type=$1
  AND m.occurred_at >= $2
  AND m.occurred_at < $3
  AND ($4::bigint=0 OR ml.warehouse_id=$4)
ORDER BY m.occurred_at,m.id,ml.id`, typ, start, end, warehouseID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []movementReportRow{}
	for rows.Next() {
		var x movementReportRow
		if err := rows.Scan(&x.OccurredAt, &x.MovementID, &x.Type, &x.Item, &x.SKU, &x.Barcode, &x.WarehouseID, &x.Warehouse, &x.Quantity, &x.Unit, &x.UnitCost, &x.DocumentNumber, &x.SupplierName, &x.Responsible); err != nil {
			return nil, err
		}
		out = append(out, x)
	}
	return out, rows.Err()
}

func (a *app) handleMovementReport(w http.ResponseWriter, r *http.Request) {
	typ := strings.ToUpper(strings.TrimSpace(r.URL.Query().Get("type")))
	if typ != "ENTRADA" && typ != "SAIDA" {
		writeJSONError(w, http.StatusBadRequest, "Tipo de movimentação inválido.")
		return
	}
	start, errStart := time.Parse(time.RFC3339, r.URL.Query().Get("start"))
	end, errEnd := time.Parse(time.RFC3339, r.URL.Query().Get("end"))
	if errStart != nil || errEnd != nil || !start.Before(end) {
		writeJSONError(w, http.StatusBadRequest, "Período do relatório inválido.")
		return
	}
	var warehouseID int64
	if raw := strings.TrimSpace(r.URL.Query().Get("warehouse_id")); raw != "" {
		var err error
		warehouseID, err = strconv.ParseInt(raw, 10, 64)
		if err != nil || warehouseID <= 0 {
			writeJSONError(w, http.StatusBadRequest, "Depósito inválido.")
			return
		}
	}
	out, err := a.listMovementReport(r.Context(), typ, start, end, warehouseID)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "Não foi possível consultar as movimentações.")
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// Keep database/sql imported here so report row iteration remains explicit about the
// standard database layer used throughout the server.
var _ *sql.Rows
