package main

import (
	"database/sql"
	"errors"
	"net/http"
	"strconv"
	"strings"
)

type brandRequest struct {
	Name    string `json:"name"`
	Active  *bool  `json:"active"`
	Version int64  `json:"version,omitempty"`
}

func (a *app) handleCatalogBrands(w http.ResponseWriter, r *http.Request) {
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	rows, err := a.db.QueryContext(r.Context(), `
SELECT id,name,active,version
FROM brands
WHERE $1='' OR lower(name) LIKE lower($2)
ORDER BY active DESC,name,id
LIMIT 100`, q, "%"+q+"%")
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "Erro interno.")
		return
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var id, version int64
		var name string
		var active bool
		if err := rows.Scan(&id,&name,&active,&version); err != nil {
			writeJSONError(w, http.StatusInternalServerError, "Erro interno.")
			return
		}
		out=append(out,map[string]any{"id":id,"name":name,"active":active,"version":version})
	}
	writeJSON(w,http.StatusOK,out)
}

func (a *app) handleCreateCatalogBrand(w http.ResponseWriter, r *http.Request) {
	var req brandRequest
	if !decodeJSON(w,r,&req) { return }
	req.Name=strings.TrimSpace(req.Name)
	if req.Name=="" {
		writeJSONError(w,http.StatusBadRequest,"Informe o nome da marca.")
		return
	}
	active:=true
	if req.Active!=nil { active=*req.Active }
	var id int64
	err:=a.db.QueryRowContext(r.Context(),`
INSERT INTO brands(name,active) VALUES ($1,$2)
RETURNING id`,req.Name,active).Scan(&id)
	if err!=nil {
		if strings.Contains(err.Error(),"idx_brands_normalized_name_unique") {
			writeJSONError(w,http.StatusConflict,req.Name+" já existe no banco de dados.")
			return
		}
		writeJSONError(w,http.StatusBadRequest,"Não foi possível salvar a marca.")
		return
	}
	writeJSON(w,http.StatusCreated,map[string]any{"id":id,"version":int64(1)})
}

func (a *app) handleUpdateCatalogBrand(w http.ResponseWriter, r *http.Request) {
	id,ok:=pathID(w,r)
	if !ok { return }
	var req brandRequest
	if !decodeJSON(w,r,&req) { return }
	req.Name=strings.TrimSpace(req.Name)
	if req.Name=="" || req.Version<=0 {
		writeJSONError(w,http.StatusBadRequest,"Informe marca e versão válidas.")
		return
	}
	active:=true
	if req.Active!=nil { active=*req.Active }
	var version int64
	err:=a.db.QueryRowContext(r.Context(),`
UPDATE brands
SET name=$1,active=$2,version=version+1
WHERE id=$3 AND version=$4
RETURNING version`,req.Name,active,id,req.Version).Scan(&version)
	if errors.Is(err,sql.ErrNoRows) {
		writeConcurrentEditError(w)
		return
	}
	if err!=nil {
		if strings.Contains(err.Error(),"idx_brands_normalized_name_unique") {
			writeJSONError(w,http.StatusConflict,req.Name+" já existe no banco de dados.")
			return
		}
		writeJSONError(w,http.StatusBadRequest,"Não foi possível atualizar a marca.")
		return
	}
	writeJSON(w,http.StatusOK,map[string]any{"id":id,"version":version})
}

func (a *app) handleCatalogProducts(w http.ResponseWriter, r *http.Request) {
	q:=strings.TrimSpace(r.URL.Query().Get("q"))
	rows,err:=a.db.QueryContext(r.Context(),`
SELECT s.id,s.item_id,i.name,s.brand_id,b.name,s.content_quantity::text,
       COALESCE(s.barcode,''),COALESCE(s.name,''),s.active,s.version,i.unit_code,
       EXISTS(SELECT 1 FROM movement_lines ml WHERE ml.sku_id=s.id)
FROM product_skus s
JOIN inventory_items i ON i.id=s.item_id
JOIN brands b ON b.id=s.brand_id
WHERE $1='' OR lower(i.name) LIKE lower($2) OR lower(b.name) LIKE lower($2)
   OR lower(COALESCE(s.name,'')) LIKE lower($2) OR COALESCE(s.barcode,'') LIKE $2
ORDER BY s.active DESC,i.name,b.name,s.content_quantity,s.id
LIMIT 150`,q,"%"+q+"%")
	if err!=nil {
		writeJSONError(w,http.StatusInternalServerError,"Erro interno.")
		return
	}
	defer rows.Close()
	out:=[]map[string]any{}
	for rows.Next() {
		var id,itemID,brandID,version int64
		var item,brand,content,barcode,name,unit string
		var active,hasHistory bool
		if err:=rows.Scan(&id,&itemID,&item,&brandID,&brand,&content,&barcode,&name,&active,&version,&unit,&hasHistory);err!=nil {
			writeJSONError(w,http.StatusInternalServerError,"Erro interno.")
			return
		}
		out=append(out,map[string]any{
			"id":id,"item_id":itemID,"item":item,"brand_id":brandID,"brand":brand,
			"content_quantity":content,"barcode":barcode,"name":name,"active":active,
			"version":version,"unit":unit,"has_history":hasHistory,
		})
	}
	writeJSON(w,http.StatusOK,out)
}

func productDisplayName(item, brand, content, unit string) string {
	content=strings.TrimSpace(content)
	if strings.Contains(content,".") {
		content=strings.TrimRight(content,"0")
		content=strings.TrimRight(content,".")
	}
	return strings.TrimSpace(item)+" - "+strings.TrimSpace(brand)+" - "+content+" "+strings.TrimSpace(unit)
}

func parseBrandID(raw string) int64 {
	id,_:=strconv.ParseInt(strings.TrimSpace(raw),10,64)
	return id
}
