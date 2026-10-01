package main

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"strings"
)

var errAssetWarehouseNotFound = errors.New("asset warehouse not found")

type assetTypeRequest struct {
	Name   string `json:"name"`
	Active *bool  `json:"active"`
}

type assetRequest struct {
	AssetTypeID int64  `json:"asset_type_id"`
	Tag         string `json:"tag"`
	Status      string `json:"status"`
	WarehouseID int64  `json:"warehouse_id"`
}

type assetUpdateRequest struct {
	AssetTypeID int64  `json:"asset_type_id"`
	Tag         string `json:"tag"`
	Version     int64  `json:"version"`
}

type assetMoveRequest struct {
	OperationID string `json:"operation_id"`
	WarehouseID int64  `json:"warehouse_id"`
}

type assetStatusRequest struct {
	OperationID string `json:"operation_id"`
	Status      string `json:"status"`
}

func (a *app) registerAssetHandlers(mux *http.ServeMux) {
	mux.HandleFunc("GET /assets", a.uiPage("assets", "Patrimônio", "assets", "read"))
	mux.HandleFunc("GET /api/assets/types", a.requireAuth("assets", "read", a.handleAssetTypes))
	mux.HandleFunc("POST /api/assets/types", a.requireAuth("assets", "write", a.handleCreateAssetType))
	mux.HandleFunc("PATCH /api/assets/types/{id}", a.requireAuth("assets", "write", a.handleUpdateAssetType))
	mux.HandleFunc("GET /api/assets", a.requireAuth("assets", "read", a.handleAssets))
	mux.HandleFunc("POST /api/assets", a.requireAuth("assets", "write", a.handleCreateAsset))
	mux.HandleFunc("PATCH /api/assets/{id}", a.requireAuth("assets", "write", a.handleUpdateAsset))
	mux.HandleFunc("GET /api/assets/lookup", a.requireAuth("assets", "read", a.handleAssetLookup))
	mux.HandleFunc("POST /api/assets/{tag}/move", a.requireAuth("assets", "write", a.handleMoveAsset))
	mux.HandleFunc("POST /api/assets/{tag}/status", a.requireAuth("assets", "write", a.handleAssetStatus))
}

func (a *app) handleAssetTypes(w http.ResponseWriter, r *http.Request) {
	rows, err := a.db.QueryContext(r.Context(), `SELECT id,name,active FROM asset_types WHERE $1='' OR lower(name) LIKE lower($2) ORDER BY name LIMIT 100`, strings.TrimSpace(r.URL.Query().Get("q")), "%"+strings.TrimSpace(r.URL.Query().Get("q"))+"%")
	if err != nil {
		writeJSONError(w, 500, "internal error")
		return
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var id int64
		var name string
		var active bool
		if err := rows.Scan(&id, &name, &active); err != nil {
			writeJSONError(w, 500, "internal error")
			return
		}
		out = append(out, map[string]any{"id": id, "name": name, "active": active})
	}
	writeJSON(w, 200, out)
}

func (a *app) handleCreateAssetType(w http.ResponseWriter, r *http.Request) {
	var req assetTypeRequest
	if !decodeJSON(w, r, &req) || strings.TrimSpace(req.Name) == "" {
		writeJSONError(w, 400, "invalid request")
		return
	}
	active := true
	if req.Active != nil {
		active = *req.Active
	}
	var id int64
	if err := a.db.QueryRowContext(r.Context(), `INSERT INTO asset_types (name,active) VALUES ($1,$2) RETURNING id`, strings.TrimSpace(req.Name), active).Scan(&id); err != nil {
		log.Printf("asset type create failed error=%v", err)
		writeJSONError(w, 409, "tipo patrimonial não salvo")
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"id": id})
}

func (a *app) handleUpdateAssetType(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var req assetTypeRequest
	if !decodeJSON(w, r, &req) || strings.TrimSpace(req.Name) == "" {
		writeJSONError(w, 400, "invalid request")
		return
	}
	active := true
	if req.Active != nil {
		active = *req.Active
	}
	res, err := a.db.ExecContext(r.Context(), `UPDATE asset_types SET name=$1,active=$2 WHERE id=$3`, strings.TrimSpace(req.Name), active, id)
	if err != nil {
		log.Printf("asset type update failed id=%d error=%v", id, err)
		writeJSONError(w, 409, "tipo patrimonial não salvo")
		return
	}
	if n, _ := res.RowsAffected(); n == 0 {
		writeJSONError(w, 404, "tipo patrimonial não encontrado")
		return
	}
	writeJSON(w, 200, map[string]any{"id": id})
}

