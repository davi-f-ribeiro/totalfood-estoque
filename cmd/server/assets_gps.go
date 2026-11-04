package main

import (
 "database/sql"
 "errors"
 "math"
 "net/http"
 "strings"
 "time"
)

type assetGPSRequest struct {
 OperationID string `json:"operation_id"`
 Latitude float64 `json:"latitude"`
 Longitude float64 `json:"longitude"`
 AccuracyM float64 `json:"accuracy_m"`
 CapturedAt time.Time `json:"captured_at"`
 Source string `json:"source"`
}

func validAssetGPS(p assetGPSRequest) bool {
 return strings.TrimSpace(p.OperationID)!="" && !math.IsNaN(p.Latitude) && !math.IsInf(p.Latitude,0) &&
 !math.IsNaN(p.Longitude) && !math.IsInf(p.Longitude,0) &&
 !math.IsNaN(p.AccuracyM) && !math.IsInf(p.AccuracyM,0) &&
 p.Latitude>=-90 && p.Latitude<=90 && p.Longitude>=-180 && p.Longitude<=180 &&
 p.AccuracyM>=0 && p.AccuracyM<=100000 && !p.CapturedAt.IsZero() &&
 p.CapturedAt.Before(time.Now().Add(2*time.Minute)) && p.CapturedAt.After(time.Now().Add(-24*time.Hour)) &&
 (p.Source=="CREATE" || p.Source=="MOVE" || p.Source=="STATUS" || p.Source=="MANUAL")
}

func (a *app) handleAssetGPSCapture(w http.ResponseWriter,r *http.Request) {
 var p assetGPSRequest
 if !decodeJSON(w,r,&p) || !validAssetGPS(p) {
  writeJSONError(w,http.StatusBadRequest,"Coordenadas inválidas ou captura expirada.");return
 }
 tag:=cleanBarcode(r.PathValue("tag"))
 if tag=="" {writeJSONError(w,http.StatusBadRequest,"Plaqueta inválida.");return}
 var assetID int64
 err:=a.db.QueryRowContext(r.Context(),"SELECT id FROM assets WHERE tag=$1",tag).Scan(&assetID)
 if errors.Is(err,sql.ErrNoRows) {writeJSONError(w,http.StatusNotFound,"Patrimônio não encontrado.");return}
 if err!=nil {writeJSONError(w,http.StatusInternalServerError,"Falha ao localizar patrimônio.");return}
 userID:=userFromContext(r.Context()).ID
 // A retry with the same operation ID must be identical, including the operator.
 var existingID,existingAsset,existingUser int64
 var lat,lng,accuracy float64
 var captured time.Time
 var source string
 err=a.db.QueryRowContext(r.Context(),`SELECT id,asset_id,user_id,latitude,longitude,accuracy_m,captured_at,source FROM asset_gps_history WHERE operation_id=$1`,p.OperationID).Scan(&existingID,&existingAsset,&existingUser,&lat,&lng,&accuracy,&captured,&source)
 if err==nil {
  if existingAsset!=assetID || existingUser!=userID || lat!=p.Latitude || lng!=p.Longitude || accuracy!=p.AccuracyM || !captured.Equal(p.CapturedAt) || source!=p.Source {
   writeJSONError(w,http.StatusConflict,"Identificador de captura já utilizado.");return
  }
  writeJSON(w,http.StatusOK,map[string]any{"id":existingID,"status":"confirmed"});return
 }
 if !errors.Is(err,sql.ErrNoRows) {writeJSONError(w,http.StatusInternalServerError,"Falha ao consultar captura.");return}
 var id int64
 err=a.db.QueryRowContext(r.Context(),`INSERT INTO asset_gps_history(operation_id,asset_id,user_id,latitude,longitude,accuracy_m,captured_at,source)
 VALUES ($1,$2,$3,$4,$5,$6,$7,$8) ON CONFLICT (operation_id) DO NOTHING RETURNING id`,p.OperationID,assetID,userID,p.Latitude,p.Longitude,p.AccuracyM,p.CapturedAt,p.Source).Scan(&id)
 if errors.Is(err,sql.ErrNoRows) {writeJSONError(w,http.StatusConflict,"Captura simultânea; consulte o histórico.");return}
 if err!=nil {writeJSONError(w,http.StatusInternalServerError,"Não foi possível registrar a localização.");return}
 writeJSON(w,http.StatusCreated,map[string]any{"id":id,"status":"confirmed"})
}

func (a *app) handleAssetGPSHistory(w http.ResponseWriter,r *http.Request) {
 tag:=cleanBarcode(r.PathValue("tag"))
 rows,err:=a.db.QueryContext(r.Context(),`SELECT g.latitude,g.longitude,g.accuracy_m,g.captured_at,g.recorded_at,g.source,u.login
 FROM asset_gps_history g JOIN assets a ON a.id=g.asset_id JOIN users u ON u.id=g.user_id
 WHERE a.tag=$1 ORDER BY g.recorded_at DESC,g.id DESC LIMIT 100`,tag)
 if err!=nil {writeJSONError(w,http.StatusInternalServerError,"Não foi possível consultar o histórico.");return}
 defer rows.Close()
 out:=[]map[string]any{}
 for rows.Next() {
  var lat,lng,acc float64
  var captured,recorded time.Time
  var source,user string
  if err:=rows.Scan(&lat,&lng,&acc,&captured,&recorded,&source,&user);err!=nil {writeJSONError(w,500,"Falha ao ler histórico.");return}
  out=append(out,map[string]any{"latitude":lat,"longitude":lng,"accuracy_m":acc,"captured_at":captured,"recorded_at":recorded,"source":source,"user":user})
 }
 if rows.Err()!=nil {writeJSONError(w,500,"Falha ao ler histórico.");return}
 writeJSON(w,http.StatusOK,out)
}
