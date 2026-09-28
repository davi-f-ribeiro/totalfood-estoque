package main

import (
	"database/sql"
	"encoding/json"
	"log"
	"net/http"
	"strconv"
	"strings"
)

type catalogItemRequest struct {
	Name         string `json:"name"`
	CategoryID   int64  `json:"category_id"`
	UnitCode     string `json:"unit_code"`
	MinimumStock string `json:"minimum_stock"`
	Notes        string `json:"notes"`
	Active       *bool  `json:"active"`
}

type catalogSKURequest struct {
	ItemID  int64  `json:"item_id"`
	Name    string `json:"name"`
	Barcode string `json:"barcode"`
	Active  *bool  `json:"active"`
}

func (a *app) registerCatalogHandlers(mux *http.ServeMux) {
	mux.HandleFunc("GET /catalog", a.uiPage("catalog", "Catálogo", "catalog", "read"))
	mux.HandleFunc("GET /api/catalog/options", a.requireAuth("catalog", "read", a.handleCatalogOptions))
	mux.HandleFunc("GET /api/catalog/items", a.requireAuth("catalog", "read", a.handleCatalogItems))
	mux.HandleFunc("POST /api/catalog/items", a.requireAuth("catalog", "write", a.handleCreateCatalogItem))
	mux.HandleFunc("PATCH /api/catalog/items/{id}", a.requireAuth("catalog", "write", a.handleUpdateCatalogItem))
	mux.HandleFunc("POST /api/catalog/skus", a.requireAuth("catalog", "write", a.handleCreateCatalogSKU))
	mux.HandleFunc("PATCH /api/catalog/skus/{id}", a.requireAuth("catalog", "write", a.handleUpdateCatalogSKU))
}

func decodeJSON(w http.ResponseWriter, r *http.Request, dst any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	defer r.Body.Close()
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid json")
		return false
	}
	return true
}

func (a *app) handleCatalogOptions(w http.ResponseWriter, r *http.Request) {
	cats, err := simpleRows(r, a.db, `SELECT id,name FROM categories WHERE active=true ORDER BY name`)
	if err != nil {
		writeJSONError(w, 500, "internal error")
		return
	}
	units, err := simpleRows(r, a.db, `SELECT code,name FROM units WHERE active=true ORDER BY code`)
	if err != nil {
		writeJSONError(w, 500, "internal error")
		return
	}
	items, err := catalogItems(r, a.db, "")
	if err != nil {
		writeJSONError(w, 500, "internal error")
		return
	}
	writeJSON(w, 200, map[string]any{"categories": cats, "units": units, "items": items})
}

func simpleRows(r *http.Request, db *sql.DB, q string) ([]map[string]any, error) {
	rows, err := db.QueryContext(r.Context(), q)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var id any
		var name string
		if err := rows.Scan(&id, &name); err != nil {
			return nil, err
		}
		out = append(out, map[string]any{"id": id, "name": name})
	}
	return out, rows.Err()
}

func (a *app) handleCatalogItems(w http.ResponseWriter, r *http.Request) {
	out, err := catalogItems(r, a.db, strings.TrimSpace(r.URL.Query().Get("q")))
	if err != nil {
		writeJSONError(w, 500, "internal error")
		return
	}
	writeJSON(w, 200, out)
}

func catalogItems(r *http.Request, db *sql.DB, q string) ([]map[string]any, error) {
	arg := "%" + q + "%"
	rows, err := db.QueryContext(r.Context(), `SELECT i.id,i.name,COALESCE(c.name,''),i.unit_code,COALESCE(i.minimum_stock::text,''),COALESCE(i.notes,''),i.active FROM inventory_items i LEFT JOIN categories c ON c.id=i.category_id WHERE $1='' OR lower(i.name) LIKE lower($2) ORDER BY i.name LIMIT 50`, q, arg)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var id int64
		var name, category, unit, minimum, notes string
		var active bool
		if err := rows.Scan(&id, &name, &category, &unit, &minimum, &notes, &active); err != nil {
			return nil, err
		}
		out = append(out, map[string]any{"id": id, "name": name, "category": category, "unit": unit, "minimum_stock": minimum, "notes": notes, "active": active})
	}
	return out, rows.Err()
}

func (a *app) handleCreateCatalogItem(w http.ResponseWriter, r *http.Request) {
	var req catalogItemRequest
	if !decodeJSON(w, r, &req) || !validItemReq(req) {
		writeJSONError(w, http.StatusBadRequest, "invalid request")
		return
	}
	active := true
	if req.Active != nil {
		active = *req.Active
	}
	var id int64
	err := a.db.QueryRowContext(r.Context(), `INSERT INTO inventory_items (name,category_id,unit_code,minimum_stock,notes,active) VALUES ($1,$2,$3,$4,NULLIF($5,''),$6) RETURNING id`, strings.TrimSpace(req.Name), req.CategoryID, strings.TrimSpace(req.UnitCode), strings.TrimSpace(req.MinimumStock), strings.TrimSpace(req.Notes), active).Scan(&id)
	if err != nil {
		log.Printf("catalog item create failed error=%v", err)
		writeJSONError(w, http.StatusBadRequest, "item not created")
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"id": id})
}

