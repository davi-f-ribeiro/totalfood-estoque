package main

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"slices"
	"strings"
	"time"
)

// Tela do motorista (fase 3). O motorista só enxerga a rota ligada ao próprio login e
// só mexe nas entregas dela. As rotas da API reaproveitam os mesmos handlers do
// despacho, com uma checagem de dono na frente.

var driverEventStatuses = []string{"em_rota", "chegou", "falhou", "entregue"}
var driverRouteStatuses = []string{"em_andamento", "concluido"}

func (a *app) registerDriverHandlers(mux *http.ServeMux) {
	mux.HandleFunc("GET /driver", a.uiPage("driver", "Minha rota", "delivery", "read"))
	mux.HandleFunc("GET /driver-sw.js", a.handleDriverServiceWorker)
	mux.HandleFunc("GET /api/driver/route", a.requireAuth("delivery", "read", a.handleDriverRoute))
	mux.HandleFunc("POST /api/driver/routes/{id}/status", a.requireAuth("delivery", "write", a.driverOwnsRoute(a.handleRouteStatus)))
	mux.HandleFunc("POST /api/driver/orders/{id}/events", a.requireAuth("delivery", "write", a.driverOwnsOrder(true, a.handleDeliveryEvent)))
	mux.HandleFunc("POST /api/driver/orders/{id}/deliver", a.requireAuth("delivery", "write", a.driverOwnsOrder(false, a.handleDeliverWithProof)))
}

// peekStatus lê o campo "status" do corpo JSON e devolve o corpo intacto para o handler.
func peekStatus(w http.ResponseWriter, r *http.Request) (string, bool) {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 64<<10))
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, "Dados inválidos. Confira os campos e tente de novo.")
		return "", false
	}
	r.Body = io.NopCloser(bytes.NewReader(body))
	var probe struct {
		Status string `json:"status"`
	}
	_ = json.Unmarshal(body, &probe)
	return strings.TrimSpace(probe.Status), true
}

func (a *app) driverOwnsOrder(checkStatus bool, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, ok := pathID(w, r)
		if !ok {
			return
		}
		if checkStatus {
			status, ok := peekStatus(w, r)
			if !ok {
				return
			}
			if !slices.Contains(driverEventStatuses, status) {
				writeJSONError(w, http.StatusForbidden, "Pelo celular dá para registrar saída para a parada, chegada, entrega ou falha.")
				return
			}
		}
		var one int
		err := a.db.QueryRowContext(r.Context(), `
SELECT 1 FROM route_stops s
JOIN routes r ON r.id=s.route_id
JOIN drivers d ON d.id=r.driver_id
WHERE s.order_id=$1 AND r.status='em_andamento' AND d.user_id=$2`, id, userFromContext(r.Context()).ID).Scan(&one)
		if errors.Is(err, sql.ErrNoRows) {
			writeJSONError(w, http.StatusForbidden, "Esta entrega não está na sua rota em andamento.")
			return
		}
		if err != nil {
			logisticsFail(w, http.StatusInternalServerError, "Não foi possível conferir a sua rota.", "driver order check failed", err)
			return
		}
		next(w, r)
	}
}

func (a *app) driverOwnsRoute(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, ok := pathID(w, r)
		if !ok {
			return
		}
		status, ok := peekStatus(w, r)
		if !ok {
			return
		}
		if !slices.Contains(driverRouteStatuses, status) {
			writeJSONError(w, http.StatusForbidden, "Pelo celular dá para iniciar ou concluir a rota.")
			return
		}
		var one int
		err := a.db.QueryRowContext(r.Context(), `
SELECT 1 FROM routes r JOIN drivers d ON d.id=r.driver_id WHERE r.id=$1 AND d.user_id=$2`, id, userFromContext(r.Context()).ID).Scan(&one)
		if errors.Is(err, sql.ErrNoRows) {
			writeJSONError(w, http.StatusForbidden, "Esta rota não é sua.")
			return
		}
		if err != nil {
			logisticsFail(w, http.StatusInternalServerError, "Não foi possível conferir a sua rota.", "driver route check failed", err)
			return
		}
		next(w, r)
	}
}

// handleDriverRoute devolve a rota liberada ou em andamento do motorista no dia.
func (a *app) handleDriverRoute(w http.ResponseWriter, r *http.Request) {
	day := strings.TrimSpace(r.URL.Query().Get("day"))
	if day == "" {
		day = time.Now().In(fortalezaLocation()).Format("2006-01-02")
	}
	if !isoDatePattern.MatchString(day) {
		writeJSONError(w, http.StatusBadRequest, "Data inválida. Use o formato ano-mês-dia.")
		return
	}
	ctx := r.Context()
	var driverID int64
	var driverName string
	err := a.db.QueryRowContext(ctx, `SELECT id,name FROM drivers WHERE user_id=$1 AND active`, userFromContext(ctx).ID).Scan(&driverID, &driverName)
	if errors.Is(err, sql.ErrNoRows) {
		writeJSONError(w, http.StatusNotFound, "Seu login não está ligado a um motorista ativo. Peça para ligar em Locais e frota.")
		return
	}
	if err != nil {
		logisticsFail(w, http.StatusInternalServerError, "Não foi possível carregar a sua rota.", "driver lookup failed", err)
		return
	}
	var routeID, version int64
	var status, plate, description string
	err = a.db.QueryRowContext(ctx, `
SELECT r.id,r.status,v.plate,v.description,r.version
FROM routes r JOIN vehicles v ON v.id=r.vehicle_id
WHERE r.driver_id=$1 AND r.day=$2::date AND r.status IN ('liberado','em_andamento')`, driverID, day).Scan(&routeID, &status, &plate, &description, &version)
	if errors.Is(err, sql.ErrNoRows) {
		writeJSON(w, http.StatusOK, map[string]any{"day": day, "driver_name": driverName, "route": nil})
		return
	}
	if err != nil {
		logisticsFail(w, http.StatusInternalServerError, "Não foi possível carregar a sua rota.", "driver route failed", err)
		return
	}
	stops, err := a.routeStops(ctx, routeID)
	if err != nil {
		logisticsFail(w, http.StatusInternalServerError, "Não foi possível carregar a sua rota.", "driver stops failed", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"day": day, "driver_name": driverName, "route": map[string]any{
		"id": routeID, "status": status, "vehicle_plate": plate, "vehicle_description": description, "version": version, "stops": stops,
	}})
}

// O service worker precisa ser servido da raiz para valer para /driver.
func (a *app) handleDriverServiceWorker(w http.ResponseWriter, r *http.Request) {
	js, err := uiFS.ReadFile("static/driver-sw.js")
	if err != nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	_, _ = w.Write(js)
}
