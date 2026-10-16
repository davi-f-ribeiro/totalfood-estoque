package main

import (
	"context"
	"database/sql"
	"errors"
	"log"
	"net/http"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
)

// Expedição (fase 3): pedido de entrega com insumos ou refeições prontas, e o histórico
// de status. A saída de estoque não nasce aqui; ela é gravada à parte e ligada à linha.

// Horário combinado é hora:minuto no dia da entrega, no fuso de Fortaleza (como o planejador).
var deliveryDueAtPattern = regexp.MustCompile(`^([01][0-9]|2[0-3]):[0-5][0-9]$`)

var deliveryStatuses = map[string]bool{
	"criado": true, "programado": true, "despachado": true, "em_rota": true,
	"chegou": true, "entregue": true, "falhou": true, "cancelado": true,
}

// deliveryNext diz para quais status cada um pode ir. Pedido entregue ou cancelado não volta.
var deliveryNext = map[string][]string{
	"criado":     {"programado", "cancelado"},
	"programado": {"despachado", "cancelado"},
	"despachado": {"em_rota", "cancelado"},
	"em_rota":    {"chegou", "falhou"},
	"chegou":     {"entregue", "falhou"},
	"falhou":     {"programado"},
	"entregue":   {},
	"cancelado":  {},
}

type deliveryLineRequest struct {
	RecipeID int64  `json:"recipe_id,omitempty"`
	Portions int64  `json:"portions,omitempty"`
	ItemID   int64  `json:"item_id,omitempty"`
	Quantity string `json:"quantity,omitempty"`
}

type deliveryOrderRequest struct {
	OperationID     string                `json:"operation_id"`
	PlaceID         int64                 `json:"place_id"`
	OriginWarehouse int64                 `json:"origin_warehouse_id"`
	Day             string                `json:"day"`
	Meal            string                `json:"meal"`
	DueAt           string                `json:"due_at"`
	PODRequired     *bool                 `json:"pod_required"`
	Notes           string                `json:"notes"`
	Lines           []deliveryLineRequest `json:"lines"`
}

type deliveryEventRequest struct {
	OperationID string `json:"operation_id"`
	Status      string `json:"status"`
	Latitude    string `json:"latitude"`
	Longitude   string `json:"longitude"`
	Notes       string `json:"notes"`
	OccurredAt  string `json:"occurred_at"`
}

func (a *app) registerDispatchHandlers(mux *http.ServeMux) {
	mux.HandleFunc("GET /dispatch", a.uiPage("dispatch", "Expedição", "dispatch", "read"))
	mux.HandleFunc("GET /api/dispatch/options", a.requireAuth("dispatch", "write", a.handleDispatchOptions))
	mux.HandleFunc("GET /api/delivery-orders", a.requireAuth("dispatch", "read", a.handleDeliveryOrders))
	mux.HandleFunc("POST /api/delivery-orders", a.requireAuth("dispatch", "write", a.handleCreateDeliveryOrder))
	mux.HandleFunc("GET /api/delivery-orders/{id}", a.requireAuth("dispatch", "read", a.handleDeliveryOrder))
	mux.HandleFunc("POST /api/delivery-orders/{id}/events", a.requireAuth("dispatch", "write", a.handleDeliveryEvent))
}

func normalizeDeliveryRequest(req *deliveryOrderRequest) string {
	req.OperationID = strings.TrimSpace(req.OperationID)
	req.Day = strings.TrimSpace(req.Day)
	req.DueAt = strings.TrimSpace(req.DueAt)
	req.Meal = strings.TrimSpace(req.Meal)
	switch {
	case req.OperationID == "":
		return "Operação sem identificador. Atualize a página e tente de novo."
	case req.PlaceID <= 0:
		return "Escolha o local de entrega."
	case req.OriginWarehouse <= 0:
		return "Escolha o depósito de saída."
	case !isoDatePattern.MatchString(req.Day):
		return "Data da entrega inválida."
	case req.Meal != "" && !slices.Contains(plannerMealOrder, req.Meal):
		return "Refeição inválida. Use café, almoço, lanche ou jantar."
	case req.DueAt == "":
		return "Informe o horário combinado da entrega."
	case !deliveryDueAtPattern.MatchString(req.DueAt):
		return "Horário combinado inválido. Use hora e minuto, como 11:00."
	case len(req.Lines) == 0:
		return "Inclua ao menos um insumo ou refeição no pedido."
	}
	for i, l := range req.Lines {
		hasMeal := l.RecipeID > 0 || l.Portions > 0
		hasItem := l.ItemID > 0 || strings.TrimSpace(l.Quantity) != ""
		switch {
		case hasMeal && hasItem:
			return "Cada linha é uma refeição ou um insumo, não os dois."
		case hasMeal && (l.RecipeID <= 0 || l.Portions <= 0):
			return "Informe a refeição e a quantidade de porções."
		case hasItem && (l.ItemID <= 0 || !positiveDecimal(normalizeDecimalInput(l.Quantity))):
			return "Informe o insumo e uma quantidade maior que zero."
		case !hasMeal && !hasItem:
			return "Linha " + strconv.Itoa(i+1) + " sem insumo nem refeição."
		}
	}
	return ""
}