func (a *app) handleAssets(w http.ResponseWriter, r *http.Request) {
	rows, err := a.db.QueryContext(r.Context(), `SELECT a.id,a.tag,a.status,COALESCE(a.current_warehouse_id,0),COALESCE(w.name,''),t.id,t.name,a.version FROM assets a JOIN asset_types t ON t.id=a.asset_type_id LEFT JOIN warehouses w ON w.id=a.current_warehouse_id WHERE $1='' OR a.tag=$1 OR lower(t.name) LIKE lower($2) ORDER BY a.tag LIMIT 100`, cleanBarcode(r.URL.Query().Get("q")), "%"+cleanBarcode(r.URL.Query().Get("q"))+"%")
	if err != nil {
		writeJSONError(w, 500, "internal error")
		return
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var id, whID, typeID, version int64
		var tag, status, whName, typeName string
		if err := rows.Scan(&id, &tag, &status, &whID, &whName, &typeID, &typeName, &version); err != nil {
			writeJSONError(w, 500, "internal error")
			return
		}
		out = append(out, map[string]any{"id": id, "tag": tag, "status": status, "warehouse_id": whID, "warehouse": whName, "asset_type_id": typeID, "type": typeName, "version": version})
	}
	writeJSON(w, 200, out)
}

func (a *app) handleAssetLookup(w http.ResponseWriter, r *http.Request) {
	q := cleanBarcode(r.URL.Query().Get("tag"))
	if q == "" {
		writeJSON(w, 200, map[string]any{})
		return
	}
	rows, err := a.db.QueryContext(r.Context(), `SELECT a.id,a.tag,a.status,COALESCE(a.current_warehouse_id,0),COALESCE(w.name,''),t.id,t.name,a.version FROM assets a JOIN asset_types t ON t.id=a.asset_type_id LEFT JOIN warehouses w ON w.id=a.current_warehouse_id WHERE a.tag=$1 LIMIT 1`, q)
	if err != nil {
		writeJSONError(w, 500, "internal error")
		return
	}
	defer rows.Close()
	if !rows.Next() {
		writeJSONError(w, 404, "patrimônio não encontrado")
		return
	}
	var id, whID, typeID, version int64
	var tag, status, whName, typeName string
	if err := rows.Scan(&id, &tag, &status, &whID, &whName, &typeID, &typeName, &version); err != nil {
		writeJSONError(w, 500, "internal error")
		return
	}
	writeJSON(w, 200, map[string]any{"id": id, "tag": tag, "status": status, "warehouse_id": whID, "warehouse": whName, "asset_type_id": typeID, "type": typeName, "version": version})
}

func (a *app) handleCreateAsset(w http.ResponseWriter, r *http.Request) {
	var req assetRequest
	if !decodeJSON(w, r, &req) || !validAssetReq(req) {
		writeJSONError(w, 400, "invalid request")
		return
	}
	var wh any = req.WarehouseID
	if req.Status == "BAIXADO" {
		wh = nil
	} else {
		active, err := a.activeWarehouseExists(r.Context(), req.WarehouseID)
		if err != nil {
			writeJSONError(w, http.StatusInternalServerError, "internal error")
			return
		}
		if !active {
			writeJSONError(w, http.StatusConflict, "depósito inativo ou inexistente")
			return
		}
	}
	var id int64
	if err := a.db.QueryRowContext(r.Context(), `INSERT INTO assets (asset_type_id,tag,status,current_warehouse_id) VALUES ($1,$2,$3,$4) RETURNING id`, req.AssetTypeID, cleanBarcode(req.Tag), req.Status, wh).Scan(&id); err != nil {
		writeAssetError(w, err, "asset create failed")
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"id": id, "version": int64(1)})
}

