package main

import (
	"database/sql"
	"errors"
	"net/http"
	"strconv"
	"strings"
)

type stockAvailability struct {
	WarehouseID int64  `json:"warehouse_id"`
	Warehouse   string `json:"warehouse"`
	Quantity    string `json:"quantity"`
	Unit        string `json:"unit"`
}

func (a *app) handleStockAvailability(w http.ResponseWriter, r *http.Request) {
	skuID, err := strconv.ParseInt(r.URL.Query().Get("sku_id"), 10, 64)
	if err != nil || skuID <= 0 {
		writeJSONError(w, http.StatusBadRequest, "invalid sku")
		return
	}
	rows, err := a.db.QueryContext(r.Context(), `
SELECT w.id,w.name,bal.quantity::text,i.unit_code
FROM warehouses w
JOIN product_skus requested_sku ON requested_sku.id=$1
JOIN inventory_items i ON i.id=requested_sku.item_id
JOIN (
  SELECT ml.warehouse_id, line_sku.item_id,
         COALESCE(sum(CASE WHEN ml.direction='IN' THEN ml.quantity ELSE -ml.quantity END),0) AS quantity
  FROM movement_lines ml
  JOIN product_skus line_sku ON line_sku.id=ml.sku_id
  GROUP BY ml.warehouse_id,line_sku.item_id
) bal ON bal.warehouse_id=w.id AND bal.item_id=requested_sku.item_id
WHERE w.active=true AND bal.quantity > 0
ORDER BY w.name`, skuID)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "internal error")
		return
	}
	defer rows.Close()
	out := []stockAvailability{}
	for rows.Next() {
		var x stockAvailability
		if err := rows.Scan(&x.WarehouseID, &x.Warehouse, &x.Quantity, &x.Unit); err != nil {
			writeJSONError(w, http.StatusInternalServerError, "internal error")
			return
		}
		out = append(out, x)
	}
	writeJSON(w, http.StatusOK, out)
}

func (a *app) handleStockDetail(w http.ResponseWriter, r *http.Request) {
	itemID, err := strconv.ParseInt(r.PathValue("item_id"), 10, 64)
	if err != nil || itemID <= 0 {
		writeJSONError(w, http.StatusBadRequest, "invalid item")
		return
	}
	var head struct {
		ID                  int64
		Name, Unit, Minimum string
	}
	err = a.db.QueryRowContext(r.Context(), `SELECT id,name,unit_code,COALESCE(minimum_stock::text,'') FROM inventory_items WHERE id=$1`, itemID).Scan(&head.ID, &head.Name, &head.Unit, &head.Minimum)
	if errors.Is(err, sql.ErrNoRows) {
		writeJSONError(w, http.StatusNotFound, "item not found")
		return
	}
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "internal error")
		return
	}
	rows, err := a.db.QueryContext(r.Context(), `
SELECT rep.id,COALESCE(rep.name,''),COALESCE(rep.barcode,''),w.id,w.name,
       sum(CASE WHEN ml.direction='IN' THEN ml.quantity ELSE -ml.quantity END)::text
FROM movement_lines ml
JOIN product_skus line_sku ON line_sku.id=ml.sku_id
JOIN warehouses w ON w.id=ml.warehouse_id
JOIN LATERAL (
    SELECT s.id,s.name,s.barcode
    FROM product_skus s
    WHERE s.item_id=$1 AND s.active=true
    ORDER BY s.id
    LIMIT 1
) rep ON true
WHERE line_sku.item_id=$1
GROUP BY rep.id,rep.name,rep.barcode,w.id,w.name
HAVING sum(CASE WHEN ml.direction='IN' THEN ml.quantity ELSE -ml.quantity END) <> 0
ORDER BY w.name`, itemID)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "internal error")
		return
	}
	defer rows.Close()
	details := []map[string]any{}
	for rows.Next() {
		var skuID, whID sql.NullInt64
		var sku, barcode, wh, qty string
		if err := rows.Scan(&skuID, &sku, &barcode, &whID, &wh, &qty); err != nil {
			writeJSONError(w, http.StatusInternalServerError, "internal error")
			return
		}
		details = append(details, map[string]any{"sku_id": skuID.Int64, "sku": sku, "barcode": barcode, "warehouse_id": whID.Int64, "warehouse": wh, "quantity": qty})
	}
	writeJSON(w, http.StatusOK, map[string]any{"id": head.ID, "name": head.Name, "unit": head.Unit, "minimum": head.Minimum, "details": details})
}

func stockRowsSQL(general bool, showZero bool) string {
	where := "HAVING COALESCE(sum(CASE WHEN ml.direction='IN' THEN ml.quantity ELSE -ml.quantity END),0) <> 0"
	if showZero {
		where = ""
	}
	if general {
		return `SELECT i.id,i.name,i.unit_code,COALESCE(i.minimum_stock::text,''),COALESCE(sum(CASE WHEN ml.direction='IN' THEN ml.quantity ELSE -ml.quantity END),0)::text AS qty, CASE WHEN i.minimum_stock IS NULL THEN false ELSE COALESCE(sum(CASE WHEN ml.direction='IN' THEN ml.quantity ELSE -ml.quantity END),0) < i.minimum_stock END FROM inventory_items i JOIN product_skus s ON s.item_id=i.id LEFT JOIN movement_lines ml ON ml.sku_id=s.id GROUP BY i.id ` + where + ` ORDER BY i.name`
	}
	return `SELECT i.id,i.name,i.unit_code,COALESCE(i.minimum_stock::text,''),COALESCE(sum(CASE WHEN ml.direction='IN' THEN ml.quantity ELSE -ml.quantity END),0)::text AS qty, CASE WHEN i.minimum_stock IS NULL THEN false ELSE COALESCE(sum(CASE WHEN ml.direction='IN' THEN ml.quantity ELSE -ml.quantity END),0) < i.minimum_stock END FROM inventory_items i JOIN product_skus s ON s.item_id=i.id LEFT JOIN movement_lines ml ON ml.sku_id=s.id AND ml.warehouse_id=$1 GROUP BY i.id ` + where + ` ORDER BY i.name`
}

func normalizeOpText(v string) string { return strings.TrimSpace(v) }
