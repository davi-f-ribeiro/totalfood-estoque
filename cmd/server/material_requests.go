package main

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"
)

var materialQtyPattern = regexp.MustCompile("^(0|[1-9][0-9]{0,10})([.][0-9]{1,3})?$")

type materialError struct {
	status  int
	message string
}

func (e materialError) Error() string   { return e.message }
func matErr(code int, msg string) error { return materialError{code, msg} }
func matBad(msg string) error           { return matErr(400, msg) }
func matConflict(msg string) error      { return matErr(409, msg) }
func matMissing() error                 { return matErr(404, "Requisição ou item não encontrado.") }
func (a *app) matRespond(w http.ResponseWriter, err error) {
	var e materialError
	if errors.As(err, &e) {
		writeJSONError(w, e.status, e.message)
		return
	}
	if errors.Is(err, errInsufficientStock) {
		writeJSONError(w, 409, "Estoque insuficiente.")
		return
	}
	if errors.Is(err, errWarehouseInactive) {
		writeJSONError(w, 409, "Depósito desativado.")
		return
	}
	log.Printf("material requests: %v", err)
	writeJSONError(w, 500, "Não foi possível concluir. Confira a operação antes de reenviar.")
}

// Saldo que ainda falta separar de uma linha: pedido menos o reservado ativo e o já entregue.
// É a única conta desse saldo: o detalhe envia o valor por linha e a lista conta as linhas com saldo.
const materialLineToSeparateSQL = `GREATEST(l.quantity - COALESCE((SELECT sum(res.remaining_quantity) FROM material_request_reservations res WHERE res.request_line_id = l.id AND res.status = 'ATIVA'), 0) - COALESCE((SELECT sum(d.quantity) FROM material_request_deliveries d JOIN material_request_reservations res ON res.id = d.reservation_id WHERE res.request_line_id = l.id), 0), 0)`

func matHash(v any) string {
	b, _ := json.Marshal(v)
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}
func matQty(s string) bool { return materialQtyPattern.MatchString(s) && positiveDecimal(s) }
func matOp(s string) bool  { return len(s) > 0 && len(s) <= 120 && !strings.Contains(s, ":") }
func matPath(w http.ResponseWriter, r *http.Request, key string) (int64, bool) {
	n, e := strconv.ParseInt(r.PathValue(key), 10, 64)
	if e != nil || n <= 0 {
		writeJSONError(w, 400, "Identificador inválido.")
		return 0, false
	}
	return n, true
}
func matDecode(w http.ResponseWriter, r *http.Request, v any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	defer r.Body.Close()
	d := json.NewDecoder(r.Body)
	d.DisallowUnknownFields()
	if d.Decode(v) != nil {
		writeJSONError(w, 400, "Dados inválidos.")
		return false
	}
	var extra any
	if !errors.Is(d.Decode(&extra), io.EOF) {
		writeJSONError(w, 400, "Envie um único documento JSON.")
		return false
	}
	return true
}

type materialLineInput struct {
	ItemID   int64  `json:"item_id"`
	Quantity string `json:"quantity"`
}
type materialCreateInput struct {
	OperationID    string              `json:"operation_id"`
	UnitName       string              `json:"unit_name"`
	DeliveryDate   string              `json:"delivery_date"`
	ProductionDate string              `json:"production_date"`
	Service        string              `json:"service"`
	Notes          string              `json:"notes"`
	Lines          []materialLineInput `json:"lines"`
}
type materialReserveInput struct {
	OperationID string `json:"operation_id"`
	LineID      int64  `json:"line_id"`
	SKUID       int64  `json:"sku_id"`
	WarehouseID int64  `json:"warehouse_id"`
	Quantity    string `json:"quantity"`
}
type materialDeliveryInput struct {
	OperationID   string `json:"operation_id"`
	ReservationID int64  `json:"reservation_id"`
	Quantity      string `json:"quantity"`
	// CrateIDs: caixas de onde o material sai, para item controlado por caixa (hortifruti).
	// Várias caixas saem inteiras (a quantidade é a soma dos pesos atuais); uma caixa só
	// pode sair em parte, e o restante fica nela com etiqueta nova.
	CrateIDs []int64 `json:"crate_ids,omitempty"`
}

// matWeightTolerance: quanto um item em KG pode passar do pedido na separação e na entrega.
// Hortifruti e proteína não têm peso padrão; a cozinha confere e aceita o peso real.
const matWeightTolerance = "1.2"

