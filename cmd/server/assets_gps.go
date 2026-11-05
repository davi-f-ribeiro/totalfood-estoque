package main

import (
	"context"
	"database/sql"
	"errors"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// GPS coordinates are optional. When present in an asset operation they are
// written in the SAME database transaction as that operation's audit event.
type assetGPSPoint struct {
	Latitude   float64   `json:"latitude"`
	Longitude  float64   `json:"longitude"`
	AccuracyM  float64   `json:"accuracy_m"`
	CapturedAt time.Time `json:"captured_at"`
}

type assetGPSRequest struct {
	OperationID string        `json:"operation_id"`
	GPS         assetGPSPoint `json:"gps"`
}

func validGPSPoint(p assetGPSPoint) bool {
	now := time.Now()
	return !math.IsNaN(p.Latitude) && !math.IsInf(p.Latitude, 0) &&
		!math.IsNaN(p.Longitude) && !math.IsInf(p.Longitude, 0) &&
		!math.IsNaN(p.AccuracyM) && !math.IsInf(p.AccuracyM, 0) &&
		p.Latitude >= -90 && p.Latitude <= 90 &&
		p.Longitude >= -180 && p.Longitude <= 180 &&
		p.AccuracyM >= 0 && p.AccuracyM <= 100000 &&
		!p.CapturedAt.IsZero() && p.CapturedAt.Before(now.Add(2*time.Minute)) &&
		p.CapturedAt.After(now.Add(-30*24*time.Hour))
}

func insertAssetGPS(ctx context.Context, tx *sql.Tx, operationID, hash string, assetID, userID int64, source string, gps *assetGPSPoint) error {
	if gps == nil {
		return nil // No permission/no GPS: no fabricated location.
	}
	_, err := tx.ExecContext(ctx, `INSERT INTO asset_gps_history
        (operation_id,request_hash,asset_id,user_id,latitude,longitude,accuracy_m,captured_at,source)
        VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)`,
		operationID, hash, assetID, userID, gps.Latitude, gps.Longitude, gps.AccuracyM, gps.CapturedAt, source)
	return err
}

// Only the manual endpoint can create MANUAL observations. CREATE/MOVE/STATUS
// are created by the respective operational endpoints, never by a client-chosen source.
func (a *app) handleAssetGPSCapture(w http.ResponseWriter, r *http.Request) {
	var req assetGPSRequest
	if !decodeJSON(w, r, &req) || strings.TrimSpace(req.OperationID) == "" || !validGPSPoint(req.GPS) {
		writeJSONError(w, http.StatusBadRequest, "Coordenadas inválidas ou captura expirada.")
		return
	}
	tag := cleanBarcode(r.PathValue("tag"))
	if tag == "" {
		writeJSONError(w, http.StatusBadRequest, "Plaqueta inválida.")
		return
	}
	var assetID int64
	if err := a.db.QueryRowContext(r.Context(), "SELECT id FROM assets WHERE tag=$1", tag).Scan(&assetID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeJSONError(w, 404, "Patrimônio não encontrado.")
		} else {
			writeJSONError(w, 500, "Não foi possível consultar o patrimônio.")
		}
		return
	}
	userID := userFromContext(r.Context()).ID
	hash := requestHash(struct {
		AssetID int64
		UserID  int64
		Request assetGPSRequest
	}{assetID, userID, req})
	var id int64
	err := a.db.QueryRowContext(r.Context(), `INSERT INTO asset_gps_history
        (operation_id,request_hash,asset_id,user_id,latitude,longitude,accuracy_m,captured_at,source)
        VALUES ($1,$2,$3,$4,$5,$6,$7,$8,'MANUAL')
        ON CONFLICT (operation_id) DO NOTHING RETURNING id`,
		req.OperationID, hash, assetID, userID, req.GPS.Latitude, req.GPS.Longitude, req.GPS.AccuracyM, req.GPS.CapturedAt).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		var previousHash string
		var previousUser, previousAsset int64
		var previousSource string
		err = a.db.QueryRowContext(r.Context(), `SELECT id,request_hash,user_id,asset_id,source
            FROM asset_gps_history WHERE operation_id=$1`, req.OperationID).
			Scan(&id, &previousHash, &previousUser, &previousAsset, &previousSource)
		if err == nil && previousHash == hash && previousUser == userID &&
			previousAsset == assetID && previousSource == "MANUAL" {
			writeJSON(w, 200, map[string]any{"id": id, "status": "confirmed"})
			return
		}
		if err != nil {
			writeJSONError(w, 500, "Não foi possível conferir a captura.")
		} else {
			writeJSONError(w, 409, "Identificador GPS reutilizado com outros dados.")
		}
		return
	}
	if err != nil {
		writeJSONError(w, 500, "Não foi possível salvar a localização.")
		return
	}
	writeJSON(w, 201, map[string]any{"id": id, "status": "confirmed"})
}

func (a *app) handleAssetGPSHistory(w http.ResponseWriter, r *http.Request) {
	tag := cleanBarcode(r.PathValue("tag"))
	offset := 0
	if value := r.URL.Query().Get("offset"); value != "" {
		n, err := strconv.Atoi(value)
		if err != nil || n < 0 || n > 1000000 {
			writeJSONError(w, 400, "Paginação inválida.")
			return
		}
		offset = n
	}
	rows, err := a.db.QueryContext(r.Context(), `SELECT g.latitude,g.longitude,g.accuracy_m,
        g.captured_at,g.recorded_at,g.source,u.login
        FROM asset_gps_history g
        JOIN assets a ON a.id=g.asset_id
        JOIN users u ON u.id=g.user_id
        WHERE a.tag=$1
        ORDER BY g.captured_at DESC,g.id DESC LIMIT 100 OFFSET $2`, tag, offset)
	if err != nil {
		writeJSONError(w, 500, "Não foi possível consultar o histórico GPS.")
		return
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var latitude, longitude, accuracy float64
		var captured, recorded time.Time
		var source, user string
		if err := rows.Scan(&latitude, &longitude, &accuracy, &captured, &recorded, &source, &user); err != nil {
			writeJSONError(w, 500, "Falha ao ler o histórico GPS.")
			return
		}
		out = append(out, map[string]any{
			"latitude": latitude, "longitude": longitude, "accuracy_m": accuracy,
			"captured_at": captured, "recorded_at": recorded, "source": source, "user": user,
		})
	}
	if rows.Err() != nil {
		writeJSONError(w, 500, "Falha ao ler o histórico GPS.")
		return
	}
	writeJSON(w, 200, out)
}
