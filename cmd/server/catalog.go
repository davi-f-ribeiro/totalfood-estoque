package main

import (
	"database/sql"
	"encoding/json"
	"errors"
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
	mux.HandleFunc("GET /api/catalog/items/{id}/skus", a.requireAuth("catalog", "read", a.handleCatalogItemSKUs))
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
		writeJSONError(w, http.StatusBadRequest, "JSON inválido.")
		return false
	}
	return true
}

func (a *app) handleCatalogOptions(w http.ResponseWriter, r *http.Request) {
	cats, err := simpleRows(r, a.db, `SELECT id,name FROM categories WHERE active=true ORDER BY name`)
	if err != nil {
		writeJSONError(w, 500, "Erro interno.")
		return
	}
	units, err := simpleRows(r, a.db, `SELECT code,name FROM units WHERE active=true ORDER BY code`)
	if err != nil {
		writeJSONError(w, 500, "Erro interno.")
		return
	}
	items, err := catalogItems(r, a.db, "")
	if err != nil {
		writeJSONError(w, 500, "Erro interno.")
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
		writeJSONError(w, 500, "Erro interno.")
		return
	}
	writeJSON(w, 200, out)
}

func catalogItems(r *http.Request, db *sql.DB, q string) ([]map[string]any, error) {
	arg := "%" + q + "%"
	rows, err := db.QueryContext(r.Context(), `
SELECT i.id,i.name,COALESCE(c.name,''),COALESCE(i.category_id,0),i.unit_code,
       COALESCE(i.minimum_stock::text,''),COALESCE(i.notes,''),i.active,
       count(s.id) AS sku_count
FROM inventory_items i
LEFT JOIN categories c ON c.id=i.category_id
LEFT JOIN product_skus s ON s.item_id=i.id
WHERE $1='' OR lower(i.name) LIKE lower($2)
   OR EXISTS (
       SELECT 1 FROM product_skus sx
       WHERE sx.item_id=i.id
         AND (lower(COALESCE(sx.name,'')) LIKE lower($2)
              OR lower(COALESCE(sx.brand,'')) LIKE lower($2)
              OR COALESCE(sx.barcode,'') LIKE $2)
   )
GROUP BY i.id,c.name
ORDER BY i.active DESC,i.name
LIMIT 50`, q, arg)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var id, categoryID, skuCount int64
		var name, category, unit, minimum, notes string
		var active bool
		if err := rows.Scan(&id, &name, &category, &categoryID, &unit, &minimum, &notes, &active, &skuCount); err != nil {
			return nil, err
		}
		out = append(out, map[string]any{"id": id, "name": name, "category": category, "category_id": categoryID, "unit": unit, "minimum_stock": minimum, "notes": notes, "active": active, "sku_count": skuCount})
	}
	return out, rows.Err()
}

func (a *app) handleCatalogItemSKUs(w http.ResponseWriter, r *http.Request) {
	itemID, ok := pathID(w, r)
	if !ok {
		return
	}
	var exists bool
	if err := a.db.QueryRowContext(r.Context(), `SELECT EXISTS(SELECT 1 FROM inventory_items WHERE id=$1)`, itemID).Scan(&exists); err != nil {
		writeJSONError(w, 500, "Erro interno.")
		return
	}
	if !exists {
		writeJSONError(w, http.StatusNotFound, "Item pai não encontrado.")
		return
	}
	rows, err := a.db.QueryContext(r.Context(), `
SELECT s.id,s.item_id,COALESCE(s.name,''),COALESCE(s.brand,''),COALESCE(s.barcode,''),s.active,
       COALESCE(sum(CASE WHEN ml.direction='IN' THEN ml.quantity ELSE -ml.quantity END),0)::text AS balance,
       EXISTS(SELECT 1 FROM movement_lines mh WHERE mh.sku_id=s.id) AS has_history
FROM product_skus s
LEFT JOIN movement_lines ml ON ml.sku_id=s.id
WHERE s.item_id=$1
GROUP BY s.id
ORDER BY s.active DESC,COALESCE(s.name,''),COALESCE(s.barcode,'')`, itemID)
	if err != nil {
		writeJSONError(w, 500, "Erro interno.")
		return
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var id, parentID int64
		var name, brand, barcode, balance string
		var active, hasHistory bool
		if err := rows.Scan(&id, &parentID, &name, &brand, &barcode, &active, &balance, &hasHistory); err != nil {
			writeJSONError(w, 500, "Erro interno.")
			return
		}
		out = append(out, map[string]any{"id": id, "item_id": parentID, "name": name, "brand": brand, "barcode": barcode, "active": active, "balance": balance, "has_history": hasHistory})
	}
	writeJSON(w, 200, out)
}