func (a *app) registerMaterialHandlers(mux *http.ServeMux) {
	mux.HandleFunc("GET /material-requests", a.uiPage("material_requests", "Requisições", "material_requests", "read"))
	mux.HandleFunc("GET /api/material-requests", a.requireAuth("material_requests", "read", a.handleMaterialList))
	mux.HandleFunc("GET /api/material-requests/search", a.requireAuth("material_requests", "read", a.handleMaterialListPage))
	mux.HandleFunc("GET /api/material-requests/items", a.requireAuth("material_requests", "read", a.handleMaterialItems))
	mux.HandleFunc("GET /api/material-requests/options/{item_id}", a.requireAuth("material_fulfillment", "write", a.handleMaterialOptions))
	mux.HandleFunc("POST /api/material-requests", a.requireAuth("material_requests", "write", a.handleMaterialCreate))
	mux.HandleFunc("GET /api/material-requests/{id}", a.requireAuth("material_requests", "read", a.handleMaterialDetail))
	mux.HandleFunc("POST /api/material-requests/{id}/reserve", a.requireAuth("material_fulfillment", "write", a.handleMaterialReserve))
	mux.HandleFunc("POST /api/material-requests/{id}/deliver", a.requireAuth("material_fulfillment", "write", a.handleMaterialDeliver))
	mux.HandleFunc("POST /api/material-requests/{id}/reservations/{reservation_id}/release", a.requireAuth("material_fulfillment", "write", a.handleMaterialRelease))
	mux.HandleFunc("POST /api/material-requests/{id}/deliveries/{delivery_id}/receive", a.requireAuth("material_receipts", "write", a.handleMaterialReceive))
	mux.HandleFunc("POST /api/material-requests/{id}/close", a.requireAuth("material_receipts", "write", a.handleMaterialClose))
	mux.HandleFunc("POST /api/material-requests/{id}/deliveries/{delivery_id}/reconcile", a.requireAuth("material_receipts", "write", a.handleMaterialReconcile))
	mux.HandleFunc("POST /api/material-requests/{id}/edit", a.requireAuth("material_requests", "write", a.handleMaterialEdit))
	mux.HandleFunc("POST /api/material-requests/{id}/cancel", a.requireAuth("material_requests", "write", a.handleMaterialCancel))
}
func matEvent(ctx context.Context, tx *sql.Tx, id, actor int64, action, detail string) error {
	_, err := tx.ExecContext(ctx, "INSERT INTO material_request_events(request_id,actor_id,action,details) VALUES($1,$2,$3,$4)", id, actor, action, detail)
	return err
}
func (a *app) handleMaterialItems(w http.ResponseWriter, r *http.Request) {
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	if len([]rune(q)) < 2 {
		writeJSON(w, 200, []any{})
		return
	}
	rows, err := a.db.QueryContext(r.Context(), "SELECT id,name,unit_code FROM inventory_items WHERE active AND strpos(translate(lower(name), "+searchAccents+"), translate(lower($1::text), "+searchAccents+")) > 0 ORDER BY lower(name),id LIMIT 40", q)
	if err != nil {
		a.matRespond(w, err)
		return
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var id int64
		var name, unit string
		if err = rows.Scan(&id, &name, &unit); err != nil {
			a.matRespond(w, err)
			return
		}
		out = append(out, map[string]any{"id": id, "name": name, "unit": unit})
	}
	if err = rows.Err(); err != nil {
		a.matRespond(w, err)
		return
	}
	writeJSON(w, 200, out)
}
func (a *app) handleMaterialOptions(w http.ResponseWriter, r *http.Request) {
	itemID, ok := matPath(w, r, "item_id")
	if !ok {
		return
	}
	rows, err := a.db.QueryContext(r.Context(), "SELECT s.id,COALESCE(NULLIF(s.name,''),CASE WHEN b.id=0 THEN 'Sem marca' ELSE b.name END),CASE WHEN b.id=0 THEN 'Sem marca' ELSE b.name END,COALESCE(s.barcode,''),s.content_quantity::text,w.id,w.name,COALESCE(bal.qty,0)::text,GREATEST(COALESCE(bal.qty,0)-COALESCE(held.qty,0),0)::text FROM product_skus s JOIN brands b ON b.id=s.brand_id CROSS JOIN warehouses w LEFT JOIN (SELECT ml.item_id,ps.brand_id,ml.warehouse_id,sum(CASE WHEN ml.direction='IN' THEN ml.quantity ELSE -ml.quantity END) AS qty FROM movement_lines ml JOIN product_skus ps ON ps.id=ml.sku_id GROUP BY ml.item_id,ps.brand_id,ml.warehouse_id) bal ON bal.item_id=s.item_id AND bal.brand_id=s.brand_id AND bal.warehouse_id=w.id LEFT JOIN (SELECT rs.item_id,rs.brand_id,res.warehouse_id,sum(res.remaining_quantity) AS qty FROM material_request_reservations res JOIN product_skus rs ON rs.id=res.sku_id WHERE res.status='ATIVA' GROUP BY rs.item_id,rs.brand_id,res.warehouse_id) held ON held.item_id=s.item_id AND held.brand_id=s.brand_id AND held.warehouse_id=w.id WHERE s.item_id=$1 AND s.active AND w.active AND COALESCE(bal.qty,0)-COALESCE(held.qty,0)>0 ORDER BY b.name,s.content_quantity,w.name,s.id", itemID)
	if err != nil {
		a.matRespond(w, err)
		return
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var id, whID int64
		var name, brand, barcode, factor, wh, qty, available string
		if err = rows.Scan(&id, &name, &brand, &barcode, &factor, &whID, &wh, &qty, &available); err != nil {
			a.matRespond(w, err)
			return
		}
		out = append(out, map[string]any{"sku_id": id, "sku": name, "brand": brand, "barcode": barcode, "content_quantity": factor, "warehouse_id": whID, "warehouse": wh, "stock": qty, "available": available})
	}
	if err = rows.Err(); err != nil {
		a.matRespond(w, err)
		return
	}
	writeJSON(w, 200, out)
}

