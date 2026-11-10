package main

import (
	"database/sql"
	"errors"
	"net/http"
	"strings"
	"time"
)

type routeDeadlineRequest struct {
	Deadline string `json:"deadline"`
	Version  int64  `json:"version"`
}

func (a *app) registerGeofenceHandlers(mux *http.ServeMux) {
	mux.HandleFunc("PUT /api/routes/{id}/departure-deadline", a.requireAuth("dispatch", "write", a.handleRouteDeadline))
	mux.HandleFunc("GET /api/geofencing/overview", a.requireAuth("dispatch", "read", a.handleGeofenceOverview))
}
func (a *app) handleRouteDeadline(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var input routeDeadlineRequest
	if !decodeJSON(w, r, &input) {
		return
	}
	if input.Version < 1 {
		writeJSONError(w, 400, "Atualize a rota antes de definir o prazo.")
		return
	}
	var due any
	if raw := strings.TrimSpace(input.Deadline); raw != "" {
		if !clockPattern.MatchString(raw) {
			writeJSONError(w, 400, "Informe o limite no formato HH:MM.")
			return
		}
		var day string
		err := a.db.QueryRowContext(r.Context(), `SELECT day::text FROM routes WHERE id=$1`, id).Scan(&day)
		if errors.Is(err, sql.ErrNoRows) {
			writeJSONError(w, 404, "Rota não encontrada.")
			return
		}
		if err != nil {
			writeJSONError(w, 500, "Falha ao consultar a data da rota.")
			return
		}
		date, err := time.ParseInLocation("2006-01-02 15:04", day+" "+raw, fortalezaLocation())
		if err != nil {
			writeJSONError(w, 400, "Data do horário-limite inválida.")
			return
		}
		due = date.UTC()
	}
	var version int64
	err := a.db.QueryRowContext(r.Context(), `
UPDATE routes SET departure_deadline=$1,version=version+1
WHERE id=$2 AND status='rascunho' AND version=$3 RETURNING version`, due, id, input.Version).Scan(&version)
	if errors.Is(err, sql.ErrNoRows) {
		writeJSONError(w, 409, "O horário-limite só pode ser alterado em rascunho. Atualize a rota.")
		return
	}
	if err != nil {
		writeJSONError(w, 500, "Não foi possível salvar o horário-limite.")
		return
	}
	writeJSON(w, 200, map[string]any{"id": id, "version": version, "departure_deadline": due})
}

func (a *app) handleGeofenceOverview(w http.ResponseWriter, r *http.Request) {
	day := strings.TrimSpace(r.URL.Query().Get("day"))
	if day == "" {
		day = time.Now().In(fortalezaLocation()).Format("2006-01-02")
	}
	if !isoDatePattern.MatchString(day) {
		writeJSONError(w, 400, "Data inválida.")
		return
	}
	rows, err := a.db.QueryContext(r.Context(), `
SELECT r.id,v.plate,r.status,r.departure_deadline,r.actual_departure_at,r.released_at,
  (SELECT MAX(gp.gps_at) FROM geofence_positions gp WHERE gp.vehicle_id=r.vehicle_id) AS gps_at,
  (SELECT MAX(gp.received_at) FROM geofence_positions gp WHERE gp.vehicle_id=r.vehicle_id) AS received_at
FROM routes r JOIN vehicles v ON v.id=r.vehicle_id
WHERE r.day=$1::date ORDER BY r.id`, day)
	if err != nil {
		writeJSONError(w, 500, "Não foi possível consultar o monitoramento.")
		return
	}
	type overviewRow struct {
		ID                                               int64
		Plate, Status                                    string
		Deadline, Departure, Released, GPSTime, Received sql.NullTime
	}
	items := []overviewRow{}
	for rows.Next() {
		var o overviewRow
		if err = rows.Scan(&o.ID, &o.Plate, &o.Status, &o.Deadline, &o.Departure, &o.Released, &o.GPSTime, &o.Received); err != nil {
			rows.Close()
			writeJSONError(w, 500, "Erro ao consultar monitoramento.")
			return
		}
		items = append(items, o)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		writeJSONError(w, 500, "Falha de leitura do monitoramento.")
		return
	}
	out := []map[string]any{}
	now := time.Now().UTC()
	for _, o := range items {
		status := "sem_prazo"
		if o.Deadline.Valid {
			switch {
			case o.Departure.Valid && !o.Departure.Time.After(o.Deadline.Time):
				status = "no_prazo"
			case o.Departure.Valid:
				status = "saida_atrasada"
			case o.Status == "rascunho":
				status = "em_planejamento"
			case !o.GPSTime.Valid || now.Sub(o.GPSTime.Time) > 5*time.Minute || o.GPSTime.Time.After(now.Add(2*time.Minute)):
				status = "gps_indisponivel"
			case now.After(o.Deadline.Time):
				status = "prazo_vencido"
			default:
				status = "aguardando_saida"
			}
		}
		stops, err := a.db.QueryContext(r.Context(), `SELECT o.id,p.name,to_char(o.due_at AT TIME ZONE 'America/Fortaleza','HH24:MI'),s.arrived_at
FROM route_stops s JOIN delivery_orders o ON o.id=s.order_id JOIN places p ON p.id=o.place_id
WHERE s.route_id=$1 ORDER BY s.sequence`, o.ID)
		if err != nil {
			writeJSONError(w, 500, "Não foi possível consultar as chegadas.")
			return
		}
		arrivals := []map[string]any{}
		for stops.Next() {
			var orderID int64
			var name, expected string
			var at sql.NullTime
			if err = stops.Scan(&orderID, &name, &expected, &at); err != nil {
				break
			}
			arrivals = append(arrivals, map[string]any{"order_id": orderID, "place_name": name, "due_at": expected, "arrived_at": nullTime(at)})
		}
		if err == nil {
			err = stops.Err()
		}
		stops.Close()
		if err != nil {
			writeJSONError(w, 500, "Falha ao carregar as chegadas.")
			return
		}
		out = append(out, map[string]any{"route_id": o.ID, "plate": o.Plate, "route_status": o.Status, "status": status,
			"departure_deadline": nullTime(o.Deadline), "actual_departure_at": nullTime(o.Departure),
			"released_at": nullTime(o.Released), "gps_at": nullTime(o.GPSTime), "gps_received_at": nullTime(o.Received), "stops": arrivals})
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, 200, out)
}
