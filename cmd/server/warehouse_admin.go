package main

import (
	"database/sql"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5/pgconn"
)

var errWarehouseInactive = errors.New("warehouse inactive")

type warehouseAdminRequest struct {
	Name string `json:"name"`
}

func (a *app) registerWarehouseAdminHandlers(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/admin/warehouses", a.requireAuth("warehouses", "write", a.handleAdminWarehouses))
	mux.HandleFunc("POST /api/admin/warehouses", a.requireAuth("warehouses", "write", a.handleAdminCreateWarehouse))
	mux.HandleFunc("POST /api/admin/warehouses/{id}/activate", a.requireAuth("warehouses", "write", a.handleAdminActivateWarehouse))
	mux.HandleFunc("POST /api/admin/warehouses/{id}/deactivate", a.requireAuth("warehouses", "write", a.handleAdminDeactivateWarehouse))
}

func (a *app) handleAdminWarehouses(w http.ResponseWriter, r *http.Request) {
	rows, err := a.db.QueryContext(r.Context(), `
		SELECT w.id,w.name,w.active,w.created_at::text,COALESCE(b.nonzero_skus,0),COALESCE(a.asset_count,0)
		FROM warehouses w
		LEFT JOIN (
			SELECT warehouse_id,count(*)::bigint AS nonzero_skus
			FROM (
				SELECT warehouse_id,sku_id
				FROM movement_lines
				GROUP BY warehouse_id,sku_id
				HAVING SUM(CASE direction WHEN 'IN' THEN quantity ELSE -quantity END) <> 0
			) balances
			GROUP BY warehouse_id
		) b ON b.warehouse_id=w.id
		LEFT JOIN (
			SELECT current_warehouse_id AS warehouse_id,count(*)::bigint AS asset_count
			FROM assets
			WHERE current_warehouse_id IS NOT NULL
			GROUP BY current_warehouse_id
		) a ON a.warehouse_id=w.id
		ORDER BY w.name`)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "internal error")
		return
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var id int64
		var name, created string
		var active bool
		var nonZeroSKUs, assetCount int64
		if err := rows.Scan(&id, &name, &active, &created, &nonZeroSKUs, &assetCount); err != nil {
			writeJSONError(w, http.StatusInternalServerError, "internal error")
			return
		}
		out = append(out, map[string]any{"id": id, "name": name, "active": active, "created_at": created, "nonzero_skus": nonZeroSKUs, "asset_count": assetCount, "empty": nonZeroSKUs == 0 && assetCount == 0})
	}
	if err := rows.Err(); err != nil {
		writeJSONError(w, http.StatusInternalServerError, "internal error")
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (a *app) handleAdminCreateWarehouse(w http.ResponseWriter, r *http.Request) {
	var req warehouseAdminRequest
	if !decodeWarehouseAdminJSON(w, r, &req) {
		return
	}
	name := strings.TrimSpace(req.Name)
	if name == "" {
		writeJSONError(w, http.StatusBadRequest, "invalid warehouse name")
		return
	}
	tx, err := a.db.BeginTx(r.Context(), &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "internal error")
		return
	}
	defer tx.Rollback()
	var id int64
	err = tx.QueryRowContext(r.Context(), `INSERT INTO warehouses(name,active) VALUES($1,false) RETURNING id`, name).Scan(&id)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			writeJSONError(w, http.StatusConflict, "warehouse name already exists")
			return
		}
		log.Printf("warehouse create failed error=%v", err)
		writeJSONError(w, http.StatusInternalServerError, "internal error")
		return
	}
	u := userFromContext(r.Context())
	if _, err = tx.ExecContext(r.Context(), `INSERT INTO warehouse_audit(warehouse_id,action,user_id) VALUES($1,'CREATED',$2)`, id, u.ID); err != nil {
		writeJSONError(w, http.StatusInternalServerError, "internal error")
		return
	}
	if err = tx.Commit(); err != nil {
		writeJSONError(w, http.StatusInternalServerError, "internal error")
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"id": id, "name": name, "active": false})
}

func (a *app) handleAdminActivateWarehouse(w http.ResponseWriter, r *http.Request) {
	a.setWarehouseActive(w, r, true)
}

func (a *app) handleAdminDeactivateWarehouse(w http.ResponseWriter, r *http.Request) {
	a.setWarehouseActive(w, r, false)
}

func (a *app) setWarehouseActive(w http.ResponseWriter, r *http.Request, active bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		writeJSONError(w, http.StatusBadRequest, "invalid warehouse")
		return
	}
	tx, err := a.db.BeginTx(r.Context(), &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "internal error")
		return
	}
	defer tx.Rollback()
	var current bool
	var name string
	if err = tx.QueryRowContext(r.Context(), `SELECT name,active FROM warehouses WHERE id=$1 FOR UPDATE`, id).Scan(&name, &current); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeJSONError(w, http.StatusNotFound, "warehouse not found")
		} else {
			writeJSONError(w, http.StatusInternalServerError, "internal error")
		}
		return
	}
	if current == active {
		writeJSON(w, http.StatusOK, map[string]any{"id": id, "name": name, "active": active})
		return
	}
	if !active {
		var nonZero bool
		err = tx.QueryRowContext(r.Context(), `
			SELECT EXISTS(
				SELECT 1
				FROM movement_lines
				WHERE warehouse_id=$1
				GROUP BY sku_id
				HAVING SUM(CASE direction WHEN 'IN' THEN quantity ELSE -quantity END) <> 0
			)`, id).Scan(&nonZero)
		if err != nil {
			writeJSONError(w, http.StatusInternalServerError, "internal error")
			return
		}
		if nonZero {
			writeJSONError(w, http.StatusConflict, "warehouse_not_empty")
			return
		}
		var hasAssets bool
		if err = tx.QueryRowContext(r.Context(), `SELECT EXISTS(SELECT 1 FROM assets WHERE current_warehouse_id=$1)`, id).Scan(&hasAssets); err != nil {
			writeJSONError(w, http.StatusInternalServerError, "internal error")
			return
		}
		if hasAssets {
			writeJSONError(w, http.StatusConflict, "warehouse_not_empty")
			return
		}
	}
	if _, err = tx.ExecContext(r.Context(), `UPDATE warehouses SET active=$2 WHERE id=$1`, id, active); err != nil {
		writeJSONError(w, http.StatusInternalServerError, "internal error")
		return
	}
	action := "DEACTIVATED"
	if active {
		action = "ACTIVATED"
	}
	u := userFromContext(r.Context())
	if _, err = tx.ExecContext(r.Context(), `INSERT INTO warehouse_audit(warehouse_id,action,user_id) VALUES($1,$2,$3)`, id, action, u.ID); err != nil {
		writeJSONError(w, http.StatusInternalServerError, "internal error")
		return
	}
	if err = tx.Commit(); err != nil {
		writeJSONError(w, http.StatusInternalServerError, "internal error")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"id": id, "name": name, "active": active})
}

func decodeWarehouseAdminJSON(w http.ResponseWriter, r *http.Request, dst any) bool {
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
