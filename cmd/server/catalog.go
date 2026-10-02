package main

import (
	"database/sql"
	"encoding/base64"
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
	Version      int64  `json:"version,omitempty"`
}

type catalogSKURequest struct {
	ItemID          int64  `json:"item_id"`
	Name            string `json:"name"`
	Barcode         string `json:"barcode"`
	ContentQuantity string `json:"content_quantity,omitempty"`
	Active          *bool  `json:"active"`
	Version         int64  `json:"version,omitempty"`
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
	writeJSON(w, 200, map[string]any{"categories": cats, "units": units})
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

const catalogPageSize = 30

type catalogPageCursor struct {
	Rank int    `json:"r"`
	Name string `json:"n"`
	ID   int64  `json:"i"`
}

type catalogPageResponse struct {
	Items      []map[string]any `json:"items"`
	HasMore    bool             `json:"has_more"`
	NextCursor string           `json:"next_cursor,omitempty"`
}

func encodeCatalogCursor(c catalogPageCursor) (string, error) {
	b, err := json.Marshal(c)
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

func decodeCatalogCursor(raw string) (catalogPageCursor, error) {
	var c catalogPageCursor
	b, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		return c, err
	}
	if err := json.Unmarshal(b, &c); err != nil {
		return c, err
	}
	if c.Rank < 0 || c.Rank > 1 || c.Name == "" || c.ID <= 0 {
		return c, errors.New("invalid catalog cursor")
	}
	return c, nil
}

func (a *app) handleCatalogItems(w http.ResponseWriter, r *http.Request) {
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	if r.URL.Query().Get("paged") == "1" {
		var cursor *catalogPageCursor
		if raw := strings.TrimSpace(r.URL.Query().Get("cursor")); raw != "" {
			decoded, err := decodeCatalogCursor(raw)
			if err != nil {
				writeJSONError(w, http.StatusBadRequest, "Cursor de catálogo inválido.")
				return
			}
			cursor = &decoded
		}
		out, err := catalogItemsPage(r, a.db, q, cursor)
		if err != nil {
			writeJSONError(w, 500, "Erro interno.")
			return
		}
		writeJSON(w, 200, out)
		return
	}
	out, err := catalogItems(r, a.db, q)
	if err != nil {
		writeJSONError(w, 500, "Erro interno.")
		return
	}
	writeJSON(w, 200, out)
}

func catalogItemsPage(r *http.Request, db *sql.DB, q string, cursor *catalogPageCursor) (catalogPageResponse, error) {
	arg := "%" + q + "%"
	hasCursor := cursor != nil
	cursorRank, cursorName, cursorID := 0, "", int64(0)
	if cursor != nil {
		cursorRank, cursorName, cursorID = cursor.Rank, cursor.Name, cursor.ID
	}
	rows, err := db.QueryContext(r.Context(), `
WITH parent_page AS (
    SELECT i.id,i.name,COALESCE(c.name,'') AS category,COALESCE(i.category_id,0) AS category_id,
           i.unit_code,COALESCE(i.minimum_stock::text,'') AS minimum_stock,
           COALESCE(i.notes,'') AS notes,i.active,i.version,
           CASE WHEN i.active THEN 0 ELSE 1 END AS active_rank,
           lower(i.name) AS sort_name
    FROM inventory_items i
    LEFT JOIN categories c ON c.id=i.category_id
    WHERE (
        $1='' OR lower(i.name) LIKE lower($2)
        OR EXISTS (
            SELECT 1 FROM product_skus sx
            WHERE sx.item_id=i.id
              AND (lower(COALESCE(sx.name,'')) LIKE lower($2)
                   OR lower(COALESCE(sx.brand,'')) LIKE lower($2)
                   OR COALESCE(sx.barcode,'') LIKE $2)
        )
    )
      AND (
        $3::boolean=false
        OR (CASE WHEN i.active THEN 0 ELSE 1 END, lower(i.name), i.id)
           > ($4::int, $5::text, $6::bigint)
      )
    ORDER BY active_rank,sort_name,i.id
    LIMIT 31
)
SELECT p.id,p.name,p.category,p.category_id,p.unit_code,p.minimum_stock,p.notes,p.active,p.version,
       (SELECT count(*) FROM product_skus s WHERE s.item_id=p.id) AS sku_count,
       p.active_rank,p.sort_name
FROM parent_page p
ORDER BY p.active_rank,p.sort_name,p.id
`, q, arg, hasCursor, cursorRank, cursorName, cursorID)
	if err != nil {
		return catalogPageResponse{}, err
	}
	defer rows.Close()

	items := make([]map[string]any, 0, catalogPageSize+1)
	cursors := make([]catalogPageCursor, 0, catalogPageSize+1)
	for rows.Next() {
		var id, categoryID, skuCount, version int64
		var name, category, unit, minimum, notes, sortName string
		var active bool
		var rank int
		if err := rows.Scan(&id, &name, &category, &categoryID, &unit, &minimum, &notes, &active, &version, &skuCount, &rank, &sortName); err != nil {
			return catalogPageResponse{}, err
		}
		items = append(items, map[string]any{"id": id, "name": name, "category": category, "category_id": categoryID, "unit": unit, "minimum_stock": minimum, "notes": notes, "active": active, "version": version, "sku_count": skuCount})
		cursors = append(cursors, catalogPageCursor{Rank: rank, Name: sortName, ID: id})
	}
	if err := rows.Err(); err != nil {
		return catalogPageResponse{}, err
	}

	out := catalogPageResponse{Items: items, HasMore: len(items) > catalogPageSize}
	if out.HasMore {
		out.Items = out.Items[:catalogPageSize]
		cursors = cursors[:catalogPageSize]
		next, err := encodeCatalogCursor(cursors[len(cursors)-1])
		if err != nil {
			return catalogPageResponse{}, err
		}
		out.NextCursor = next
	}
	return out, nil
}

func catalogItems(r *http.Request, db *sql.DB, q string) ([]map[string]any, error) {
	arg := "%" + q + "%"
	rows, err := db.QueryContext(r.Context(), `
SELECT i.id,i.name,COALESCE(c.name,''),COALESCE(i.category_id,0),i.unit_code,
       COALESCE(i.minimum_stock::text,''),COALESCE(i.notes,''),i.active,i.version,
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
		var id, categoryID, skuCount, version int64
		var name, category, unit, minimum, notes string
		var active bool
		if err := rows.Scan(&id, &name, &category, &categoryID, &unit, &minimum, &notes, &active, &version, &skuCount); err != nil {
			return nil, err
		}
		out = append(out, map[string]any{"id": id, "name": name, "category": category, "category_id": categoryID, "unit": unit, "minimum_stock": minimum, "notes": notes, "active": active, "version": version, "sku_count": skuCount})
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
SELECT s.id,s.item_id,COALESCE(s.name,''),COALESCE(s.brand,''),COALESCE(s.barcode,''),
       s.content_quantity::text,s.active,s.version,
       EXISTS(SELECT 1 FROM movement_lines mh WHERE mh.sku_id=s.id) AS has_history
FROM product_skus s
WHERE s.item_id=$1
ORDER BY s.active DESC,COALESCE(s.name,''),COALESCE(s.barcode,'')`, itemID)
	if err != nil {
		writeJSONError(w, 500, "Erro interno.")
		return
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var id, parentID, version int64
		var name, brand, barcode, contentQuantity string
		var active, hasHistory bool
		if err := rows.Scan(&id, &parentID, &name, &brand, &barcode, &contentQuantity, &active, &version, &hasHistory); err != nil {
			writeJSONError(w, 500, "Erro interno.")
			return
		}
		out = append(out, map[string]any{"id": id, "item_id": parentID, "name": name, "brand": brand, "barcode": barcode, "content_quantity": contentQuantity, "active": active, "version": version, "has_history": hasHistory})
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
		writeCatalogItemError(w, err, "catalog item create failed")
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"id": id, "version": int64(1)})
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
	if !validItemReq(req) || req.Version <= 0 {
		writeJSONError(w, http.StatusBadRequest, "Preencha nome, categoria, unidade, estoque mínimo e versão válidos.")
		return
	}
	var currentUnit string
	var currentVersion int64
	if err := a.db.QueryRowContext(r.Context(), `SELECT unit_code,version FROM inventory_items WHERE id=$1`, id).Scan(&currentUnit, &currentVersion); errors.Is(err, sql.ErrNoRows) {
		writeJSONError(w, http.StatusNotFound, "Item pai não encontrado.")
		return
	} else if err != nil {
		writeJSONError(w, 500, "Erro interno.")
		return
	}
	if currentVersion != req.Version {
		writeConcurrentEditError(w)
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
			writeJSONError(w, http.StatusConflict, "Unidade não pode ser alterada porque já existe movimentação para este item.")
			return
		}
	}
	active := true
	if req.Active != nil {
		active = *req.Active
	}
	var newVersion int64
	err := a.db.QueryRowContext(r.Context(), `
UPDATE inventory_items
SET name=$1,category_id=$2,unit_code=$3,minimum_stock=$4,notes=NULLIF($5,''),active=$6,version=version+1
WHERE id=$7 AND version=$8
RETURNING version`, req.Name, req.CategoryID, req.UnitCode, req.MinimumStock, req.Notes, active, id, req.Version).Scan(&newVersion)
	if errors.Is(err, sql.ErrNoRows) {
		writeConcurrentEditError(w)
		return
	}
	if err != nil {
		writeCatalogItemError(w, err, "catalog item update failed")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"id": id, "version": newVersion})
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
	err := a.db.QueryRowContext(r.Context(), `INSERT INTO product_skus (item_id,name,barcode,content_quantity,active) VALUES ($1,$2,$3,$4,$5) RETURNING id`, req.ItemID, req.Name, req.Barcode, req.ContentQuantity, active).Scan(&id)
	if err != nil {
		writeCatalogSKUError(w, err, "catalog sku create failed")
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"id": id, "version": int64(1)})
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
	if req.Name == "" || req.Barcode == "" || req.Version <= 0 {
		writeJSONError(w, http.StatusBadRequest, "Preencha o produto/SKU, código de barras e versão válidos.")
		return
	}
	var currentItem, currentVersion int64
	var currentUnit, targetUnit, currentContentQuantity string
	var hasHistory bool
	err := a.db.QueryRowContext(r.Context(), `
SELECT s.item_id,s.version,src.unit_code,s.content_quantity::text,EXISTS(SELECT 1 FROM movement_lines ml WHERE ml.sku_id=s.id)
FROM product_skus s JOIN inventory_items src ON src.id=s.item_id WHERE s.id=$1`, id).Scan(&currentItem, &currentVersion, &currentUnit, &currentContentQuantity, &hasHistory)
	if errors.Is(err, sql.ErrNoRows) {
		writeJSONError(w, http.StatusNotFound, "SKU não encontrado.")
		return
	}
	if err != nil {
		writeJSONError(w, 500, "Erro interno.")
		return
	}
	if currentVersion != req.Version {
		writeConcurrentEditError(w)
		return
	}
	if req.ItemID <= 0 {
		req.ItemID = currentItem
	}
	if req.ContentQuantity == "" {
		req.ContentQuantity = currentContentQuantity
	}
	if !positiveDecimal(req.ContentQuantity) {
		writeJSONError(w, http.StatusBadRequest, "Informe um conteúdo por apresentação maior que zero.")
		return
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
	var newVersion int64
	err = a.db.QueryRowContext(r.Context(), `
UPDATE product_skus
SET item_id=$1,name=$2,barcode=$3,content_quantity=$4,active=$5,version=version+1
WHERE id=$6 AND version=$7
RETURNING version`, req.ItemID, req.Name, req.Barcode, req.ContentQuantity, active, id, req.Version).Scan(&newVersion)
	if errors.Is(err, sql.ErrNoRows) {
		writeConcurrentEditError(w)
		return
	}
	if err != nil {
		writeCatalogSKUError(w, err, "catalog sku update failed")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"id": id, "item_id": req.ItemID, "moved": currentItem != req.ItemID, "version": newVersion})
}

func writeCatalogItemError(w http.ResponseWriter, err error, logPrefix string) {
	log.Printf("%s error=%v", logPrefix, err)
	if strings.Contains(err.Error(), "idx_inventory_items_normalized_name_unique") {
		writeJSONError(w, http.StatusConflict, "Já existe um item pai com este nome.")
		return
	}
	writeJSONError(w, http.StatusBadRequest, "Não foi possível salvar o item pai.")
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
	req.ContentQuantity = normalizeDecimalInput(req.ContentQuantity)
	if req.ContentQuantity == "" && req.Version == 0 {
		req.ContentQuantity = "1"
	}
}

func validItemReq(req catalogItemRequest) bool {
	return req.Name != "" && req.CategoryID > 0 && req.UnitCode != "" && nonNegativeDecimal(req.MinimumStock)
}

func validSKUReq(req catalogSKURequest) bool {
	return req.ItemID > 0 && req.Name != "" && req.Barcode != "" && positiveDecimal(req.ContentQuantity)
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
