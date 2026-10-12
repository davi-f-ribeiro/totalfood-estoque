package main

import (
	"context"
	"database/sql"
	"errors"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"
)

type inventoryListItem struct {
	ID            int64      `json:"id"`
	Type          string     `json:"type"`
	WarehouseID   int64      `json:"warehouse_id"`
	Warehouse     string     `json:"warehouse"`
	Responsible  string     `json:"responsible"`
	StartedAt     *time.Time `json:"started_at,omitempty"`
	Status        string     `json:"status"`
	Counted       int        `json:"counted"`
	Total         int        `json:"total"`
}

type inventoryListResponse struct {
	Items  []inventoryListItem `json:"items"`
	Limit  int                 `json:"limit"`
	Offset int                 `json:"offset"`
	More   bool                `json:"more"`
}

func (a *app) registerInventoryAdminHandlers(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/inventory-validations", a.requireAuth("inventory_validation", "read", a.handleListInventoryValidations))
	mux.HandleFunc("GET /api/inventory-validations/warehouses", a.requireAuth("inventory_validation", "read", a.handleInventoryWarehouses))
}

func (a *app) handleInventoryWarehouses(w http.ResponseWriter, r *http.Request) {
	rows, err := a.db.QueryContext(r.Context(), `
SELECT id,name FROM warehouses WHERE active=true ORDER BY name,id`)
	if err != nil {
		log.Printf("inventory warehouses failed: %v", err)
		writeJSONError(w, http.StatusInternalServerError, "Erro interno.")
		return
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var id int64
		var name string
		if err := rows.Scan(&id, &name); err != nil {
			writeJSONError(w, http.StatusInternalServerError, "Erro interno.")
			return
		}
		out = append(out, map[string]any{"id": id, "name": name})
	}
	if err := rows.Err(); err != nil {
		writeJSONError(w, http.StatusInternalServerError, "Erro interno.")
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (a *app) handleListInventoryValidations(w http.ResponseWriter, r *http.Request) {
	status := strings.TrimSpace(strings.ToUpper(r.URL.Query().Get("status")))
	typ := strings.TrimSpace(strings.ToUpper(r.URL.Query().Get("type")))
	warehouseID, err := parseOptionalPositiveInt64(r.URL.Query().Get("warehouse_id"))
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, "Depósito inválido.")
		return
	}
	responsible := strings.TrimSpace(r.URL.Query().Get("responsible"))
	from, err := parseInventoryDate(r.URL.Query().Get("from"))
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, "Data inicial inválida.")
		return
	}
	to, err := parseInventoryDate(r.URL.Query().Get("to"))
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, "Data final inválida.")
		return
	}
	if status != "" && !inventoryListStatus(status) {
		writeJSONError(w, http.StatusBadRequest, "Filtro de status inválido.")
		return
	}
	if typ != "" && typ != "TOTAL" && typ != "BLIND" {
		writeJSONError(w, http.StatusBadRequest, "Filtro de tipo inválido.")
		return
	}
	limit := 30
	if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 || n > 100 {
			writeJSONError(w, http.StatusBadRequest, "Paginação inválida.")
			return
		}
		limit = n
	}
	offset := 0
	if raw := strings.TrimSpace(r.URL.Query().Get("offset")); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 0 {
			writeJSONError(w, http.StatusBadRequest, "Paginação inválida.")
			return
		}
		offset = n
	}

	out, err := a.listInventoryValidations(r.Context(), status, typ, warehouseID, responsible, from, to, limit, offset)
	if err != nil {
		log.Printf("inventory list failed: %v", err)
		writeJSONError(w, http.StatusInternalServerError, "Erro interno.")
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func inventoryListStatus(v string) bool {
	switch v {
	case "OPEN", "COUNTING", "REVIEW", "COMPLETED", "CANCELLED":
		return true
	default:
		return false
	}
}

func parseOptionalPositiveInt64(v string) (int64, error) {
	v = strings.TrimSpace(v)
	if v == "" {
		return 0, nil
	}
	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil || n <= 0 {
		return 0, errors.New("invalid positive integer")
	}
	return n, nil
}

func parseInventoryDate(v string) (*time.Time, error) {
	v = strings.TrimSpace(v)
	if v == "" {
		return nil, nil
	}
	t, err := time.Parse("2006-01-02", v)
	if err != nil {
		return nil, err
	}
	return &t, nil
}

func (a *app) listInventoryValidations(ctx context.Context, status, typ string, warehouseID int64, responsible string, from, to *time.Time, limit, offset int) (inventoryListResponse, error) {
	rows, err := a.db.QueryContext(ctx, `
SELECT v.id,v.type,v.warehouse_id,w.name,COALESCE(u.login,''),v.started_at,v.status,
       count(l.id) FILTER (WHERE l.physical_quantity IS NOT NULL)::int,
       count(l.id)::int
FROM inventory_validations v
JOIN warehouses w ON w.id=v.warehouse_id
LEFT JOIN users u ON u.id=v.responsible_user_id
LEFT JOIN inventory_validation_lines l ON l.validation_id=v.id
WHERE ($1='' OR v.status=$1)
  AND ($2='' OR v.type=$2)
  AND ($3::bigint=0 OR v.warehouse_id=$3)
  AND ($4='' OR lower(COALESCE(u.login,'')) LIKE lower('%'||$4||'%'))
  AND ($5::timestamptz IS NULL OR v.started_at >= $5)
  AND ($6::timestamptz IS NULL OR v.started_at < $6 + interval '1 day')
GROUP BY v.id,w.name,u.login
ORDER BY v.started_at DESC NULLS LAST,v.id DESC
LIMIT $7 OFFSET $8`, status, typ, warehouseID, responsible, nullableTime(from), nullableTime(to), limit+1, offset)
	if err != nil {
		return inventoryListResponse{}, err
	}
	defer rows.Close()
	out := inventoryListResponse{Items: []inventoryListItem{}, Limit: limit, Offset: offset}
	for rows.Next() {
		var item inventoryListItem
		var started sql.NullTime
		if err := rows.Scan(&item.ID,&item.Type,&item.WarehouseID,&item.Warehouse,&item.Responsible,&started,&item.Status,&item.Counted,&item.Total); err != nil {
			return inventoryListResponse{}, err
		}
		if started.Valid {
			t := started.Time
			item.StartedAt = &t
		}
		out.Items = append(out.Items,item)
	}
	if err := rows.Err(); err != nil {
		return inventoryListResponse{}, err
	}
	if len(out.Items) > limit {
		out.More = true
		out.Items = out.Items[:limit]
	}
	return out,nil
}

func nullableTime(t *time.Time) any {
	if t == nil {
		return nil
	}
	return *t
}
