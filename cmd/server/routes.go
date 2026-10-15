package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"math"
	"net/http"
	"slices"
	"strings"
	"time"
)

// Rota (fase 3): um veículo e um motorista num dia, com as entregas em sequência.
// O detalhe da rota é o romaneio. Liberar, iniciar e concluir a rota move os pedidos
// junto, gravando um evento por pedido como se fosse o despachante.

var routeOpenStatuses = []string{"rascunho", "liberado", "em_andamento"}

var routeNext = map[string][]string{
	"rascunho":     {"liberado", "cancelado"},
	"liberado":     {"em_andamento", "cancelado"},
	"em_andamento": {"concluido"},
	"concluido":    {},
	"cancelado":    {},
}

// Pedidos que podem entrar numa rota: novo, já programado (troca de rota) ou que falhou.
var routableOrderStatuses = []string{"criado", "programado", "falhou"}

type routeRequest struct {
	Day       string `json:"day"`
	VehicleID int64  `json:"vehicle_id"`
	DriverID  int64  `json:"driver_id"`
}

type routeStopsRequest struct {
	OrderIDs []int64 `json:"order_ids"`
	Version  int64   `json:"version"`
}

type routeStatusRequest struct {
	Status  string `json:"status"`
	Version int64  `json:"version"`
}

func (a *app) registerRouteHandlers(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/routes", a.requireAuth("dispatch", "read", a.handleRoutes))
	mux.HandleFunc("POST /api/routes", a.requireAuth("dispatch", "write", a.handleCreateRoute))
	mux.HandleFunc("GET /api/routes/{id}", a.requireAuth("dispatch", "read", a.handleRoute))
	mux.HandleFunc("PUT /api/routes/{id}/stops", a.requireAuth("dispatch", "write", a.handleRouteStops))
	mux.HandleFunc("POST /api/routes/{id}/status", a.requireAuth("dispatch", "write", a.handleRouteStatus))
	mux.HandleFunc("POST /api/routes/{id}/optimize", a.requireAuth("dispatch", "write", a.handleRouteOptimize))
}

func (a *app) handleCreateRoute(w http.ResponseWriter, r *http.Request) {
	var req routeRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	req.Day = strings.TrimSpace(req.Day)
	switch {
	case !isoDatePattern.MatchString(req.Day):
		writeJSONError(w, http.StatusBadRequest, "Data da rota inválida.")
		return
	case req.VehicleID <= 0:
		writeJSONError(w, http.StatusBadRequest, "Escolha o veículo da rota.")
		return
	case req.DriverID <= 0:
		writeJSONError(w, http.StatusBadRequest, "Escolha o motorista da rota.")
		return
	}
	ctx := r.Context()
	var vehicleActive bool
	err := a.db.QueryRowContext(ctx, `SELECT active FROM vehicles WHERE id=$1`, req.VehicleID).Scan(&vehicleActive)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && !vehicleActive) {
		writeJSONError(w, http.StatusBadRequest, "Veículo não encontrado ou inativo. Atualize a página e escolha de novo.")
		return
	}
	if err != nil {
		logisticsFail(w, http.StatusInternalServerError, "Não foi possível criar a rota.", "route vehicle lookup failed", err)
		return
	}
	var driverActive, licenseExpired bool
	err = a.db.QueryRowContext(ctx, `SELECT active, coalesce(license_expires_at < $2::date, false) FROM drivers WHERE id=$1`,
		req.DriverID, req.Day).Scan(&driverActive, &licenseExpired)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && !driverActive) {
		writeJSONError(w, http.StatusBadRequest, "Motorista não encontrado ou inativo. Atualize a página e escolha de novo.")
		return
	}
	if err != nil {
		logisticsFail(w, http.StatusInternalServerError, "Não foi possível criar a rota.", "route driver lookup failed", err)
		return
	}
	if licenseExpired {
		writeJSONError(w, http.StatusBadRequest, "A CNH deste motorista estará vencida no dia da rota.")
		return
	}
	var id int64
	err = a.db.QueryRowContext(ctx, `INSERT INTO routes (day,vehicle_id,driver_id) VALUES ($1::date,$2,$3) RETURNING id`,
		req.Day, req.VehicleID, req.DriverID).Scan(&id)
	if err != nil {
		msg := err.Error()
		switch {
		case strings.Contains(msg, "idx_routes_open_vehicle_day"):
			writeJSONError(w, http.StatusConflict, "Este veículo já tem uma rota aberta neste dia.")
		case strings.Contains(msg, "idx_routes_open_driver_day"):
			writeJSONError(w, http.StatusConflict, "Este motorista já tem uma rota aberta neste dia.")
		default:
			logisticsFail(w, http.StatusInternalServerError, "Não foi possível criar a rota.", "route create failed", err)
		}
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"id": id, "status": "rascunho", "version": int64(1)})
}