func (a *app) handleUpdateCatalogItem(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var req catalogItemRequest
	if !decodeJSON(w, r, &req) || !validItemReq(req) {
		writeJSONError(w, http.StatusBadRequest, "invalid request")
		return
	}
	var currentUnit string
	if err := a.db.QueryRowContext(r.Context(), `SELECT unit_code FROM inventory_items WHERE id=$1`, id).Scan(&currentUnit); err != nil {
		writeJSONError(w, http.StatusNotFound, "item not found")
		return
	}
	if currentUnit != strings.TrimSpace(req.UnitCode) {
		var used bool
		if err := a.db.QueryRowContext(r.Context(), `SELECT EXISTS (SELECT 1 FROM movement_lines ml JOIN product_skus s ON s.id=ml.sku_id WHERE s.item_id=$1)`, id).Scan(&used); err != nil {
			writeJSONError(w, 500, "internal error")
			return
		}
		if used {
			log.Printf("catalog item unit change blocked item_id=%d", id)
			writeJSONError(w, http.StatusConflict, "Unidade não pode ser alterada porque já existe movimentação para este item.")
			return
		}
	}
	active := true
	if req.Active != nil {
		active = *req.Active
	}
	res, err := a.db.ExecContext(r.Context(), `UPDATE inventory_items SET name=$1,category_id=$2,unit_code=$3,minimum_stock=$4,notes=NULLIF($5,''),active=$6 WHERE id=$7`, strings.TrimSpace(req.Name), req.CategoryID, strings.TrimSpace(req.UnitCode), strings.TrimSpace(req.MinimumStock), strings.TrimSpace(req.Notes), active, id)
	if err != nil {
		log.Printf("catalog item update failed item_id=%d error=%v", id, err)
		writeJSONError(w, http.StatusBadRequest, "item not updated")
		return
	}
	if n, _ := res.RowsAffected(); n == 0 {
		writeJSONError(w, http.StatusNotFound, "item not found")
		return
	}
	writeJSON(w, 200, map[string]any{"id": id})
}

func (a *app) handleCreateCatalogSKU(w http.ResponseWriter, r *http.Request) {
	var req catalogSKURequest
	if !decodeJSON(w, r, &req) || !validSKUReq(req) {
		writeJSONError(w, http.StatusBadRequest, "invalid request")
		return
	}
	active := true
	if req.Active != nil {
		active = *req.Active
	}
	var id int64
	err := a.db.QueryRowContext(r.Context(), `INSERT INTO product_skus (item_id,name,barcode,active) VALUES ($1,$2,$3,$4) RETURNING id`, req.ItemID, strings.TrimSpace(req.Name), cleanBarcode(req.Barcode), active).Scan(&id)
	if err != nil {
		writeCatalogSKUError(w, err, "catalog sku create failed")
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"id": id})
}

func (a *app) handleUpdateCatalogSKU(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var req catalogSKURequest
	if !decodeJSON(w, r, &req) || strings.TrimSpace(req.Name) == "" || cleanBarcode(req.Barcode) == "" {
		writeJSONError(w, http.StatusBadRequest, "invalid request")
		return
	}
	active := true
	if req.Active != nil {
		active = *req.Active
	}
	res, err := a.db.ExecContext(r.Context(), `UPDATE product_skus SET name=$1,barcode=$2,active=$3 WHERE id=$4`, strings.TrimSpace(req.Name), cleanBarcode(req.Barcode), active, id)
	if err != nil {
		writeCatalogSKUError(w, err, "catalog sku update failed")
		return
	}
	if n, _ := res.RowsAffected(); n == 0 {
		writeJSONError(w, http.StatusNotFound, "sku not found")
		return
	}
	writeJSON(w, 200, map[string]any{"id": id})
}

func writeCatalogSKUError(w http.ResponseWriter, err error, logPrefix string) {
	log.Printf("%s error=%v", logPrefix, err)
	if strings.Contains(err.Error(), "product_skus_barcode") || strings.Contains(err.Error(), "idx_product_skus_barcode") {
		writeJSONError(w, http.StatusConflict, "Este código de barras já está cadastrado.")
		return
	}
	if strings.Contains(err.Error(), "foreign key") {
		writeJSONError(w, http.StatusBadRequest, "invalid item")
		return
	}
	writeJSONError(w, http.StatusBadRequest, "sku not saved")
}

func validItemReq(req catalogItemRequest) bool {
	return strings.TrimSpace(req.Name) != "" && req.CategoryID > 0 && strings.TrimSpace(req.UnitCode) != "" && nonNegativeDecimal(strings.TrimSpace(req.MinimumStock))
}

func validSKUReq(req catalogSKURequest) bool {
	return req.ItemID > 0 && strings.TrimSpace(req.Name) != "" && cleanBarcode(req.Barcode) != ""
}

func cleanBarcode(v string) string {
	return strings.TrimSpace(strings.ReplaceAll(strings.ReplaceAll(v, "\r", ""), "\n", ""))
}

func pathID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(strings.TrimSpace(r.PathValue("id")), 10, 64)
	if err != nil || id <= 0 {
		writeJSONError(w, http.StatusBadRequest, "invalid id")
		return 0, false
	}
	return id, true
}