func (a *app) handleUpdateAsset(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var req assetUpdateRequest
	if !decodeJSON(w, r, &req) || req.AssetTypeID <= 0 || cleanBarcode(req.Tag) == "" || req.Version <= 0 {
		writeJSONError(w, http.StatusBadRequest, "invalid request")
		return
	}
	var currentVersion int64
	if err := a.db.QueryRowContext(r.Context(), `SELECT version FROM assets WHERE id=$1`, id).Scan(&currentVersion); errors.Is(err, sql.ErrNoRows) {
		writeJSONError(w, http.StatusNotFound, "patrimônio não encontrado")
		return
	} else if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "internal error")
		return
	}
	if currentVersion != req.Version {
		writeConcurrentEditError(w)
		return
	}
	var newVersion int64
	err := a.db.QueryRowContext(r.Context(), `
UPDATE assets
SET asset_type_id=$1,tag=$2,version=version+1
WHERE id=$3 AND version=$4
RETURNING version`, req.AssetTypeID, cleanBarcode(req.Tag), id, req.Version).Scan(&newVersion)
	if errors.Is(err, sql.ErrNoRows) {
		writeConcurrentEditError(w)
		return
	}
	if err != nil {
		writeAssetError(w, err, "asset update failed")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"id": id, "version": newVersion})
}

func validAssetReq(req assetRequest) bool {
	return req.AssetTypeID > 0 && cleanBarcode(req.Tag) != "" && validAssetStatus(req.Status) && (req.Status != "ATIVO" || req.WarehouseID > 0)
}
func validAssetStatus(s string) bool { return s == "ATIVO" || s == "EM_MANUTENCAO" || s == "BAIXADO" }
func writeAssetError(w http.ResponseWriter, err error, prefix string) {
	log.Printf("%s error=%v", prefix, err)
	if strings.Contains(err.Error(), "assets_tag") {
		writeJSONError(w, 409, "Esta plaqueta já está cadastrada.")
		return
	}
	if strings.Contains(err.Error(), "asset warehouse inactive or not found") {
		writeJSONError(w, 409, "depósito inativo ou inexistente")
		return
	}
	writeJSONError(w, 400, "patrimônio não salvo")
}

func (a *app) handleMoveAsset(w http.ResponseWriter, r *http.Request) {
	var req assetMoveRequest
	if !decodeJSON(w, r, &req) || strings.TrimSpace(req.OperationID) == "" || req.WarehouseID <= 0 {
		writeJSONError(w, 400, "invalid request")
		return
	}
	if err := a.changeAssetLocation(r.Context(), cleanBarcode(r.PathValue("tag")), req, userFromContext(r.Context()).ID, requestHash(req)); err != nil {
		writeAssetMutationError(w, err)
		return
	}
	writeJSON(w, 200, map[string]string{"status": "confirmed"})
}

func (a *app) handleAssetStatus(w http.ResponseWriter, r *http.Request) {
	var req assetStatusRequest
	if !decodeJSON(w, r, &req) || strings.TrimSpace(req.OperationID) == "" || !validAssetStatus(req.Status) {
		writeJSONError(w, 400, "invalid request")
		return
	}
	if err := a.changeAssetStatus(r.Context(), cleanBarcode(r.PathValue("tag")), req, userFromContext(r.Context()).ID, requestHash(req)); err != nil {
		writeAssetMutationError(w, err)
		return
	}
	writeJSON(w, 200, map[string]string{"status": "confirmed"})
}

func writeAssetMutationError(w http.ResponseWriter, err error) {
	if errors.Is(err, errOperationConflict) {
		writeJSONError(w, 409, "operation_id conflict")
		return
	}
	if errors.Is(err, errAssetWarehouseNotFound) {
		writeJSONError(w, 404, "depósito não encontrado")
		return
	}
	if errors.Is(err, errWarehouseInactive) {
		writeJSONError(w, 409, "depósito inativo")
		return
	}
	if errors.Is(err, errNotFound) {
		writeJSONError(w, 404, "patrimônio não encontrado")
		return
	}
	if errors.Is(err, errValidation) {
		writeJSONError(w, 400, "operação inválida")
		return
	}
	if strings.Contains(err.Error(), "asset warehouse inactive or not found") {
		writeJSONError(w, 409, "depósito inativo ou inexistente")
		return
	}
	log.Printf("asset mutation failed error=%v", err)
	writeJSONError(w, 500, "internal error")
}