func writeDeliveryError(w http.ResponseWriter, err error, logPrefix string) {
	log.Printf("%s error=%v", logPrefix, err)
	msg := err.Error()
	switch {
	case strings.Contains(msg, "delivery_orders_operation_id_key"):
		writeJSONError(w, http.StatusConflict, "Este pedido já foi gravado.")
	case strings.Contains(msg, "delivery_line_kind"):
		writeJSONError(w, http.StatusBadRequest, "Cada linha é uma refeição ou um insumo, não os dois.")
	case strings.Contains(msg, "delivery_order_lines_recipe_id_fkey"):
		writeJSONError(w, http.StatusBadRequest, "Refeição não encontrada. Atualize a página e escolha de novo.")
	case strings.Contains(msg, "delivery_order_lines_item_id_fkey"):
		writeJSONError(w, http.StatusBadRequest, "Insumo não encontrado. Atualize a página e escolha de novo.")
	case strings.Contains(msg, "delivery_orders_place_id_fkey"):
		writeJSONError(w, http.StatusBadRequest, "Local não encontrado. Atualize a página e escolha de novo.")
	default:
		writeJSONError(w, http.StatusBadRequest, "Não foi possível gravar o pedido de entrega.")
	}
}

func (a *app) handleCreateDeliveryOrder(w http.ResponseWriter, r *http.Request) {
	var req deliveryOrderRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	if problem := normalizeDeliveryRequest(&req); problem != "" {
		writeJSONError(w, http.StatusBadRequest, problem)
		return
	}
	token, err := newToken()
	if err != nil {
		logisticsFail(w, http.StatusInternalServerError, "Não foi possível gravar o pedido de entrega.", "delivery token failed", err)
		return
	}
	ctx := r.Context()
	tx, err := a.db.BeginTx(ctx, nil)
	if err != nil {
		logisticsFail(w, http.StatusInternalServerError, "Não foi possível gravar o pedido de entrega.", "delivery begin failed", err)
		return
	}
	defer tx.Rollback()

	// Repetir o mesmo pedido (rede instável no celular) devolve o que já existe.
	var existingID int64
	err = tx.QueryRowContext(ctx, `SELECT id FROM delivery_orders WHERE operation_id=$1`, req.OperationID).Scan(&existingID)
	if err == nil {
		writeJSON(w, http.StatusOK, map[string]any{"id": existingID, "replayed": true})
		return
	}
	if !errors.Is(err, sql.ErrNoRows) {
		logisticsFail(w, http.StatusInternalServerError, "Não foi possível gravar o pedido de entrega.", "delivery lookup failed", err)
		return
	}

	var id int64
	err = tx.QueryRowContext(ctx, `
INSERT INTO delivery_orders (operation_id,place_id,origin_warehouse_id,day,meal,due_at,status,tracking_token,pod_required,notes,created_by)
VALUES ($1,$2,$3,$4::date,$5,($4::date+$6::time) AT TIME ZONE 'America/Fortaleza','criado',$7,$8,$9,$10) RETURNING id`,
		req.OperationID, req.PlaceID, req.OriginWarehouse, req.Day, optionalText(req.Meal), req.DueAt, token,
		req.PODRequired == nil || *req.PODRequired, optionalText(req.Notes), userFromContext(ctx).ID).Scan(&id)
	if err != nil {
		writeDeliveryError(w, err, "delivery order create failed")
		return
	}
	for _, l := range req.Lines {
		if l.RecipeID > 0 {
			_, err = tx.ExecContext(ctx, `INSERT INTO delivery_order_lines (order_id,recipe_id,portions) VALUES ($1,$2,$3)`, id, l.RecipeID, l.Portions)
		} else {
			_, err = tx.ExecContext(ctx, `INSERT INTO delivery_order_lines (order_id,item_id,quantity) VALUES ($1,$2,$3::numeric)`, id, l.ItemID, normalizeDecimalInput(l.Quantity))
		}
		if err != nil {
			writeDeliveryError(w, err, "delivery line create failed")
			return
		}
	}
	if err := tx.Commit(); err != nil {
		logisticsFail(w, http.StatusInternalServerError, "Não foi possível gravar o pedido de entrega.", "delivery commit failed", err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"id": id, "status": "criado", "version": int64(1)})
}