func (a *app) handleRoutes(w http.ResponseWriter, r *http.Request) {
	day := strings.TrimSpace(r.URL.Query().Get("day"))
	if day == "" {
		day = time.Now().Format("2006-01-02")
	}
	if !isoDatePattern.MatchString(day) {
		writeJSONError(w, http.StatusBadRequest, "Data inválida. Use o formato ano-mês-dia.")
		return
	}
	rows, err := a.db.QueryContext(r.Context(), `
SELECT r.id,r.day::text,r.status,v.plate,v.description,v.ownership,d.name,
       (SELECT count(*) FROM route_stops s WHERE s.route_id=r.id),r.total_distance_m,r.total_duration_s,r.version
FROM routes r
JOIN vehicles v ON v.id=r.vehicle_id
JOIN drivers d ON d.id=r.driver_id
WHERE r.day=$1::date
ORDER BY r.id`, day)
	if err != nil {
		logisticsFail(w, http.StatusInternalServerError, "Não foi possível carregar as rotas.", "route list failed", err)
		return
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var id, stops, version int64
		var dayText, status, plate, description, ownership, driver string
		var distance, duration sql.NullInt64
		if err := rows.Scan(&id, &dayText, &status, &plate, &description, &ownership, &driver, &stops, &distance, &duration, &version); err != nil {
			logisticsFail(w, http.StatusInternalServerError, "Não foi possível carregar as rotas.", "route scan failed", err)
			return
		}
		out = append(out, map[string]any{
			"id": id, "day": dayText, "status": status, "vehicle_plate": plate, "vehicle_description": description,
			"vehicle_ownership": ownership, "driver_name": driver, "stops": stops,
			"total_distance_m": nullInt(distance), "total_duration_s": nullInt(duration), "version": version,
		})
	}
	if err := rows.Err(); err != nil {
		logisticsFail(w, http.StatusInternalServerError, "Não foi possível carregar as rotas.", "route rows failed", err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// handleRoute devolve o romaneio: cabeçalho da rota e as paradas na ordem, com o que
// entregar em cada uma.
func (a *app) handleRoute(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	ctx := r.Context()
	var dayText, status, plate, description, ownership, driver string
	var driverPhone sql.NullString
	var capacityKg string
	var refrigerated bool
	var distance, duration sql.NullInt64
	var version int64
	err := a.db.QueryRowContext(ctx, `
SELECT r.day::text,r.status,v.plate,v.description,v.ownership,v.capacity_kg::text,v.refrigerated,d.name,d.phone,
       r.total_distance_m,r.total_duration_s,r.version
FROM routes r
JOIN vehicles v ON v.id=r.vehicle_id
JOIN drivers d ON d.id=r.driver_id
WHERE r.id=$1`, id).Scan(&dayText, &status, &plate, &description, &ownership, &capacityKg, &refrigerated, &driver, &driverPhone,
		&distance, &duration, &version)
	if errors.Is(err, sql.ErrNoRows) {
		writeJSONError(w, http.StatusNotFound, "Rota não encontrada.")
		return
	}
	if err != nil {
		logisticsFail(w, http.StatusInternalServerError, "Não foi possível carregar a rota.", "route detail failed", err)
		return
	}
	stops, err := a.routeStops(ctx, id)
	if err != nil {
		logisticsFail(w, http.StatusInternalServerError, "Não foi possível carregar a rota.", "route stops failed", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"id": id, "day": dayText, "status": status, "vehicle_plate": plate, "vehicle_description": description,
		"vehicle_ownership": ownership, "vehicle_capacity_kg": capacityKg, "vehicle_refrigerated": refrigerated,
		"driver_name": driver, "driver_phone": nullText(driverPhone),
		"total_distance_m": nullInt(distance), "total_duration_s": nullInt(duration), "version": version,
		"stops": stops, "next_statuses": routeNext[status],
	})
}

func (a *app) routeStops(ctx context.Context, routeID int64) ([]map[string]any, error) {
	rows, err := a.db.QueryContext(ctx, `
SELECT s.sequence,o.id,o.status,o.meal,to_char(o.due_at AT TIME ZONE 'America/Fortaleza','HH24:MI'),o.pod_required,
       p.name,p.address,p.city,p.latitude::text,p.longitude::text,
       to_char(p.window_start,'HH24:MI'),to_char(p.window_end,'HH24:MI'),p.contact,p.access_notes,
       s.distance_from_prev_m,s.duration_from_prev_s
FROM route_stops s
JOIN delivery_orders o ON o.id=s.order_id
JOIN places p ON p.id=o.place_id
WHERE s.route_id=$1
ORDER BY s.sequence`, routeID)
	if err != nil {
		return nil, err
	}
	out := []map[string]any{}
	var orderIDs []int64
	for rows.Next() {
		var sequence, orderID int64
		var status, dueAt, place, address string
		var pod bool
		var meal, city, lat, lng, ws, we, contact, access sql.NullString
		var distance, duration sql.NullInt64
		if err := rows.Scan(&sequence, &orderID, &status, &meal, &dueAt, &pod, &place, &address, &city, &lat, &lng,
			&ws, &we, &contact, &access, &distance, &duration); err != nil {
			rows.Close()
			return nil, err
		}
		orderIDs = append(orderIDs, orderID)
		out = append(out, map[string]any{
			"sequence": sequence, "order_id": orderID, "status": status, "meal": nullText(meal), "due_at": dueAt,
			"pod_required": pod, "place_name": place, "address": address, "city": nullText(city),
			"latitude": nullText(lat), "longitude": nullText(lng), "window_start": nullText(ws), "window_end": nullText(we),
			"contact": nullText(contact), "access_notes": nullText(access),
			"distance_from_prev_m": nullInt(distance), "duration_from_prev_s": nullInt(duration),
		})
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	for i, orderID := range orderIDs {
		lines, err := a.deliveryLines(ctx, orderID)
		if err != nil {
			return nil, err
		}
		out[i]["lines"] = lines
	}
	return out, nil
}

// lockRoute trava a rota e confere a versão enviada pela tela.
func lockRoute(ctx context.Context, tx *sql.Tx, w http.ResponseWriter, id, version int64) (status, day string, ok bool) {
	var current int64
	err := tx.QueryRowContext(ctx, `SELECT status,day::text,version FROM routes WHERE id=$1 FOR UPDATE`, id).Scan(&status, &day, &current)
	if errors.Is(err, sql.ErrNoRows) {
		writeJSONError(w, http.StatusNotFound, "Rota não encontrada.")
		return "", "", false
	}
	if err != nil {
		logisticsFail(w, http.StatusInternalServerError, "Não foi possível alterar a rota.", "route lock failed", err)
		return "", "", false
	}
	if current != version {
		writeConcurrentEditError(w)
		return "", "", false
	}
	return status, day, true
}

// handleRouteStops troca as paradas da rota pela lista enviada, na ordem enviada.
// Só vale em rascunho. Os pedidos ficam travados até o fim para que dois despachantes
// não ponham a mesma entrega em duas rotas abertas.
func (a *app) handleRouteStops(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var req routeStopsRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	seen := map[int64]bool{}
	for _, orderID := range req.OrderIDs {
		if orderID <= 0 || seen[orderID] {
			writeJSONError(w, http.StatusBadRequest, "Lista de entregas inválida: cada entrega aparece uma vez só.")
			return
		}
		seen[orderID] = true
	}
	ctx := r.Context()
	tx, err := a.db.BeginTx(ctx, nil)
	if err != nil {
		logisticsFail(w, http.StatusInternalServerError, "Não foi possível alterar a rota.", "route stops begin failed", err)
		return
	}
	defer tx.Rollback()
	status, day, ok := lockRoute(ctx, tx, w, id, req.Version)
	if !ok {
		return
	}
	if status != "rascunho" {
		writeJSONError(w, http.StatusConflict, "Só dá para mudar as entregas de uma rota em rascunho.")
		return
	}

	rows, err := tx.QueryContext(ctx, `SELECT id,status,day::text FROM delivery_orders WHERE id=ANY($1::bigint[]) ORDER BY id FOR UPDATE`, req.OrderIDs)
	if err != nil {
		logisticsFail(w, http.StatusInternalServerError, "Não foi possível alterar a rota.", "route stops lock orders failed", err)
		return
	}
	found := 0
	problem := ""
	for rows.Next() {
		var orderID int64
		var orderStatus, orderDay string
		if err := rows.Scan(&orderID, &orderStatus, &orderDay); err != nil {
			rows.Close()
			logisticsFail(w, http.StatusInternalServerError, "Não foi possível alterar a rota.", "route stops scan failed", err)
			return
		}
		found++
		switch {
		case problem != "":
		case orderDay != day:
			problem = fmt.Sprintf("A entrega #%d é de outro dia (%s).", orderID, orderDay)
		case !slices.Contains(routableOrderStatuses, orderStatus):
			problem = fmt.Sprintf("A entrega #%d está %s e não pode entrar na rota.", orderID, orderStatus)
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		logisticsFail(w, http.StatusInternalServerError, "Não foi possível alterar a rota.", "route stops rows failed", err)
		return
	}
	if found != len(req.OrderIDs) {
		writeJSONError(w, http.StatusBadRequest, "Entrega não encontrada. Atualize a página e escolha de novo.")
		return
	}
	if problem != "" {
		writeJSONError(w, http.StatusBadRequest, problem)
		return
	}
	var busyOrder, busyRoute int64
	err = tx.QueryRowContext(ctx, `
SELECT s.order_id,s.route_id FROM route_stops s JOIN routes r ON r.id=s.route_id
WHERE s.order_id=ANY($1::bigint[]) AND r.id<>$2 AND r.status=ANY($3::text[])
ORDER BY s.order_id LIMIT 1`, req.OrderIDs, id, routeOpenStatuses).Scan(&busyOrder, &busyRoute)
	if err == nil {
		writeJSONError(w, http.StatusConflict, fmt.Sprintf("A entrega #%d já está na rota #%d. Tire de lá antes.", busyOrder, busyRoute))
		return
	}
	if !errors.Is(err, sql.ErrNoRows) {
		logisticsFail(w, http.StatusInternalServerError, "Não foi possível alterar a rota.", "route stops busy check failed", err)
		return
	}

	if _, err := tx.ExecContext(ctx, `DELETE FROM route_stops WHERE route_id=$1`, id); err != nil {
		logisticsFail(w, http.StatusInternalServerError, "Não foi possível alterar a rota.", "route stops delete failed", err)
		return
	}
	for i, orderID := range req.OrderIDs {
		if _, err := tx.ExecContext(ctx, `INSERT INTO route_stops (route_id,sequence,order_id) VALUES ($1,$2,$3)`, id, i+1, orderID); err != nil {
			logisticsFail(w, http.StatusInternalServerError, "Não foi possível alterar a rota.", "route stop insert failed", err)
			return
		}
	}
	// Distâncias e tempos calculados antes não valem para a nova ordem.
	var version int64
	if err := tx.QueryRowContext(ctx, `UPDATE routes SET total_distance_m=NULL,total_duration_s=NULL,version=version+1 WHERE id=$1 RETURNING version`, id).Scan(&version); err != nil {
		logisticsFail(w, http.StatusInternalServerError, "Não foi possível alterar a rota.", "route stops version failed", err)
		return
	}
	if err := tx.Commit(); err != nil {
		logisticsFail(w, http.StatusInternalServerError, "Não foi possível alterar a rota.", "route stops commit failed", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"id": id, "stops": len(req.OrderIDs), "version": version})
}

// routeOrderStatus diz para onde vai cada pedido quando a rota muda de status.
// "" significa que o pedido fica como está; ok=false bloqueia a mudança da rota.
func routeOrderStatus(routeTo, orderStatus string) (next string, ok bool) {
	switch routeTo {
	case "liberado":
		switch orderStatus {
		case "criado", "falhou":
			return "programado", true
		case "programado", "cancelado":
			return "", true
		}
	case "em_andamento":
		switch orderStatus {
		case "programado":
			return "despachado", true
		case "cancelado":
			return "", true
		}
	case "concluido":
		switch orderStatus {
		case "entregue", "falhou", "cancelado":
			return "", true
		}
	case "cancelado":
		return "", true
	}
	return "", false
}

func (a *app) handleRouteStatus(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var req routeStatusRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	req.Status = strings.TrimSpace(req.Status)
	if _, known := routeNext[req.Status]; !known {
		writeJSONError(w, http.StatusBadRequest, "Status de rota inválido.")
		return
	}
	ctx := r.Context()
	tx, err := a.db.BeginTx(ctx, nil)
	if err != nil {
		logisticsFail(w, http.StatusInternalServerError, "Não foi possível alterar a rota.", "route status begin failed", err)
		return
	}
	defer tx.Rollback()
	current, _, ok := lockRoute(ctx, tx, w, id, req.Version)
	if !ok {
		return
	}
	if !slices.Contains(routeNext[current], req.Status) {
		writeJSONError(w, http.StatusConflict, "Não dá para mudar a rota de "+current+" para "+req.Status+". Atualize a página e confira o status atual.")
		return
	}

	type stopOrder struct {
		id     int64
		status string
	}
	rows, err := tx.QueryContext(ctx, `
SELECT o.id,o.status FROM route_stops s JOIN delivery_orders o ON o.id=s.order_id
WHERE s.route_id=$1 ORDER BY o.id FOR UPDATE OF o`, id)
	if err != nil {
		logisticsFail(w, http.StatusInternalServerError, "Não foi possível alterar a rota.", "route status orders failed", err)
		return
	}
	var orders []stopOrder
	for rows.Next() {
		var o stopOrder
		if err := rows.Scan(&o.id, &o.status); err != nil {
			rows.Close()
			logisticsFail(w, http.StatusInternalServerError, "Não foi possível alterar a rota.", "route status scan failed", err)
			return
		}
		orders = append(orders, o)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		logisticsFail(w, http.StatusInternalServerError, "Não foi possível alterar a rota.", "route status rows failed", err)
		return
	}
	if req.Status == "liberado" && len(orders) == 0 {
		writeJSONError(w, http.StatusBadRequest, "Inclua ao menos uma entrega na rota antes de liberar.")
		return
	}
	open := 0
	for _, o := range orders {
		if _, ok := routeOrderStatus(req.Status, o.status); !ok {
			if req.Status != "concluido" {
				writeJSONError(w, http.StatusConflict, fmt.Sprintf("A entrega #%d está %s. Confira a rota antes de continuar.", o.id, o.status))
				return
			}
			open++
		}
	}
	if open > 0 {
		writeJSONError(w, http.StatusConflict, fmt.Sprintf("Ainda há %d entrega(s) em aberto nesta rota. Registre a entrega ou a falha antes de concluir.", open))
		return
	}

	var version int64
	err = tx.QueryRowContext(ctx, `
UPDATE routes SET status=$1,version=version+1,
       started_at=CASE WHEN $1='em_andamento' THEN now() ELSE started_at END,
       completed_at=CASE WHEN $1 IN ('concluido','cancelado') THEN now() ELSE completed_at END
WHERE id=$2 RETURNING version`, req.Status, id).Scan(&version)
	if err != nil {
		logisticsFail(w, http.StatusInternalServerError, "Não foi possível alterar a rota.", "route status update failed", err)
		return
	}
	user := userFromContext(ctx).ID
	now := time.Now().UTC()
	moved := 0
	for _, o := range orders {
		next, _ := routeOrderStatus(req.Status, o.status)
		if next == "" {
			continue
		}
		// O identificador nasce da rota e da versão nova, então é único e determinístico.
		op := fmt.Sprintf("route:%d:v%d:%s:%d", id, version, req.Status, o.id)
		if _, _, err := recordDeliveryStatus(ctx, tx, deliveryStatusChange{
			OrderID: o.id, Status: next, OperationID: op, Notes: "Rota #" + fmt.Sprint(id) + " " + req.Status, UserID: user, OccurredAt: now,
		}); err != nil {
			logisticsFail(w, http.StatusInternalServerError, "Não foi possível alterar a rota.", "route status order event failed", err)
			return
		}
		moved++
	}
	if err := tx.Commit(); err != nil {
		logisticsFail(w, http.StatusInternalServerError, "Não foi possível alterar a rota.", "route status commit failed", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"id": id, "status": req.Status, "version": version, "orders_moved": moved})
}

type routeOptimizeRequest struct {
	Version   int64  `json:"version"`
	Departure string `json:"departure"`
	KeepOrder bool   `json:"keep_order"`
}

type routeStopPlan struct {
	OrderID     int64
	Place       string
	Point       latLng
	WindowStart string
	WindowEnd   string
}

// Tempo parado em cada entrega (descarregar, conferir, comprovante).
const routeServiceTime = 10 * time.Minute

func fortalezaLocation() *time.Location {
	if loc, err := time.LoadLocation("America/Fortaleza"); err == nil {
		return loc
	}
	return time.FixedZone("America/Fortaleza", -3*3600)
}

// handleRouteOptimize ordena as paradas pelo menor tempo (ou mantém a ordem, se pedido)
// e grava distância, tempo e previsão de chegada de cada parada. Só em rascunho.
func (a *app) handleRouteOptimize(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var req routeOptimizeRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	req.Departure = strings.TrimSpace(req.Departure)
	if req.Departure == "" {
		req.Departure = "07:00"
	}
	if !deliveryDueAtPattern.MatchString(req.Departure) {
		writeJSONError(w, http.StatusBadRequest, "Horário de saída inválido. Use hora e minuto, como 07:00.")
		return
	}
	ctx := r.Context()
	var status, day string
	var version int64
	err := a.db.QueryRowContext(ctx, `SELECT status,day::text,version FROM routes WHERE id=$1`, id).Scan(&status, &day, &version)
	if errors.Is(err, sql.ErrNoRows) {
		writeJSONError(w, http.StatusNotFound, "Rota não encontrada.")
		return
	}
	if err != nil {
		logisticsFail(w, http.StatusInternalServerError, "Não foi possível calcular a rota.", "route optimize lookup failed", err)
		return
	}
	if version != req.Version {
		writeConcurrentEditError(w)
		return
	}
	if status != "rascunho" {
		writeJSONError(w, http.StatusConflict, "Só dá para reordenar uma rota em rascunho.")
		return
	}
	depot, stops, problem, err := a.routePlanInput(ctx, id)
	if err != nil {
		logisticsFail(w, http.StatusInternalServerError, "Não foi possível calcular a rota.", "route optimize input failed", err)
		return
	}
	if problem != "" {
		writeJSONError(w, http.StatusBadRequest, problem)
		return
	}

	pts := make([]latLng, 0, len(stops)+1)
	pts = append(pts, depot)
	for _, s := range stops {
		pts = append(pts, s.Point)
	}
	matrix, source, osrmErr := travelMatrixFor(ctx, pts)
	if osrmErr != nil {
		log.Printf("route optimize osrm fallback route_id=%d error=%v", id, osrmErr)
	}
	order := make([]int, len(stops))
	for i := range order {
		order[i] = i + 1
	}
	if !req.KeepOrder {
		order = solveOpenPath(matrix.Durations)
	}

	loc := fortalezaLocation()
	clock, _ := time.ParseInLocation("2006-01-02 15:04", day+" "+req.Departure, loc)
	type leg struct {
		orderID            int64
		distance, duration int
		eta                time.Time
	}
	legs := make([]leg, 0, len(order))
	warnings := []string{}
	prev := 0
	totalDistance, totalDuration := 0, 0
	for seq, k := range order {
		s := stops[k-1]
		distance := int(math.Round(matrix.Distances[prev][k]))
		duration := int(math.Round(matrix.Durations[prev][k]))
		if seq > 0 {
			clock = clock.Add(routeServiceTime)
		}
		clock = clock.Add(time.Duration(duration) * time.Second)
		if s.WindowStart != "" {
			opens, _ := time.ParseInLocation("2006-01-02 15:04", day+" "+s.WindowStart, loc)
			if clock.Before(opens) {
				clock = opens // chega cedo e espera a janela abrir
			}
		}
		if s.WindowEnd != "" {
			closes, _ := time.ParseInLocation("2006-01-02 15:04", day+" "+s.WindowEnd, loc)
			if clock.After(closes) {
				warnings = append(warnings, fmt.Sprintf("Parada %d (%s) chega %s, depois da janela que fecha %s.", seq+1, s.Place, clock.Format("15:04"), s.WindowEnd))
			}
		}
		legs = append(legs, leg{orderID: s.OrderID, distance: distance, duration: duration, eta: clock})
		totalDistance += distance
		totalDuration += duration
		prev = k
	}

	tx, err := a.db.BeginTx(ctx, nil)
	if err != nil {
		logisticsFail(w, http.StatusInternalServerError, "Não foi possível calcular a rota.", "route optimize begin failed", err)
		return
	}
	defer tx.Rollback()
	// Confere de novo com a rota travada: alguém pode ter mudado as paradas enquanto o OSRM respondia.
	if _, _, ok := lockRoute(ctx, tx, w, id, req.Version); !ok {
		return
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM route_stops WHERE route_id=$1`, id); err != nil {
		logisticsFail(w, http.StatusInternalServerError, "Não foi possível calcular a rota.", "route optimize delete failed", err)
		return
	}
	out := make([]map[string]any, 0, len(legs))
	for i, l := range legs {
		if _, err := tx.ExecContext(ctx, `
INSERT INTO route_stops (route_id,sequence,order_id,eta,distance_from_prev_m,duration_from_prev_s) VALUES ($1,$2,$3,$4,$5,$6)`,
			id, i+1, l.orderID, l.eta.UTC(), l.distance, l.duration); err != nil {
			logisticsFail(w, http.StatusInternalServerError, "Não foi possível calcular a rota.", "route optimize insert failed", err)
			return
		}
		out = append(out, map[string]any{"sequence": i + 1, "order_id": l.orderID, "eta": l.eta.Format("15:04"),
			"distance_from_prev_m": l.distance, "duration_from_prev_s": l.duration})
	}
	var newVersion int64
	if err := tx.QueryRowContext(ctx, `UPDATE routes SET total_distance_m=$1,total_duration_s=$2,version=version+1 WHERE id=$3 RETURNING version`,
		totalDistance, totalDuration, id).Scan(&newVersion); err != nil {
		logisticsFail(w, http.StatusInternalServerError, "Não foi possível calcular a rota.", "route optimize update failed", err)
		return
	}
	if err := tx.Commit(); err != nil {
		logisticsFail(w, http.StatusInternalServerError, "Não foi possível calcular a rota.", "route optimize commit failed", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"id": id, "version": newVersion, "source": source, "total_distance_m": totalDistance, "total_duration_s": totalDuration,
		"stops": out, "warnings": warnings,
	})
}

// routePlanInput junta o ponto de saída (o local do depósito) e as paradas com coordenada.
// problem vem preenchido quando falta cadastro que só a pessoa pode corrigir.
func (a *app) routePlanInput(ctx context.Context, routeID int64) (depot latLng, stops []routeStopPlan, problem string, err error) {
	rows, err := a.db.QueryContext(ctx, `
SELECT o.id,o.origin_warehouse_id,p.name,p.latitude::float8,p.longitude::float8,
       coalesce(to_char(p.window_start,'HH24:MI'),''),coalesce(to_char(p.window_end,'HH24:MI'),'')
FROM route_stops s
JOIN delivery_orders o ON o.id=s.order_id
JOIN places p ON p.id=o.place_id
WHERE s.route_id=$1
ORDER BY s.sequence`, routeID)
	if err != nil {
		return depot, nil, "", err
	}
	defer rows.Close()
	warehouses := map[int64]bool{}
	var warehouseID int64
	var missing []string
	for rows.Next() {
		var s routeStopPlan
		var wh int64
		var lat, lng sql.NullFloat64
		if err := rows.Scan(&s.OrderID, &wh, &s.Place, &lat, &lng, &s.WindowStart, &s.WindowEnd); err != nil {
			return depot, nil, "", err
		}
		warehouses[wh] = true
		warehouseID = wh
		if !lat.Valid || !lng.Valid {
			missing = append(missing, s.Place)
			continue
		}
		s.Point = latLng{lat.Float64, lng.Float64}
		stops = append(stops, s)
	}
	if err := rows.Err(); err != nil {
		return depot, nil, "", err
	}
	switch {
	case len(warehouses) == 0:
		return depot, nil, "Inclua ao menos uma entrega na rota antes de calcular.", nil
	case len(missing) > 0:
		return depot, nil, "Locais sem coordenada: " + strings.Join(missing, ", ") + ". Cadastre a coordenada em Locais e frota.", nil
	case len(warehouses) > 1:
		return depot, nil, "As entregas desta rota saem de depósitos diferentes. Monte uma rota por depósito.", nil
	case len(stops) > 99:
		return depot, nil, "Rota com entregas demais para calcular. Divida em rotas de até 99 paradas.", nil
	}
	var lat, lng sql.NullFloat64
	var warehouse string
	err = a.db.QueryRowContext(ctx, `
SELECT w.name,p.latitude::float8,p.longitude::float8
FROM warehouses w
LEFT JOIN places p ON p.warehouse_id=w.id AND p.kind='deposito' AND p.active AND p.latitude IS NOT NULL
WHERE w.id=$1
ORDER BY p.id LIMIT 1`, warehouseID).Scan(&warehouse, &lat, &lng)
	if err != nil {
		return depot, nil, "", err
	}
	if !lat.Valid || !lng.Valid {
		return depot, nil, "Cadastre o local do depósito " + warehouse + " com coordenada em Locais e frota. A rota sai de lá.", nil
	}
	return latLng{lat.Float64, lng.Float64}, stops, "", nil
}