// matNormalizeRequest limpa e valida os dados do pedido (criação e edição usam a mesma regra).
// Devolve a mensagem de erro, ou "" se estiver tudo certo.
func matNormalizeRequest(req *materialCreateInput) string {
	req.UnitName = strings.TrimSpace(req.UnitName)
	req.Notes = strings.TrimSpace(req.Notes)
	req.Service = strings.TrimSpace(req.Service)
	if len(req.UnitName) == 0 || len(req.UnitName) > 120 || len(req.Notes) > 1000 ||
		req.DeliveryDate == "" || !validProductionDate(req.DeliveryDate) ||
		req.ProductionDate == "" || !validProductionDate(req.ProductionDate) ||
		(req.Service != "cafe" && req.Service != "almoco" && req.Service != "lanche" && req.Service != "jantar") ||
		len(req.Lines) == 0 || len(req.Lines) > 100 {
		return "Confira unidade, datas, serviço e itens da requisição."
	}
	seen := map[int64]bool{}
	for i := range req.Lines {
		l := &req.Lines[i]
		l.Quantity = normalizeDecimalInput(l.Quantity)
		if l.ItemID <= 0 || seen[l.ItemID] || !matQty(l.Quantity) {
			return "Itens repetidos ou quantidades inválidas (até 3 casas decimais)."
		}
		seen[l.ItemID] = true
	}
	return ""
}
func (a *app) handleMaterialCreate(w http.ResponseWriter, r *http.Request) {
	var req materialCreateInput
	if !matDecode(w, r, &req) {
		return
	}
	req.OperationID = strings.TrimSpace(req.OperationID)
	if !matOp(req.OperationID) {
		writeJSONError(w, 400, "Confira unidade, datas, serviço e itens da requisição.")
		return
	}
	if msg := matNormalizeRequest(&req); msg != "" {
		writeJSONError(w, 400, msg)
		return
	}
	h := matHash(req)
	actor := userFromContext(r.Context()).ID
	ctx := r.Context()
	tx, err := a.db.BeginTx(ctx, nil)
	if err != nil {
		a.matRespond(w, err)
		return
	}
	defer tx.Rollback()
	var id int64
	err = tx.QueryRowContext(ctx, "INSERT INTO material_requests(operation_id,request_hash,unit_name,delivery_date,production_date,service,notes,created_by) VALUES($1,$2,$3,$4::date,$5::date,$6,$7,$8) ON CONFLICT(operation_id) DO NOTHING RETURNING id", req.OperationID, h, req.UnitName, req.DeliveryDate, req.ProductionDate, req.Service, req.Notes, actor).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		var oldHash string
		var owner int64
		if err = tx.QueryRowContext(ctx, "SELECT id,request_hash,created_by FROM material_requests WHERE operation_id=$1", req.OperationID).Scan(&id, &oldHash, &owner); err != nil {
			a.matRespond(w, err)
			return
		}
		if oldHash != h || owner != actor {
			a.matRespond(w, matConflict("Este envio já foi utilizado com outros dados."))
			return
		}
		writeJSON(w, 200, map[string]any{"id": id, "status": "confirmed"})
		return
	}
	if err != nil {
		a.matRespond(w, err)
		return
	}
	for _, l := range req.Lines {
		var active bool
		if err = tx.QueryRowContext(ctx, "SELECT active FROM inventory_items WHERE id=$1", l.ItemID).Scan(&active); err != nil {
			a.matRespond(w, matBad("Item não encontrado."))
			return
		}
		if !active {
			a.matRespond(w, matBad("Um dos itens está inativo."))
			return
		}
		if _, err = tx.ExecContext(ctx, "INSERT INTO material_request_lines(request_id,item_id,quantity) VALUES($1,$2,$3::numeric)", id, l.ItemID, l.Quantity); err != nil {
			a.matRespond(w, err)
			return
		}
	}
	if err = matEvent(ctx, tx, id, actor, "CRIADA", req.Service+" · "+req.ProductionDate); err != nil {
		a.matRespond(w, err)
		return
	}
	if err = tx.Commit(); err != nil {
		a.matRespond(w, err)
		return
	}
	writeJSON(w, 201, map[string]any{"id": id, "status": "confirmed"})
}
func (a *app) matAccess(ctx context.Context, id, actor int64) error {
	var owner int64
	err := a.db.QueryRowContext(ctx, "SELECT created_by FROM material_requests WHERE id=$1", id).Scan(&owner)
	if errors.Is(err, sql.ErrNoRows) {
		return matMissing()
	}
	if err != nil {
		return err
	}
	if owner == actor {
		return nil
	}
	allowed, err := a.hasPermission(ctx, actor, "material_fulfillment", "write")
	if err != nil {
		return err
	}
	if !allowed {
		return matErr(403, "Você não tem acesso a esta requisição.")
	}
	return nil
}
func (a *app) handleMaterialList(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	actor := userFromContext(ctx).ID
	all, err := a.hasPermission(ctx, actor, "material_fulfillment", "write")
	if err != nil {
		a.matRespond(w, err)
		return
	}
	rows, err := a.db.QueryContext(ctx, "SELECT r.id,r.unit_name,r.delivery_date::text,r.production_date::text,r.service,r.status,r.created_at,u.login,COALESCE(t.n,0) FROM material_requests r JOIN users u ON u.id=r.created_by LEFT JOIN (SELECT request_id,count(*) n FROM material_request_lines GROUP BY request_id)t ON t.request_id=r.id WHERE ($1::boolean OR r.created_by=$2) ORDER BY (r.status IN ('RECEBIDA','CANCELADA')),r.delivery_date,r.created_at DESC LIMIT 150", all, actor)
	if err != nil {
		a.matRespond(w, err)
		return
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var id, n int64
		var unit, delivery, production, service, status, owner string
		var created time.Time
		if err = rows.Scan(&id, &unit, &delivery, &production, &service, &status, &created, &owner, &n); err != nil {
			a.matRespond(w, err)
			return
		}
		out = append(out, map[string]any{"id": id, "unit_name": unit, "delivery_date": delivery, "production_date": production, "service": service, "status": status, "requested_by": owner, "created_at": created, "items": n})
	}
	if err = rows.Err(); err != nil {
		a.matRespond(w, err)
		return
	}
	writeJSON(w, 200, out)
}
func (a *app) handleMaterialDetail(w http.ResponseWriter, r *http.Request) {
	id, ok := matPath(w, r, "id")
	if !ok {
		return
	}
	ctx := r.Context()
	actor := userFromContext(ctx).ID
	if err := a.matAccess(ctx, id, actor); err != nil {
		a.matRespond(w, err)
		return
	}
	var unit, delivery, production, service, status, notes, owner string
	var created time.Time
	var closedAt sql.NullTime
	var closedBy, closeNotes string
	var version int64
	err := a.db.QueryRowContext(ctx, "SELECT r.unit_name,r.delivery_date::text,r.production_date::text,r.service,r.status,r.notes,r.created_at,u.login,r.closed_at,COALESCE(cb.login,''),r.close_notes,r.version FROM material_requests r JOIN users u ON u.id=r.created_by LEFT JOIN users cb ON cb.id=r.closed_by WHERE r.id=$1", id).Scan(&unit, &delivery, &production, &service, &status, &notes, &created, &owner, &closedAt, &closedBy, &closeNotes, &version)
	if err != nil {
		a.matRespond(w, err)
		return
	}
	lines := []map[string]any{}
	rows, err := a.db.QueryContext(ctx, "SELECT l.id,i.id,i.name,i.unit_code,l.quantity::text,COALESCE((SELECT sum(res.remaining_quantity) FROM material_request_reservations res WHERE res.request_line_id=l.id AND res.status='ATIVA'),0)::text,COALESCE((SELECT sum(d.quantity) FROM material_request_deliveries d JOIN material_request_reservations res ON res.id=d.reservation_id WHERE res.request_line_id=l.id),0)::text,"+materialLineToSeparateSQL+"::text FROM material_request_lines l JOIN inventory_items i ON i.id=l.item_id WHERE l.request_id=$1 ORDER BY lower(i.name),l.id", id)
	if err != nil {
		a.matRespond(w, err)
		return
	}
	for rows.Next() {
		var lid, itemID int64
		var name, unitCode, qty, reserved, delivered, toSeparate string
		if err = rows.Scan(&lid, &itemID, &name, &unitCode, &qty, &reserved, &delivered, &toSeparate); err != nil {
			rows.Close()
			a.matRespond(w, err)
			return
		}
		lines = append(lines, map[string]any{"id": lid, "item_id": itemID, "item": name, "unit": unitCode, "quantity": qty, "reserved": reserved, "delivered": delivered, "to_separate": toSeparate})
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		a.matRespond(w, err)
		return
	}
	reservations := []map[string]any{}
	rows, err = a.db.QueryContext(ctx, "SELECT res.id,res.request_line_id,res.sku_id,res.warehouse_id,res.quantity::text,res.remaining_quantity::text,res.status,COALESCE(NULLIF(s.name,''),CASE WHEN b.id=0 THEN 'Sem marca' ELSE b.name END),CASE WHEN b.id=0 THEN 'Sem marca' ELSE b.name END,w.name FROM material_request_reservations res JOIN product_skus s ON s.id=res.sku_id JOIN brands b ON b.id=s.brand_id JOIN warehouses w ON w.id=res.warehouse_id JOIN material_request_lines l ON l.id=res.request_line_id WHERE l.request_id=$1 ORDER BY res.id", id)
	if err != nil {
		a.matRespond(w, err)
		return
	}
	for rows.Next() {
		var rid, lid, skuID, whID int64
		var qty, remaining, state, sku, brand, warehouse string
		if err = rows.Scan(&rid, &lid, &skuID, &whID, &qty, &remaining, &state, &sku, &brand, &warehouse); err != nil {
			rows.Close()
			a.matRespond(w, err)
			return
		}
		reservations = append(reservations, map[string]any{"id": rid, "line_id": lid, "sku_id": skuID, "warehouse_id": whID, "quantity": qty, "remaining": remaining, "status": state, "sku": sku, "brand": brand, "warehouse": warehouse})
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		a.matRespond(w, err)
		return
	}
	deliveries := []map[string]any{}
	rows, err = a.db.QueryContext(ctx, "SELECT d.id,d.reservation_id,d.quantity::text,d.movement_id,d.delivered_at,d.received_at IS NOT NULL,COALESCE(del.login,''),COALESCE(rec.login,''),COALESCE(d.received_quantity::text,''),(d.quantity-COALESCE(d.received_quantity,d.quantity))::numeric(14,3)::text,d.divergence_reason FROM material_request_deliveries d LEFT JOIN users del ON del.id=d.delivered_by LEFT JOIN users rec ON rec.id=d.received_by WHERE d.request_id=$1 ORDER BY d.id", id)
	if err != nil {
		a.matRespond(w, err)
		return
	}
	for rows.Next() {
		var did, rid, movementID int64
		var qty, deliverer, receiver, got, pendingDiv, reason string
		var at time.Time
		var received bool
		if err = rows.Scan(&did, &rid, &qty, &movementID, &at, &received, &deliverer, &receiver, &got, &pendingDiv, &reason); err != nil {
			rows.Close()
			a.matRespond(w, err)
			return
		}
		deliveries = append(deliveries, map[string]any{"id": did, "reservation_id": rid, "quantity": qty, "movement_id": movementID, "delivered_at": at, "received": received, "delivered_by": deliverer, "received_by": receiver,
			"received_quantity": got, "pending_divergence": pendingDiv, "divergence_reason": reason, "receipts": []map[string]any{}, "crates": []map[string]any{}})
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		a.matRespond(w, err)
		return
	}
	// Caixas que saíram em cada entrega (item controlado por caixa).
	rows, err = a.db.QueryContext(ctx, "SELECT dc.delivery_id,dc.crate_id,dc.quantity::text FROM material_request_delivery_crates dc JOIN material_request_deliveries d ON d.id=dc.delivery_id WHERE d.request_id=$1 ORDER BY dc.delivery_id,dc.crate_id", id)
	if err != nil {
		a.matRespond(w, err)
		return
	}
	for rows.Next() {
		var did, cid int64
		var qty string
		if err = rows.Scan(&did, &cid, &qty); err != nil {
			rows.Close()
			a.matRespond(w, err)
			return
		}
		for _, dv := range deliveries {
			if dv["id"] == did {
				dv["crates"] = append(dv["crates"].([]map[string]any), map[string]any{"id": cid, "code": crateCode(cid), "quantity": qty})
			}
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		a.matRespond(w, err)
		return
	}
	// Histórico de conferências e conciliações de cada entrega.
	rows, err = a.db.QueryContext(ctx, "SELECT rc.delivery_id,rc.kind,rc.quantity::text,rc.notes,rc.occurred_at,u.login FROM material_request_receipts rc JOIN material_request_deliveries d ON d.id=rc.delivery_id JOIN users u ON u.id=rc.actor_id WHERE d.request_id=$1 ORDER BY rc.id", id)
	if err != nil {
		a.matRespond(w, err)
		return
	}
	for rows.Next() {
		var did int64
		var kind, qty, notes, who string
		var at time.Time
		if err = rows.Scan(&did, &kind, &qty, &notes, &at, &who); err != nil {
			rows.Close()
			a.matRespond(w, err)
			return
		}
		for _, dv := range deliveries {
			if dv["id"] == did {
				dv["receipts"] = append(dv["receipts"].([]map[string]any), map[string]any{"kind": kind, "quantity": qty, "notes": notes, "at": at, "by": who})
			}
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		a.matRespond(w, err)
		return
	}
	events := []map[string]any{}
	rows, err = a.db.QueryContext(ctx, "SELECT e.action,e.details,e.occurred_at,u.login FROM material_request_events e JOIN users u ON u.id=e.actor_id WHERE e.request_id=$1 ORDER BY e.id DESC LIMIT 100", id)
	if err != nil {
		a.matRespond(w, err)
		return
	}
	for rows.Next() {
		var action, details, who string
		var at time.Time
		if err = rows.Scan(&action, &details, &at, &who); err != nil {
			rows.Close()
			a.matRespond(w, err)
			return
		}
		events = append(events, map[string]any{"action": action, "details": details, "at": at, "by": who})
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		a.matRespond(w, err)
		return
	}
	var closedWhen any
	if closedAt.Valid {
		closedWhen = closedAt.Time
	}
	writeJSON(w, 200, map[string]any{"id": id, "unit_name": unit, "delivery_date": delivery, "production_date": production,
		"service": service, "status": status, "notes": notes, "created_at": created, "requested_by": owner,
		"closed_at": closedWhen, "closed_by": closedBy, "close_notes": closeNotes, "version": version,
		"lines": lines, "reservations": reservations, "deliveries": deliveries, "events": events})
}

// All reservation and delivery actions lock the parent request first.
// Stock locks are shared with the existing WMS, so concurrent requests and exits serialize.
func matLocked(ctx context.Context, tx *sql.Tx, id int64) (string, int64, string, error) {
	var status, production string
	var owner int64
	err := tx.QueryRowContext(ctx, "SELECT status,created_by,production_date::text FROM material_requests WHERE id=$1 FOR UPDATE", id).Scan(&status, &owner, &production)
	if errors.Is(err, sql.ErrNoRows) {
		return "", 0, "", matMissing()
	}
	return status, owner, production, err
}
func matReserved(ctx context.Context, tx *sql.Tx, skuID, whID int64) (string, error) {
	var qty string
	err := tx.QueryRowContext(ctx, "SELECT COALESCE(sum(res.remaining_quantity),0)::text FROM material_request_reservations res JOIN product_skus s ON s.id=res.sku_id JOIN product_skus chosen ON chosen.id=$1 WHERE s.item_id=chosen.item_id AND s.brand_id=chosen.brand_id AND res.warehouse_id=$2 AND res.status='ATIVA'", skuID, whID).Scan(&qty)
	return qty, err
}
func matFreeStock(ctx context.Context, tx *sql.Tx, have, held string) (string, error) {
	var free string
	err := tx.QueryRowContext(ctx, "SELECT GREATEST($1::numeric - $2::numeric,0)::text", have, held).Scan(&free)
	return free, err
}
func matEnough(ctx context.Context, tx *sql.Tx, have, held, want string) (bool, error) {
	var enough bool
	err := tx.QueryRowContext(ctx, "SELECT $1::numeric - $2::numeric >= $3::numeric", have, held, want).Scan(&enough)
	return enough, err
}
func matLine(ctx context.Context, tx *sql.Tx, lineID, requestID int64) (string, int64, error) {
	var qty string
	var itemID int64
	err := tx.QueryRowContext(ctx, "SELECT quantity::text,item_id FROM material_request_lines WHERE id=$1 AND request_id=$2", lineID, requestID).Scan(&qty, &itemID)
	if errors.Is(err, sql.ErrNoRows) {
		return "", 0, matMissing()
	}
	return qty, itemID, err
}
func (a *app) handleMaterialReserve(w http.ResponseWriter, r *http.Request) {
	id, ok := matPath(w, r, "id")
	if !ok {
		return
	}
	var req materialReserveInput
	if !matDecode(w, r, &req) {
		return
	}
	req.OperationID = strings.TrimSpace(req.OperationID)
	req.Quantity = normalizeDecimalInput(req.Quantity)
	if !matOp(req.OperationID) || req.LineID <= 0 || req.SKUID <= 0 || req.WarehouseID <= 0 || !matQty(req.Quantity) {
		writeJSONError(w, 400, "Informe produto, depósito e quantidade válida.")
		return
	}
	h := matHash(req)
	ctx := r.Context()
	actor := userFromContext(ctx).ID
	tx, err := a.db.BeginTx(ctx, nil)
	if err != nil {
		a.matRespond(w, err)
		return
	}
	defer tx.Rollback()
	status, _, _, err := matLocked(ctx, tx, id)
	if err != nil {
		a.matRespond(w, err)
		return
	}
	var oldID, owner int64
	var oldHash string
	err = tx.QueryRowContext(ctx, "SELECT id,request_hash,created_by FROM material_request_reservations WHERE operation_id=$1", req.OperationID).Scan(&oldID, &oldHash, &owner)
	if err == nil {
		if oldHash != h || owner != actor {
			a.matRespond(w, matConflict("Este envio já foi utilizado com outros dados."))
			return
		}
		var oldRequest int64
		if err = tx.QueryRowContext(ctx, "SELECT l.request_id FROM material_request_reservations res JOIN material_request_lines l ON l.id=res.request_line_id WHERE res.id=$1", oldID).Scan(&oldRequest); err != nil {
			a.matRespond(w, err)
			return
		}
		if oldRequest != id {
			a.matRespond(w, matConflict("Este envio pertence a outra requisição."))
			return
		}
		writeJSON(w, 200, map[string]any{"id": oldID, "status": "confirmed"})
		return
	}
	if !errors.Is(err, sql.ErrNoRows) {
		a.matRespond(w, err)
		return
	}
	if status == "CANCELADA" || matReceiptDone(status) || status == "ENTREGUE" {
		a.matRespond(w, matConflict("Esta requisição não aceita novas separações."))
		return
	}
	asked, itemID, err := matLine(ctx, tx, req.LineID, id)
	if err != nil {
		a.matRespond(w, err)
		return
	}
	var skuItem int64
	err = tx.QueryRowContext(ctx, "SELECT item_id FROM product_skus WHERE id=$1 AND active", req.SKUID).Scan(&skuItem)
	if errors.Is(err, sql.ErrNoRows) {
		a.matRespond(w, matBad("Produto não encontrado ou inativo."))
		return
	}
	if err != nil {
		a.matRespond(w, err)
		return
	}
	if skuItem != itemID {
		a.matRespond(w, matBad("A apresentação não pertence ao material solicitado."))
		return
	}
	if err = lockActiveWarehouses(ctx, tx, req.WarehouseID); err != nil {
		a.matRespond(w, err)
		return
	}
	if err = lockStock(ctx, tx, req.SKUID, req.WarehouseID); err != nil {
		a.matRespond(w, err)
		return
	}
	stock, err := getStockSnapshot(ctx, tx, req.SKUID, req.WarehouseID)
	if err != nil {
		a.matRespond(w, err)
		return
	}
	held, err := matReserved(ctx, tx, req.SKUID, req.WarehouseID)
	if err != nil {
		a.matRespond(w, err)
		return
	}
	enough, err := matEnough(ctx, tx, stock.Quantity, held, req.Quantity)
	if err != nil {
		a.matRespond(w, err)
		return
	}
	if !enough {
		a.matRespond(w, matConflict("Saldo livre insuficiente para reservar esta quantidade."))
		return
	}
	var within bool
	err = tx.QueryRowContext(ctx, "SELECT $1::numeric + COALESCE((SELECT sum(remaining_quantity) FROM material_request_reservations WHERE request_line_id=$2 AND status='ATIVA'),0) + COALESCE((SELECT sum(d.quantity) FROM material_request_deliveries d JOIN material_request_reservations res ON res.id=d.reservation_id WHERE res.request_line_id=$2),0) <= $3::numeric * CASE WHEN (SELECT unit_code FROM inventory_items WHERE id=$4)='KG' THEN "+matWeightTolerance+" ELSE 1 END", req.Quantity, req.LineID, asked, itemID).Scan(&within)
	if err != nil {
		a.matRespond(w, err)
		return
	}
	if !within {
		a.matRespond(w, matConflict("Quantidade acima do saldo ainda solicitado. Itens em KG aceitam até 20% acima do pedido."))
		return
	}
	var rid int64
	err = tx.QueryRowContext(ctx, "INSERT INTO material_request_reservations(request_line_id,sku_id,warehouse_id,quantity,remaining_quantity,operation_id,request_hash,created_by) VALUES($1,$2,$3,$4::numeric,$4::numeric,$5,$6,$7) RETURNING id", req.LineID, req.SKUID, req.WarehouseID, req.Quantity, req.OperationID, h, actor).Scan(&rid)
	if err != nil {
		a.matRespond(w, err)
		return
	}
	if err = matEvent(ctx, tx, id, actor, "RESERVADA", fmt.Sprintf("Reserva %d: %s em depósito %d", rid, req.Quantity, req.WarehouseID)); err != nil {
		a.matRespond(w, err)
		return
	}
	if status == "PENDENTE" {
		if _, err = tx.ExecContext(ctx, "UPDATE material_requests SET status='EM_SEPARACAO',updated_at=now() WHERE id=$1", id); err != nil {
			a.matRespond(w, err)
			return
		}
	}
	if err = tx.Commit(); err != nil {
		a.matRespond(w, err)
		return
	}
	writeJSON(w, 201, map[string]any{"id": rid, "status": "confirmed"})
}
func matRefresh(ctx context.Context, tx *sql.Tx, id int64) error {
	// Entregue por linha (um item em KG pode passar do pedido; isso não compensa a falta de outro).
	var allDelivered, anyDelivered, anyHeld, allReceived, divergent, closed bool
	err := tx.QueryRowContext(ctx, "SELECT NOT EXISTS(SELECT 1 FROM material_request_lines l WHERE l.request_id=$1 AND l.quantity>COALESCE((SELECT sum(d.quantity) FROM material_request_reservations res JOIN material_request_deliveries d ON d.reservation_id=res.id WHERE res.request_line_id=l.id),0)),EXISTS(SELECT 1 FROM material_request_deliveries WHERE request_id=$1),(SELECT closed_at IS NOT NULL FROM material_requests WHERE id=$1)", id).Scan(&allDelivered, &anyDelivered, &closed)
	if err != nil {
		return err
	}
	err = tx.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM material_request_reservations res JOIN material_request_lines l ON l.id=res.request_line_id WHERE l.request_id=$1 AND res.status='ATIVA' AND res.remaining_quantity>0),NOT EXISTS(SELECT 1 FROM material_request_deliveries WHERE request_id=$1 AND received_at IS NULL),EXISTS(SELECT 1 FROM material_request_deliveries WHERE request_id=$1 AND received_quantity<quantity)", id).Scan(&anyHeld, &allReceived, &divergent)
	if err != nil {
		return err
	}
	status := "PENDENTE"
	if anyHeld {
		status = "EM_SEPARACAO"
	}
	if anyDelivered {
		status = "ENTREGA_PARCIAL"
	}
	if allDelivered {
		status = "ENTREGUE"
	}
	if allDelivered && allReceived {
		status = "RECEBIDA"
		if divergent { // falta conciliar o que a cozinha não recebeu
			status = "RECEBIDA_COM_DIVERGENCIA"
		}
	}
	if closed { // a cozinha aceitou o que chegou e encerrou
		status = "RECEBIDA"
	}
	if status == "RECEBIDA" {
		// Nada mais será entregue: o que sobrou reservado volta a ficar livre.
		if _, err = tx.ExecContext(ctx, "UPDATE material_request_reservations SET remaining_quantity=0,status='LIBERADA',released_at=now() WHERE status='ATIVA' AND request_line_id IN (SELECT id FROM material_request_lines WHERE request_id=$1)", id); err != nil {
			return err
		}
	}
	_, err = tx.ExecContext(ctx, "UPDATE material_requests SET status=$2,updated_at=now() WHERE id=$1", id, status)
	return err
}
func (a *app) handleMaterialDeliver(w http.ResponseWriter, r *http.Request) {
	id, ok := matPath(w, r, "id")
	if !ok {
		return
	}
	var req materialDeliveryInput
	if !matDecode(w, r, &req) {
		return
	}
	req.OperationID = strings.TrimSpace(req.OperationID)
	req.Quantity = normalizeDecimalInput(req.Quantity)
	if !matOp(req.OperationID) || req.ReservationID <= 0 || !matQty(req.Quantity) {
		writeJSONError(w, 400, "Confira a reserva e a quantidade entregue.")
		return
	}
	h := matHash(req)
	ctx := r.Context()
	actor := userFromContext(ctx).ID
	tx, err := a.db.BeginTx(ctx, nil)
	if err != nil {
		a.matRespond(w, err)
		return
	}
	defer tx.Rollback()
	status, _, production, err := matLocked(ctx, tx, id)
	if err != nil {
		a.matRespond(w, err)
		return
	}
	var oldID, oldRequest, oldOwner int64
	var oldHash string
	err = tx.QueryRowContext(ctx, "SELECT id,request_id,request_hash,delivered_by FROM material_request_deliveries WHERE operation_id=$1", req.OperationID).Scan(&oldID, &oldRequest, &oldHash, &oldOwner)
	if err == nil {
		if oldRequest != id || oldHash != h || oldOwner != actor {
			a.matRespond(w, matConflict("Este envio já foi utilizado com outros dados."))
			return
		}
		writeJSON(w, 200, map[string]any{"id": oldID, "status": "confirmed"})
		return
	}
	if !errors.Is(err, sql.ErrNoRows) {
		a.matRespond(w, err)
		return
	}
	// "ENTREGUE" não bloqueia: com peso real, o pedido pode já estar coberto e ainda haver
	// saldo separado a entregar. Quem limita a entrega é a reserva.
	if status == "CANCELADA" || matReceiptDone(status) {
		a.matRespond(w, matConflict("Esta requisição não aceita novas entregas."))
		return
	}
	var skuID, whID, actualRequest int64
	var remaining, state string
	err = tx.QueryRowContext(ctx, "SELECT res.sku_id,res.warehouse_id,l.request_id,res.remaining_quantity::text,res.status FROM material_request_reservations res JOIN material_request_lines l ON l.id=res.request_line_id WHERE res.id=$1 FOR UPDATE OF res", req.ReservationID).Scan(&skuID, &whID, &actualRequest, &remaining, &state)
	if errors.Is(err, sql.ErrNoRows) {
		a.matRespond(w, matMissing())
		return
	}
	if err != nil {
		a.matRespond(w, err)
		return
	}
	if actualRequest != id || state != "ATIVA" {
		a.matRespond(w, matConflict("Reserva indisponível para entrega."))
		return
	}
	enough, err := matEnough(ctx, tx, remaining, "0", req.Quantity)
	if err != nil {
		a.matRespond(w, err)
		return
	}
	if !enough {
		a.matRespond(w, matConflict("Entrega maior do que a quantidade separada."))
		return
	}
	// Consumir a reserva nesta mesma transação libera exatamente a parcela
	// entregue para a saída física; falhas de saldo/lote revertem ambas.
	_, err = tx.ExecContext(ctx, "UPDATE material_request_reservations SET remaining_quantity=remaining_quantity-$2::numeric,status=CASE WHEN remaining_quantity=$2::numeric THEN 'CONCLUIDA' ELSE 'ATIVA' END WHERE id=$1", req.ReservationID, req.Quantity)
	if err != nil {
		a.matRespond(w, err)
		return
	}
	mv := movementRequest{OperationID: fmt.Sprintf("material:%d:%s", id, req.OperationID), SKUID: skuID, WarehouseID: whID, Quantity: req.Quantity, ProductionDate: production, DocumentNumber: fmt.Sprintf("REQ-%d", id), Notes: fmt.Sprintf("Requisição interna %d", id), UserID: actor}
	var result operationResult
	var usedCrates []matCrateUse
	if len(req.CrateIDs) > 0 {
		// Item em caixa: a saída é alocada no lote de cada caixa e o peso delas diminui.
		result.MovementID, usedCrates, err = matDeliverFromCrates(ctx, tx, req.CrateIDs, skuID, whID, mv)
	} else {
		result, err = createExitTx(ctx, tx, mv, matHash(mv))
		if errors.Is(err, errCrateTrackedStock) {
			err = matConflict("Este item é controlado por caixa. Escolha a caixa de onde o material vai sair.")
		}
	}
	if err != nil {
		a.matRespond(w, err)
		return
	}
	var did int64
	err = tx.QueryRowContext(ctx, "INSERT INTO material_request_deliveries(request_id,reservation_id,quantity,movement_id,operation_id,request_hash,delivered_by) VALUES($1,$2,$3::numeric,$4,$5,$6,$7) RETURNING id", id, req.ReservationID, req.Quantity, result.MovementID, req.OperationID, h, actor).Scan(&did)
	if err != nil {
		a.matRespond(w, err)
		return
	}
	detail := fmt.Sprintf("Entrega %d: %s, movimento %d", did, req.Quantity, result.MovementID)
	for i, u := range usedCrates {
		if _, err = tx.ExecContext(ctx, "INSERT INTO material_request_delivery_crates(delivery_id,crate_id,quantity) VALUES($1,$2,$3::numeric)", did, u.ID, u.Quantity); err != nil {
			a.matRespond(w, err)
			return
		}
		if i == 0 {
			detail += ", caixas"
		}
		detail += " " + u.Code + " (" + u.Quantity + ")"
	}
	if err = matEvent(ctx, tx, id, actor, "ENTREGUE", detail); err != nil {
		a.matRespond(w, err)
		return
	}
	if err = matRefresh(ctx, tx, id); err != nil {
		a.matRespond(w, err)
		return
	}
	if err = tx.Commit(); err != nil {
		a.matRespond(w, err)
		return
	}
	writeJSON(w, 201, map[string]any{"id": did, "movement_id": result.MovementID, "status": "confirmed"})
}
func (a *app) handleMaterialRelease(w http.ResponseWriter, r *http.Request) {
	id, ok := matPath(w, r, "id")
	if !ok {
		return
	}
	rid, ok := matPath(w, r, "reservation_id")
	if !ok {
		return
	}
	ctx := r.Context()
	actor := userFromContext(ctx).ID
	tx, err := a.db.BeginTx(ctx, nil)
	if err != nil {
		a.matRespond(w, err)
		return
	}
	defer tx.Rollback()
	status, _, _, err := matLocked(ctx, tx, id)
	if err != nil {
		a.matRespond(w, err)
		return
	}
	if status == "CANCELADA" || matReceiptDone(status) {
		a.matRespond(w, matConflict("Requisição encerrada."))
		return
	}
	var actual int64
	var state string
	err = tx.QueryRowContext(ctx, "SELECT l.request_id,res.status FROM material_request_reservations res JOIN material_request_lines l ON l.id=res.request_line_id WHERE res.id=$1 FOR UPDATE OF res", rid).Scan(&actual, &state)
	if errors.Is(err, sql.ErrNoRows) {
		a.matRespond(w, matMissing())
		return
	}
	if err != nil {
		a.matRespond(w, err)
		return
	}
	if actual != id {
		a.matRespond(w, matMissing())
		return
	}
	if state == "ATIVA" {
		if _, err = tx.ExecContext(ctx, "UPDATE material_request_reservations SET remaining_quantity=0,status='LIBERADA',released_by=$2,released_at=now() WHERE id=$1", rid, actor); err != nil {
			a.matRespond(w, err)
			return
		}
		if err = matEvent(ctx, tx, id, actor, "LIBERADA", fmt.Sprintf("Reserva %d liberada", rid)); err != nil {
			a.matRespond(w, err)
			return
		}
	}
	if err = matRefresh(ctx, tx, id); err != nil {
		a.matRespond(w, err)
		return
	}
	if err = tx.Commit(); err != nil {
		a.matRespond(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"id": rid, "status": "confirmed"})
}
func (a *app) handleMaterialCancel(w http.ResponseWriter, r *http.Request) {
	id, ok := matPath(w, r, "id")
	if !ok {
		return
	}
	ctx := r.Context()
	actor := userFromContext(ctx).ID
	manager, err := a.hasPermission(ctx, actor, "material_fulfillment", "write")
	if err != nil {
		a.matRespond(w, err)
		return
	}
	tx, err := a.db.BeginTx(ctx, nil)
	if err != nil {
		a.matRespond(w, err)
		return
	}
	defer tx.Rollback()
	status, owner, _, err := matLocked(ctx, tx, id)
	if err != nil {
		a.matRespond(w, err)
		return
	}

	if actor != owner && !manager {
		a.matRespond(w, matErr(403, "Somente o solicitante pode cancelar."))
		return
	}
	if status == "CANCELADA" {
		writeJSON(w, 200, map[string]any{"id": id, "status": "CANCELADA"})
		return
	}
	var delivered bool
	if err = tx.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM material_request_deliveries WHERE request_id=$1)", id).Scan(&delivered); err != nil {
		a.matRespond(w, err)
		return
	}
	if delivered {
		a.matRespond(w, matConflict("Já houve entrega. Confirme o recebimento em vez de cancelar."))
		return
	}
	if _, err = tx.ExecContext(ctx, "UPDATE material_request_reservations SET remaining_quantity=0,status='LIBERADA',released_by=$2,released_at=now() WHERE status='ATIVA' AND request_line_id IN (SELECT id FROM material_request_lines WHERE request_id=$1)", id, actor); err != nil {
		a.matRespond(w, err)
		return
	}
	if _, err = tx.ExecContext(ctx, "UPDATE material_requests SET status='CANCELADA',updated_at=now() WHERE id=$1", id); err != nil {
		a.matRespond(w, err)
		return
	}
	if err = matEvent(ctx, tx, id, actor, "CANCELADA", "Requisição cancelada sem saída"); err != nil {
		a.matRespond(w, err)
		return
	}
	if err = tx.Commit(); err != nil {
		a.matRespond(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"id": id, "status": "CANCELADA"})
}