func (a *app) handleDeliveryOrders(w http.ResponseWriter, r *http.Request) {
	day := strings.TrimSpace(r.URL.Query().Get("day"))
	if day == "" {
		day = time.Now().Format("2006-01-02")
	}
	if !isoDatePattern.MatchString(day) {
		writeJSONError(w, http.StatusBadRequest, "Data inválida. Use o formato ano-mês-dia.")
		return
	}
	rows, err := a.db.QueryContext(r.Context(), `
SELECT o.id,o.operation_id,o.day::text,o.meal,to_char(o.due_at AT TIME ZONE 'America/Fortaleza','HH24:MI'),o.status,p.name,w.name,o.pod_required,o.version,
       p.latitude IS NOT NULL,open_route.id,
       (SELECT count(*) FROM delivery_order_lines l WHERE l.order_id=o.id)
FROM delivery_orders o
JOIN places p ON p.id=o.place_id
JOIN warehouses w ON w.id=o.origin_warehouse_id
LEFT JOIN LATERAL (
  SELECT r.id FROM route_stops s JOIN routes r ON r.id=s.route_id
  WHERE s.order_id=o.id AND r.status IN ('rascunho','liberado','em_andamento') LIMIT 1
) open_route ON true
WHERE o.day=$1::date
ORDER BY o.due_at,o.id`, day)
	if err != nil {
		logisticsFail(w, http.StatusInternalServerError, "Não foi possível carregar as entregas.", "delivery list failed", err)
		return
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var id, version int64
		var opID, dayText, status, place, warehouse, dueAt string
		var meal sql.NullString
		var pod, routable bool
		var routeID sql.NullInt64
		var lines int64
		if err := rows.Scan(&id, &opID, &dayText, &meal, &dueAt, &status, &place, &warehouse, &pod, &version, &routable, &routeID, &lines); err != nil {
			logisticsFail(w, http.StatusInternalServerError, "Não foi possível carregar as entregas.", "delivery scan failed", err)
			return
		}
		out = append(out, map[string]any{
			"id": id, "operation_id": opID, "day": dayText, "meal": nullText(meal), "due_at": dueAt,
			"status": status, "place_name": place, "origin_warehouse": warehouse, "pod_required": pod, "version": version,
			"has_coordinates": routable, "route_id": nullInt(routeID), "lines": lines,
		})
	}
	if err := rows.Err(); err != nil {
		logisticsFail(w, http.StatusInternalServerError, "Não foi possível carregar as entregas.", "delivery rows failed", err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (a *app) handleDeliveryOrder(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	ctx := r.Context()
	var opID, dayText, status, place, warehouse, dueAt, token string
	var meal, notes sql.NullString
	var pod bool
	var version int64
	err := a.db.QueryRowContext(ctx, `
SELECT o.operation_id,o.day::text,o.meal,to_char(o.due_at AT TIME ZONE 'America/Fortaleza','HH24:MI'),o.status,p.name,w.name,o.pod_required,o.notes,o.tracking_token,o.version
FROM delivery_orders o
JOIN places p ON p.id=o.place_id
JOIN warehouses w ON w.id=o.origin_warehouse_id
WHERE o.id=$1`, id).Scan(&opID, &dayText, &meal, &dueAt, &status, &place, &warehouse, &pod, &notes, &token, &version)
	if errors.Is(err, sql.ErrNoRows) {
		writeJSONError(w, http.StatusNotFound, "Entrega não encontrada.")
		return
	}
	if err != nil {
		logisticsFail(w, http.StatusInternalServerError, "Não foi possível carregar a entrega.", "delivery detail failed", err)
		return
	}
	lines, err := a.deliveryLines(ctx, id)
	if err != nil {
		logisticsFail(w, http.StatusInternalServerError, "Não foi possível carregar a entrega.", "delivery lines failed", err)
		return
	}
	events, err := a.deliveryEvents(ctx, id)
	if err != nil {
		logisticsFail(w, http.StatusInternalServerError, "Não foi possível carregar a entrega.", "delivery events failed", err)
		return
	}
	proofs, err := a.deliveryProofs(ctx, id)
	if err != nil {
		logisticsFail(w, http.StatusInternalServerError, "Não foi possível carregar a entrega.", "delivery proofs failed", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"id": id, "operation_id": opID, "day": dayText, "meal": nullText(meal), "due_at": dueAt,
		"status": status, "place_name": place, "origin_warehouse": warehouse, "pod_required": pod,
		"notes": nullText(notes), "tracking_token": token, "version": version,
		"lines": lines, "events": events, "proofs": proofs,
		"next_statuses": deliveryNext[status],
	})
}

func (a *app) deliveryLines(ctx context.Context, orderID int64) ([]map[string]any, error) {
	rows, err := a.db.QueryContext(ctx, `
SELECT l.id,l.recipe_id,r.name,l.portions,l.item_id,i.name,i.unit_code,l.quantity::text,l.movement_line_id
FROM delivery_order_lines l
LEFT JOIN recipes r ON r.id=l.recipe_id
LEFT JOIN inventory_items i ON i.id=l.item_id
WHERE l.order_id=$1 ORDER BY l.id`, orderID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var id int64
		var recipeID, portions, itemID, movementLineID sql.NullInt64
		var recipeName, itemName, unit, quantity sql.NullString
		if err := rows.Scan(&id, &recipeID, &recipeName, &portions, &itemID, &itemName, &unit, &quantity, &movementLineID); err != nil {
			return nil, err
		}
		out = append(out, map[string]any{
			"id": id, "recipe_id": nullInt(recipeID), "recipe_name": nullText(recipeName), "portions": nullInt(portions),
			"item_id": nullInt(itemID), "item_name": nullText(itemName), "unit": nullText(unit), "quantity": nullText(quantity),
			"movement_line_id": nullInt(movementLineID),
		})
	}
	return out, rows.Err()
}

func (a *app) deliveryEvents(ctx context.Context, orderID int64) ([]map[string]any, error) {
	rows, err := a.db.QueryContext(ctx, `
SELECT status,latitude::text,longitude::text,notes,occurred_at
FROM delivery_events WHERE order_id=$1 ORDER BY occurred_at,id`, orderID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var status string
		var lat, lng, notes sql.NullString
		var at time.Time
		if err := rows.Scan(&status, &lat, &lng, &notes, &at); err != nil {
			return nil, err
		}
		out = append(out, map[string]any{
			"status": status, "latitude": nullText(lat), "longitude": nullText(lng),
			"notes": nullText(notes), "occurred_at": at.UTC().Format(time.RFC3339),
		})
	}
	return out, rows.Err()
}

// handleDeliveryEvent troca o status seguindo deliveryNext. Repetir o mesmo evento
// com o mesmo operation_id não grava de novo, então um reenvio do celular é seguro.
func (a *app) handleDeliveryEvent(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var req deliveryEventRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	req.OperationID = strings.TrimSpace(req.OperationID)
	req.Status = strings.TrimSpace(req.Status)
	if req.OperationID == "" {
		writeJSONError(w, http.StatusBadRequest, "Evento sem identificador. Tente de novo.")
		return
	}
	if !deliveryStatuses[req.Status] {
		writeJSONError(w, http.StatusBadRequest, "Status de entrega inválido.")
		return
	}
	lat, okLat := parseCoordinate(req.Latitude, 90)
	lng, okLng := parseCoordinate(req.Longitude, 180)
	if !okLat || !okLng || (lat == "") != (lng == "") {
		writeJSONError(w, http.StatusBadRequest, "Localização inválida. Informe latitude e longitude juntas, ou deixe as duas em branco.")
		return
	}
	occurred := time.Now().UTC()
	if strings.TrimSpace(req.OccurredAt) != "" {
		t, err := time.Parse(time.RFC3339, strings.TrimSpace(req.OccurredAt))
		if err != nil {
			writeJSONError(w, http.StatusBadRequest, "Horário do evento inválido.")
			return
		}
		occurred = t.UTC()
	}
	ctx := r.Context()
	tx, err := a.db.BeginTx(ctx, nil)
	if err != nil {
		logisticsFail(w, http.StatusInternalServerError, "Não foi possível registrar o status.", "event begin failed", err)
		return
	}
	defer tx.Rollback()

	var current string
	var podRequired bool
	var version int64
	err = tx.QueryRowContext(ctx, `SELECT status,version,pod_required FROM delivery_orders WHERE id=$1 FOR UPDATE`, id).Scan(&current, &version, &podRequired)
	if errors.Is(err, sql.ErrNoRows) {
		writeJSONError(w, http.StatusNotFound, "Entrega não encontrada.")
		return
	}
	if err != nil {
		logisticsFail(w, http.StatusInternalServerError, "Não foi possível registrar o status.", "event lookup failed", err)
		return
	}

	var existing int64
	err = tx.QueryRowContext(ctx, `SELECT id FROM delivery_events WHERE operation_id=$1`, req.OperationID).Scan(&existing)
	if err == nil {
		writeJSON(w, http.StatusOK, map[string]any{"status": current, "version": version, "replayed": true})
		return
	}
	if !errors.Is(err, sql.ErrNoRows) {
		logisticsFail(w, http.StatusInternalServerError, "Não foi possível registrar o status.", "event dedupe failed", err)
		return
	}

	if !deliveryTransitionAllowed(current, req.Status) {
		writeJSONError(w, http.StatusConflict, "Não dá para mudar de "+current+" para "+req.Status+". Atualize a página e confira o status atual.")
		return
	}
	if req.Status == "entregue" && podRequired {
		writeJSONError(w, http.StatusConflict, "Esta entrega exige comprovante. Registre com foto, assinatura ou código.")
		return
	}
	eventID, newVersion, err := recordDeliveryStatus(ctx, tx, deliveryStatusChange{
		OrderID: id, Status: req.Status, OperationID: req.OperationID, Latitude: lat, Longitude: lng,
		Notes: req.Notes, UserID: userFromContext(ctx).ID, OccurredAt: occurred,
	})
	if err != nil {
		logisticsFail(w, http.StatusInternalServerError, "Não foi possível registrar o status.", "event insert failed", err)
		return
	}
	if err := tx.Commit(); err != nil {
		logisticsFail(w, http.StatusInternalServerError, "Não foi possível registrar o status.", "event commit failed", err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"id": eventID, "status": req.Status, "version": newVersion})
}

type deliveryStatusChange struct {
	OrderID     int64
	Status      string
	OperationID string
	Latitude    string
	Longitude   string
	Notes       string
	UserID      int64
	OccurredAt  time.Time
}

// recordDeliveryStatus grava o evento e muda o status do pedido. Quem chama já travou
// o pedido e conferiu a transição com deliveryTransitionAllowed.
func recordDeliveryStatus(ctx context.Context, tx *sql.Tx, c deliveryStatusChange) (eventID, version int64, err error) {
	err = tx.QueryRowContext(ctx, `
INSERT INTO delivery_events (order_id,status,operation_id,latitude,longitude,notes,user_id,occurred_at)
VALUES ($1,$2,$3,$4::numeric,$5::numeric,$6,$7,$8) RETURNING id`,
		c.OrderID, c.Status, c.OperationID, optionalText(c.Latitude), optionalText(c.Longitude), optionalText(c.Notes),
		c.UserID, c.OccurredAt).Scan(&eventID)
	if err != nil {
		return 0, 0, err
	}
	err = tx.QueryRowContext(ctx, `UPDATE delivery_orders SET status=$1,version=version+1 WHERE id=$2 RETURNING version`, c.Status, c.OrderID).Scan(&version)
	return eventID, version, err
}

func deliveryTransitionAllowed(from, to string) bool {
	for _, next := range deliveryNext[from] {
		if next == to {
			return true
		}
	}
	return false
}

// handleDispatchOptions junta o que os formulários da expedição precisam escolher.
func (a *app) handleDispatchOptions(w http.ResponseWriter, r *http.Request) {
	queries := map[string]string{
		"places":     `SELECT id,name||CASE WHEN latitude IS NULL THEN ' (sem coordenada)' ELSE '' END FROM places WHERE active AND kind<>'deposito' ORDER BY name`,
		"warehouses": `SELECT id,name FROM warehouses WHERE active ORDER BY name`,
		"items":      `SELECT id,name||' ('||unit_code||')' FROM inventory_items WHERE active ORDER BY name`,
		"recipes":    `SELECT id,name FROM recipes WHERE active ORDER BY name`,
		"vehicles":   `SELECT id,plate||' · '||description||CASE WHEN ownership='alugada' THEN ' (alugado)' ELSE '' END FROM vehicles WHERE active ORDER BY plate`,
		"drivers":    `SELECT id,name FROM drivers WHERE active ORDER BY name`,
	}
	out := map[string]any{}
	for key, q := range queries {
		rows, err := simpleRows(r, a.db, q)
		if err != nil {
			logisticsFail(w, http.StatusInternalServerError, "Não foi possível carregar as opções da expedição.", "dispatch options "+key+" failed", err)
			return
		}
		out[key] = rows
	}
	writeJSON(w, http.StatusOK, out)
}
