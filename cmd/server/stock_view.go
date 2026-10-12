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
		writeJSONError(w, http.StatusBadRequest, "Produto inválido.")
		return
	}
	rows, err := a.db.QueryContext(r.Context(), `
SELECT w.id,w.name,bal.quantity::text,i.unit_code
FROM warehouses w
JOIN product_skus requested_sku ON requested_sku.id=$1
JOIN inventory_items i ON i.id=requested_sku.item_id
JOIN (
  SELECT ml.warehouse_id,ml.item_id,s.brand_id,
         COALESCE(sum(CASE WHEN ml.direction='IN' THEN ml.quantity ELSE -ml.quantity END),0) AS quantity
  FROM movement_lines ml
  JOIN product_skus s ON s.id=ml.sku_id
  GROUP BY ml.warehouse_id,ml.item_id,s.brand_id
) bal ON bal.warehouse_id=w.id
     AND bal.item_id=requested_sku.item_id
     AND bal.brand_id=requested_sku.brand_id
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
		writeJSONError(w, http.StatusBadRequest, "Item inválido.")
		return
	}
	rawBrand := strings.TrimSpace(r.URL.Query().Get("brand_id"))
	if rawBrand == "" {
		warehouseID, _ := strconv.ParseInt(r.URL.Query().Get("warehouse_id"), 10, 64)
		rows, err := a.db.QueryContext(r.Context(), `
SELECT b.id,b.name,i.unit_code,
       sum(CASE WHEN ml.direction='IN' THEN ml.quantity ELSE -ml.quantity END)::text
FROM movement_lines ml
JOIN product_skus s ON s.id=ml.sku_id
JOIN brands b ON b.id=s.brand_id
JOIN inventory_items i ON i.id=ml.item_id
WHERE ml.item_id=$1 AND ($2::bigint=0 OR ml.warehouse_id=$2)
GROUP BY b.id,b.name,i.unit_code
HAVING sum(CASE WHEN ml.direction='IN' THEN ml.quantity ELSE -ml.quantity END) > 0
ORDER BY b.name`, itemID, warehouseID)
		if err != nil {
			writeJSONError(w, http.StatusInternalServerError, "internal error")
			return
		}
		defer rows.Close()
		brands := []map[string]any{}
		for rows.Next() {
			var brandID int64
			var brand, unit, quantity string
			if err := rows.Scan(&brandID,&brand,&unit,&quantity); err != nil {
				writeJSONError(w, http.StatusInternalServerError, "internal error")
				return
			}
			brands = append(brands,map[string]any{"brand_id":brandID,"brand":brand,"unit":unit,"quantity":quantity})
		}
		if err := rows.Err(); err != nil {
			writeJSONError(w, http.StatusInternalServerError, "internal error")
			return
		}
		writeJSON(w,http.StatusOK,map[string]any{"item_id":itemID,"brands":brands})
		return
	}
	brandID, brandErr := strconv.ParseInt(rawBrand, 10, 64)
	if brandErr != nil || brandID <= 0 {
		writeJSONError(w, http.StatusBadRequest, "Dados inválidos. Confira os campos e tente de novo.")
		return
	}
	var head struct {
		ID, BrandID                  int64
		Name, Brand, Unit, Minimum string
	}
	err = a.db.QueryRowContext(r.Context(), `
SELECT i.id,b.id,i.name,b.name,i.unit_code,COALESCE(i.minimum_stock::text,'')
FROM inventory_items i
JOIN brands b ON b.id=$2
WHERE i.id=$1`, itemID,brandID).Scan(&head.ID,&head.BrandID,&head.Name,&head.Brand,&head.Unit,&head.Minimum)
	if errors.Is(err, sql.ErrNoRows) {
		writeJSONError(w, http.StatusNotFound, "Item não encontrado.")
		return
	}
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "internal error")
		return
	}
	rows, err := a.db.QueryContext(r.Context(), `
SELECT w.id,w.name,
       sum(CASE WHEN ml.direction='IN' THEN ml.quantity ELSE -ml.quantity END)::text
FROM movement_lines ml
JOIN product_skus s ON s.id=ml.sku_id
JOIN warehouses w ON w.id=ml.warehouse_id
WHERE ml.item_id=$1 AND s.brand_id=$2
GROUP BY w.id,w.name
HAVING sum(CASE WHEN ml.direction='IN' THEN ml.quantity ELSE -ml.quantity END) > 0
ORDER BY w.name`, itemID,brandID)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "internal error")
		return
	}
	defer rows.Close()
	details := []map[string]any{}
	for rows.Next() {
		var whID int64
		var wh, qty string
		if err := rows.Scan(&whID, &wh, &qty); err != nil {
			writeJSONError(w, http.StatusInternalServerError, "internal error")
			return
		}
		details = append(details, map[string]any{"warehouse_id": whID, "warehouse": wh, "quantity": qty})
	}
	if err := rows.Err(); err != nil {
		writeJSONError(w, http.StatusInternalServerError, "internal error")
		return
	}

	presentationRows, err := a.db.QueryContext(r.Context(), `
SELECT s.id,COALESCE(s.name,''),b.name,COALESCE(s.barcode,''),s.content_quantity::text
FROM product_skus s
JOIN brands b ON b.id=s.brand_id
WHERE s.item_id=$1 AND s.brand_id=$2 AND s.active=true
ORDER BY s.content_quantity,s.id`, itemID,brandID)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "internal error")
		return
	}
	defer presentationRows.Close()
	presentations := []map[string]any{}
	for presentationRows.Next() {
		var skuID int64
		var sku, brand, barcode, contentQuantity string
		if err := presentationRows.Scan(&skuID,&sku,&brand,&barcode,&contentQuantity); err != nil {
			writeJSONError(w, http.StatusInternalServerError, "internal error")
			return
		}
		presentations = append(presentations, map[string]any{
			"sku_id": skuID,
			"sku": sku,
			"brand": brand,
			"barcode": barcode,
			"content_quantity": contentQuantity,
		})
	}
	if err := presentationRows.Err(); err != nil {
		writeJSONError(w, http.StatusInternalServerError, "internal error")
		return
	}
	lotRows, err := a.db.QueryContext(r.Context(), `
SELECT l.id,w.id,w.name,
       (l.initial_quantity-alloc.allocated)::text,
       COALESCE(l.manufacturer_lot_code,''),
       COALESCE(l.expires_at::text,''),
       COALESCE(NULLIF(src.name,''),NULLIF(src.brand,''),''),
       COALESCE(src.barcode,''),
       l.source_movement_line_id IS NULL
FROM stock_lots l
JOIN warehouses w ON w.id=l.warehouse_id
LEFT JOIN movement_lines source_ml ON source_ml.id=l.source_movement_line_id
LEFT JOIN product_skus src ON src.id=source_ml.sku_id
JOIN LATERAL (
    SELECT COALESCE(sum(a.quantity),0) AS allocated
    FROM stock_lot_allocations a
    WHERE a.stock_lot_id=l.id
) alloc ON true
WHERE l.item_id=$1
  AND l.brand_id=$2
  AND l.initial_quantity-alloc.allocated > 0
ORDER BY w.name,(l.expires_at IS NULL),l.expires_at,l.received_at,l.id`, itemID,brandID)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "internal error")
		return
	}
	defer lotRows.Close()
	lots := []map[string]any{}
	for lotRows.Next() {
		var lotID, warehouseID int64
		var warehouse, quantity, manufacturerLot, expiresAt, sourceSKU, sourceBarcode string
		var legacyOpening bool
		if err := lotRows.Scan(&lotID,&warehouseID,&warehouse,&quantity,&manufacturerLot,&expiresAt,&sourceSKU,&sourceBarcode,&legacyOpening); err != nil {
			writeJSONError(w, http.StatusInternalServerError, "internal error")
			return
		}
		lots = append(lots, map[string]any{
			"id": lotID,
			"warehouse_id": warehouseID,
			"warehouse": warehouse,
			"quantity": quantity,
			"manufacturer_lot": manufacturerLot,
			"expires_at": expiresAt,
			"source_sku": sourceSKU,
			"source_barcode": sourceBarcode,
			"legacy_opening": legacyOpening,
		})
	}
	if err := lotRows.Err(); err != nil {
		writeJSONError(w, http.StatusInternalServerError, "internal error")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"id": head.ID, "brand_id": head.BrandID, "name": head.Name, "brand": head.Brand, "unit": head.Unit, "minimum": head.Minimum, "details": details, "presentations": presentations, "lots": lots})
}

func stockRowsSQL(general bool, showZero bool) string {
	_ = showZero // zero balances are intentionally not part of the operational stock view
	base := `SELECT i.id,i.name,i.unit_code,COALESCE(i.minimum_stock::text,''),
       sum(CASE WHEN ml.direction='IN' THEN ml.quantity ELSE -ml.quantity END)::text AS qty,
       sum(CASE WHEN ml.direction='IN' THEN ml.quantity ELSE -ml.quantity END) < COALESCE(i.minimum_stock,0)
FROM movement_lines ml
JOIN inventory_items i ON i.id=ml.item_id`
	if general {
		return base + ` GROUP BY i.id,i.name,i.unit_code,i.minimum_stock
HAVING sum(CASE WHEN ml.direction='IN' THEN ml.quantity ELSE -ml.quantity END) > 0
ORDER BY i.name`
	}
	return base + ` WHERE ml.warehouse_id=$1
GROUP BY i.id,i.name,i.unit_code,i.minimum_stock
HAVING sum(CASE WHEN ml.direction='IN' THEN ml.quantity ELSE -ml.quantity END) > 0
ORDER BY i.name`
}

func normalizeOpText(v string) string { return strings.TrimSpace(v) }