func (a *app) handleCreateCatalogItem(w http.ResponseWriter, r *http.Request) {
	var req catalogItemRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	normalizeCatalogItemRequest(&req)
	if !validItemReq(req) {
		writeJSONError(w, http.StatusBadRequest, "Preencha nome, categoria, unidade e estoque mínimo válido.")
		return
	}
	var duplicateID int64
	err := a.db.QueryRowContext(r.Context(), `SELECT id FROM inventory_items WHERE lower(btrim(name))=lower(btrim($1)) LIMIT 1`, req.Name).Scan(&duplicateID)
	if err == nil {
		writeJSONError(w, http.StatusConflict, "Já existe um item pai com este nome.")
		return
	}
	if !errors.Is(err, sql.ErrNoRows) {
		writeJSONError(w, 500, "Erro interno.")
		return
	}
	active := true
	if req.Active != nil {
		active = *req.Active
	}
	var id int64
	err = a.db.QueryRowContext(r.Context(), `INSERT INTO inventory_items (name,category_id,unit_code,minimum_stock,notes,active) VALUES ($1,$2,$3,$4,NULLIF($5,''),$6) RETURNING id`, req.Name, req.CategoryID, req.UnitCode, req.MinimumStock, req.Notes, active).Scan(&id)
	if err != nil {
		log.Printf("catalog item create failed error=%v", err)
		writeJSONError(w, http.StatusBadRequest, "Não foi possível criar o item pai.")
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
	if !decodeJSON(w, r, &req) {
		return
	}
	normalizeCatalogItemRequest(&req)
	if !validItemReq(req) {
		writeJSONError(w, http.StatusBadRequest, "Preencha nome, categoria, unidade e estoque mínimo válido.")
		return
	}
	var currentUnit string
	if err := a.db.QueryRowContext(r.Context(), `SELECT unit_code FROM inventory_items WHERE id=$1`, id).Scan(&currentUnit); errors.Is(err, sql.ErrNoRows) {
		writeJSONError(w, http.StatusNotFound, "Item pai não encontrado.")
		return
	} else if err != nil {
		writeJSONError(w, 500, "Erro interno.")
		return
	}
	var duplicate bool
	if err := a.db.QueryRowContext(r.Context(), `SELECT EXISTS(SELECT 1 FROM inventory_items WHERE id<>$1 AND lower(btrim(name))=lower(btrim($2)))`, id, req.Name).Scan(&duplicate); err != nil {
		writeJSONError(w, 500, "Erro interno.")
		return
	}
	if duplicate {
		writeJSONError(w, http.StatusConflict, "Já existe outro item pai com este nome.")
		return
	}
	if currentUnit != req.UnitCode {
		var used bool
		if err := a.db.QueryRowContext(r.Context(), `SELECT EXISTS (SELECT 1 FROM movement_lines ml JOIN product_skus s ON s.id=ml.sku_id WHERE s.item_id=$1)`, id).Scan(&used); err != nil {
			writeJSONError(w, 500, "Erro interno.")
			return
		}
		if used {
			writeJSONError(w, http.StatusConflict, "A unidade não pode ser alterada porque este item possui movimentações.")
			return
		}
	}
	active := true
	if req.Active != nil {
		active = *req.Active
	}
	res, err := a.db.ExecContext(r.Context(), `UPDATE inventory_items SET name=$1,category_id=$2,unit_code=$3,minimum_stock=$4,notes=NULLIF($5,''),active=$6 WHERE id=$7`, req.Name, req.CategoryID, req.UnitCode, req.MinimumStock, req.Notes, active, id)
	if err != nil {
		log.Printf("catalog item update failed item_id=%d error=%v", id, err)
		writeJSONError(w, http.StatusBadRequest, "Não foi possível salvar o item pai.")
		return
	}
	if n, _ := res.RowsAffected(); n == 0 {
		writeJSONError(w, http.StatusNotFound, "Item pai não encontrado.")
		return
	}
	writeJSON(w, 200, map[string]any{"id": id})
}

func (a *app) handleCreateCatalogSKU(w http.ResponseWriter, r *http.Request) {
	var req catalogSKURequest
	if !decodeJSON(w, r, &req) {
		return
	}
	normalizeCatalogSKURequest(&req)
	if !validSKUReq(req) {
		writeJSONError(w, http.StatusBadRequest, "Preencha o produto/SKU, código de barras e item pai.")
		return
	}
	active := true
	if req.Active != nil {
		active = *req.Active
	}
	var id int64
	err := a.db.QueryRowContext(r.Context(), `INSERT INTO product_skus (item_id,name,barcode,active) VALUES ($1,$2,$3,$4) RETURNING id`, req.ItemID, req.Name, req.Barcode, active).Scan(&id)
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
	if !decodeJSON(w, r, &req) {
		return
	}
	normalizeCatalogSKURequest(&req)
	if req.Name == "" || req.Barcode == "" {
		writeJSONError(w, http.StatusBadRequest, "Preencha o produto/SKU e código de barras.")
		return
	}
	var currentItem int64
	var currentUnit, targetUnit string
	var hasHistory bool
	err := a.db.QueryRowContext(r.Context(), `
SELECT s.item_id,src.unit_code,EXISTS(SELECT 1 FROM movement_lines ml WHERE ml.sku_id=s.id)
FROM product_skus s JOIN inventory_items src ON src.id=s.item_id WHERE s.id=$1`, id).Scan(&currentItem, &currentUnit, &hasHistory)
	if errors.Is(err, sql.ErrNoRows) {
		writeJSONError(w, http.StatusNotFound, "SKU não encontrado.")
		return
	}
	if err != nil {
		writeJSONError(w, 500, "Erro interno.")
		return
	}
	if req.ItemID <= 0 {
		req.ItemID = currentItem
	}
	if err := a.db.QueryRowContext(r.Context(), `SELECT unit_code FROM inventory_items WHERE id=$1`, req.ItemID).Scan(&targetUnit); errors.Is(err, sql.ErrNoRows) {
		writeJSONError(w, http.StatusBadRequest, "Item pai de destino não encontrado.")
		return
	} else if err != nil {
		writeJSONError(w, 500, "Erro interno.")
		return
	}
	if currentItem != req.ItemID && hasHistory && currentUnit != targetUnit {
		writeJSONError(w, http.StatusConflict, "Este SKU possui histórico e só pode ser movido para um item pai com a mesma unidade.")
		return
	}
	active := true
	if req.Active != nil {
		active = *req.Active
	}
	res, err := a.db.ExecContext(r.Context(), `UPDATE product_skus SET item_id=$1,name=$2,barcode=$3,active=$4 WHERE id=$5`, req.ItemID, req.Name, req.Barcode, active, id)
	if err != nil {
		writeCatalogSKUError(w, err, "catalog sku update failed")
		return
	}
	if n, _ := res.RowsAffected(); n == 0 {
		writeJSONError(w, http.StatusNotFound, "SKU não encontrado.")
		return
	}
	writeJSON(w, 200, map[string]any{"id": id, "item_id": req.ItemID, "moved": currentItem != req.ItemID})
}

func writeCatalogSKUError(w http.ResponseWriter, err error, logPrefix string) {
	log.Printf("%s error=%v", logPrefix, err)
	if strings.Contains(err.Error(), "product_skus_barcode") || strings.Contains(err.Error(), "idx_product_skus_barcode") {
		writeJSONError(w, http.StatusConflict, "Este código de barras já está cadastrado.")
		return
	}
	if strings.Contains(err.Error(), "foreign key") {
		writeJSONError(w, http.StatusBadRequest, "Item pai inválido.")
		return
	}
	writeJSONError(w, http.StatusBadRequest, "Não foi possível salvar o SKU.")
}

func normalizeCatalogItemRequest(req *catalogItemRequest) {
	req.Name = strings.TrimSpace(req.Name)
	req.UnitCode = strings.TrimSpace(req.UnitCode)
	req.MinimumStock = normalizeDecimalInput(req.MinimumStock)
	req.Notes = strings.TrimSpace(req.Notes)
}

func normalizeCatalogSKURequest(req *catalogSKURequest) {
	req.Name = strings.TrimSpace(req.Name)
	req.Barcode = cleanBarcode(req.Barcode)
}

func validItemReq(req catalogItemRequest) bool {
	return req.Name != "" && req.CategoryID > 0 && req.UnitCode != "" && nonNegativeDecimal(req.MinimumStock)
}

func validSKUReq(req catalogSKURequest) bool {
	return req.ItemID > 0 && req.Name != "" && req.Barcode != ""
}

func cleanBarcode(v string) string {
	return strings.TrimSpace(strings.ReplaceAll(strings.ReplaceAll(v, "\r", ""), "\n", ""))
}

func pathID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(strings.TrimSpace(r.PathValue("id")), 10, 64)
	if err != nil || id <= 0 {
		writeJSONError(w, http.StatusBadRequest, "ID inválido.")
		return 0, false
	}
	return id, true
}