func (a *app) changeAssetLocation(ctx context.Context, tag string, req assetMoveRequest, userID int64, hash string) error {
	return a.writeAssetEvent(ctx, req.OperationID, hash, func(ctx context.Context, tx *sql.Tx) error {
		var assetID int64
		var status string
		var origin sql.NullInt64
		if err := tx.QueryRowContext(ctx, `SELECT id,status,current_warehouse_id FROM assets WHERE tag=$1 FOR UPDATE`, tag).Scan(&assetID, &status, &origin); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return errNotFound
			}
			return err
		}
		if status != "ATIVO" {
			return errValidation
		}
		var warehouseActive bool
		err := tx.QueryRowContext(ctx, `SELECT active FROM warehouses WHERE id=$1 FOR UPDATE`, req.WarehouseID).Scan(&warehouseActive)
		if errors.Is(err, sql.ErrNoRows) {
			return errAssetWarehouseNotFound
		}
		if err != nil {
			return err
		}
		if !warehouseActive {
			return errWarehouseInactive
		}
		if origin.Valid && origin.Int64 == req.WarehouseID {
			return errValidation
		}
		if _, err := tx.ExecContext(ctx, `UPDATE assets SET current_warehouse_id=$1,version=version+1 WHERE id=$2`, req.WarehouseID, assetID); err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO asset_events (operation_id,request_hash,asset_id,event_type,origin_warehouse_id,destination_warehouse_id,old_status,new_status,user_id) VALUES ($1,$2,$3,'MOVE',$4,$5,$6,$6,$7)`, req.OperationID, hash, assetID, nullInt(origin), req.WarehouseID, status, userID)
		return err
	})
}

func (a *app) changeAssetStatus(ctx context.Context, tag string, req assetStatusRequest, userID int64, hash string) error {
	return a.writeAssetEvent(ctx, req.OperationID, hash, func(ctx context.Context, tx *sql.Tx) error {
		var assetID int64
		var old string
		var wh sql.NullInt64
		if err := tx.QueryRowContext(ctx, `SELECT id,status,current_warehouse_id FROM assets WHERE tag=$1 FOR UPDATE`, tag).Scan(&assetID, &old, &wh); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return errNotFound
			}
			return err
		}
		if old == req.Status {
			return errValidation
		}
		if !allowedAssetTransition(old, req.Status) {
			return errValidation
		}
		var newWh any = nullInt(wh)
		if req.Status == "BAIXADO" {
			newWh = nil
		}
		if _, err := tx.ExecContext(ctx, `UPDATE assets SET status=$1,current_warehouse_id=$2,version=version+1 WHERE id=$3`, req.Status, newWh, assetID); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `INSERT INTO asset_events (operation_id,request_hash,asset_id,event_type,origin_warehouse_id,destination_warehouse_id,old_status,new_status,user_id) VALUES ($1,$2,$3,'STATUS',$4,$4,$5,$6,$7)`, req.OperationID, hash, assetID, nullInt(wh), old, req.Status, userID)
		return err
	})
}

func allowedAssetTransition(old, next string) bool {
	return (old == "ATIVO" && next == "EM_MANUTENCAO") || (old == "EM_MANUTENCAO" && next == "ATIVO") || (old == "ATIVO" && next == "BAIXADO")
}
func nullInt(v sql.NullInt64) any {
	if v.Valid {
		return v.Int64
	}
	return nil
}
func requestHash(v any) string {
	b, _ := json.Marshal(v)
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

func (a *app) writeAssetEvent(ctx context.Context, op, hash string, fn func(context.Context, *sql.Tx) error) error {
	tx, err := a.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var existingHash string
	err = tx.QueryRowContext(ctx, `SELECT request_hash FROM asset_events WHERE operation_id=$1`, op).Scan(&existingHash)
	if err == nil {
		if existingHash == hash {
			return tx.Commit()
		}
		return errOperationConflict
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if err := fn(ctx, tx); err != nil {
		return err
	}
	return tx.Commit()
}


func (a *app) activeWarehouseExists(ctx context.Context, warehouseID int64) (bool, error) {
	if warehouseID <= 0 {
		return false, nil
	}
	var ok bool
	err := a.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM warehouses WHERE id=$1 AND active=true)`, warehouseID).Scan(&ok)
	return ok, err
}
