package main

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"math/big"
	"net/http"
	"regexp"
	"strconv"
	"strings"
)

var crateWeightPattern = regexp.MustCompile(`^[0-9]{1,9}(\.[0-9]{1,3})?$`)
var errCrateTrackedStock = errors.New("stock is tracked by individual crates")
var errCrateInvalid = errors.New("invalid crate request")

type crateReceiveRequest struct {
	OperationID    string   `json:"operation_id"`
	SKUID          int64    `json:"sku_id"`
	WarehouseID    int64    `json:"warehouse_id"`
	UnitCost       string   `json:"unit_cost"`
	DocumentNumber string   `json:"document_number"`
	SupplierName   string   `json:"supplier_name"`
	OriginLot      string   `json:"origin_lot"`
	Weights        []string `json:"weights"`
}
type crateWeighRequest struct {
	OperationID string `json:"operation_id"`
	Weight      string `json:"weight"`
	Notes       string `json:"notes"`
}
type crateRow struct {
	ID          int64  `json:"id"`
	Code        string `json:"code"`
	SKUID       int64  `json:"sku_id"`
	WarehouseID int64  `json:"warehouse_id"`
	LotID       int64  `json:"lot_id"`
	OriginLotID int64  `json:"origin_lot_id"`
	MovementID  int64  `json:"movement_id"`
	Position    int    `json:"position"`
	Total       int    `json:"total"`
	Weight      string `json:"weight"`
	Revision    int    `json:"revision"`
	Status      string `json:"status"`
	Product     string `json:"product"`
	Document    string `json:"document"`
	Received    string `json:"received"`
}

func crateCode(id int64) string { return fmt.Sprintf("CX-%06d", id) }
func crateHash(v any) string {
	b, _ := json.Marshal(v)
	x := sha256.Sum256(b)
	return hex.EncodeToString(x[:])
}
func validCrateWeight(s string) bool {
	return crateWeightPattern.MatchString(s) && positiveDecimal(s)
}
func sumCrateWeights(weights []string) (string, error) {
	if len(weights) == 0 || len(weights) > 50 {
		return "", errCrateInvalid
	}
	total := new(big.Rat)
	for _, w := range weights {
		if !validCrateWeight(w) {
			return "", errCrateInvalid
		}
		n, ok := new(big.Rat).SetString(w)
		if !ok {
			return "", errCrateInvalid
		}
		total.Add(total, n)
	}
	return total.FloatString(3), nil
}
func (a *app) registerCrateHandlers(mux *http.ServeMux) {
	mux.HandleFunc("GET /crates", a.uiPage("crates", "Caixas de hortifruti", "movement_entry", "write"))
	mux.HandleFunc("POST /api/crates/receive", a.requireAuth("movement_entry", "write", a.handleCrateReceive))
	mux.HandleFunc("GET /api/crates/{id}", a.requireAuth("stock", "read", a.handleCrateGet))
	mux.HandleFunc("POST /api/crates/{id}/weigh", a.requireAuth("movement_exit", "write", a.handleCrateWeigh))
	mux.HandleFunc("POST /api/crates/{id}/transfer", a.requireAuth("movement_transfer", "write", a.handleCrateTransfer))
}
func (a *app) handleCrateReceive(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	var req crateReceiveRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	req.OperationID = strings.TrimSpace(req.OperationID)
	req.UnitCost = normalizeDecimalInput(req.UnitCost)
	req.DocumentNumber = strings.TrimSpace(req.DocumentNumber)
	req.SupplierName = strings.TrimSpace(req.SupplierName)
	req.OriginLot = strings.TrimSpace(req.OriginLot)
	for i := range req.Weights {
		req.Weights[i] = normalizeDecimalInput(req.Weights[i])
	}
	total, err := sumCrateWeights(req.Weights)
	if err != nil || !matOp(req.OperationID) || req.SKUID <= 0 || req.WarehouseID <= 0 ||
		!nonNegativeDecimal(req.UnitCost) || len(req.DocumentNumber) > 120 || len(req.SupplierName) > 200 || len(req.OriginLot) > 100 {
		writeJSONError(w, 400, "Confira o produto, depósito, pesos e documento.")
		return
	}
	// The operation is atomic: the entry, its stock lot, all crates and all labels.
	tx, err := a.db.BeginTx(r.Context(), nil)
	if err != nil {
		crateError(w, err)
		return
	}
	defer tx.Rollback()
	var unit string
	err = tx.QueryRowContext(r.Context(), `SELECT i.unit_code FROM product_skus s JOIN inventory_items i ON i.id=s.item_id WHERE s.id=$1 AND s.active`, req.SKUID).Scan(&unit)
	if errors.Is(err, sql.ErrNoRows) {
		crateError(w, errNotFound)
		return
	}
	if err != nil {
		crateError(w, err)
		return
	}
	if unit != "KG" {
		writeJSONError(w, 400, "A identificação de caixas por peso exige um produto com unidade base KG.")
		return
	}
	hash := crateHash(req)
	mreq := movementRequest{OperationID: req.OperationID, SKUID: req.SKUID, WarehouseID: req.WarehouseID, Quantity: total, UnitCost: req.UnitCost, DocumentNumber: req.DocumentNumber, SupplierName: req.SupplierName, UserID: userFromContext(r.Context()).ID}
	result, err := createEntryTx(r.Context(), tx, mreq, hash)
	if err != nil {
		crateError(w, err)
		return
	}
	var lotID int64
	err = tx.QueryRowContext(r.Context(), `SELECT l.id FROM stock_lots l JOIN movement_lines ml ON ml.id=l.source_movement_line_id WHERE ml.movement_id=$1 AND ml.direction='IN'`, result.MovementID).Scan(&lotID)
	if err != nil {
		crateError(w, err)
		return
	}
	var count int
	if err = tx.QueryRowContext(r.Context(), `SELECT count(*) FROM stock_crates WHERE entry_movement_id=$1`, result.MovementID).Scan(&count); err != nil {
		crateError(w, err)
		return
	}
	if count == 0 {
		if _, err = tx.ExecContext(r.Context(), `UPDATE stock_lots SET manufacturer_lot_code=NULLIF($2,'') WHERE id=$1`, lotID, req.OriginLot); err != nil {
			crateError(w, err)
			return
		}
		for i, weight := range req.Weights {
			var c crateRow
			err = tx.QueryRowContext(r.Context(), `INSERT INTO stock_crates (stock_lot_id,origin_lot_id,entry_movement_id,sku_id,warehouse_id,box_position,box_total,initial_weight,current_weight)
    VALUES($1,$1,$2,$3,$4,$5,$6,$7,$7) RETURNING id`, lotID, result.MovementID, req.SKUID, req.WarehouseID, i+1, len(req.Weights), weight).Scan(&c.ID)
			if err != nil {
				crateError(w, err)
				return
			}
			if _, err = tx.ExecContext(r.Context(), `INSERT INTO stock_crate_events (crate_id,operation_id,event_type,next_weight,movement_id,actor_id)
    VALUES($1,$2,'RECEIVED',$3,$4,$5)`, c.ID, fmt.Sprintf("%s:crate:%d", req.OperationID, i+1), weight, result.MovementID, mreq.UserID); err != nil {
				crateError(w, err)
				return
			}
			if err = enqueueCrateLabel(r.Context(), tx, c.ID, mreq.UserID); err != nil {
				crateError(w, err)
				return
			}
		}
	} else if count != len(req.Weights) {
		crateError(w, errOperationConflict)
		return
	}
	crates, err := cratesForMovement(r.Context(), tx, result.MovementID)
	if err != nil {
		crateError(w, err)
		return
	}
	if err = tx.Commit(); err != nil {
		crateError(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"movement_id": result.MovementID, "lot_id": lotID, "crates": crates, "status": "confirmed"})
}
func cratesForMovement(ctx context.Context, tx *sql.Tx, id int64) ([]crateRow, error) {
	rows, err := tx.QueryContext(ctx, crateSelect+` WHERE c.entry_movement_id=$1 ORDER BY c.box_position`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []crateRow{}
	for rows.Next() {
		var c crateRow
		if err = scanCrate(rows, &c); err != nil {
			return nil, err
		}
		result = append(result, c)
	}
	return result, rows.Err()
}

const crateSelect = `SELECT c.id,c.sku_id,c.warehouse_id,c.stock_lot_id,c.origin_lot_id,c.entry_movement_id,c.box_position,c.box_total,
 c.current_weight::text,c.revision,c.status,i.name,COALESCE(m.document_number,''),to_char(m.occurred_at AT TIME ZONE 'America/Fortaleza','DD/MM/YYYY')
 FROM stock_crates c JOIN product_skus s ON s.id=c.sku_id JOIN inventory_items i ON i.id=s.item_id JOIN movements m ON m.id=c.entry_movement_id`

type crateScanner interface{ Scan(...any) error }

func scanCrate(row crateScanner, c *crateRow) error {
	err := row.Scan(&c.ID, &c.SKUID, &c.WarehouseID, &c.LotID, &c.OriginLotID, &c.MovementID, &c.Position, &c.Total,
		&c.Weight, &c.Revision, &c.Status, &c.Product, &c.Document, &c.Received)
	c.Code = crateCode(c.ID)
	return err
}
func getCrate(ctx context.Context, tx *sql.Tx, id int64, lock bool) (crateRow, error) {
	q := crateSelect + ` WHERE c.id=$1`
	if lock {
		q += ` FOR UPDATE OF c`
	}
	var c crateRow
	err := scanCrate(tx.QueryRowContext(ctx, q, id), &c)
	if errors.Is(err, sql.ErrNoRows) {
		err = errNotFound
	}
	return c, err
}
func (a *app) handleCrateGet(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		writeJSONError(w, 400, "Caixa inválida.")
		return
	}
	tx, err := a.db.BeginTx(r.Context(), &sql.TxOptions{ReadOnly: true})
	if err != nil {
		crateError(w, err)
		return
	}
	defer tx.Rollback()
	crate, err := getCrate(r.Context(), tx, id, false)
	if err != nil {
		crateError(w, err)
		return
	}
	writeJSON(w, 200, crate)
}
func (a *app) handleCrateWeigh(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		writeJSONError(w, 400, "Caixa inválida.")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	var req crateWeighRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	req.OperationID = strings.TrimSpace(req.OperationID)
	req.Weight = normalizeDecimalInput(req.Weight)
	req.Notes = strings.TrimSpace(req.Notes)
	if !matOp(req.OperationID) || !crateWeightPattern.MatchString(req.Weight) || len(req.Notes) > 500 {
		writeJSONError(w, 400, "Informe a nova pesagem e uma identificação válida para a operação.")
		return
	}
	tx, err := a.db.BeginTx(r.Context(), nil)
	if err != nil {
		crateError(w, err)
		return
	}
	defer tx.Rollback()
	c, err := getCrate(r.Context(), tx, id, true)
	if err != nil {
		crateError(w, err)
		return
	}
	var oldWeight string
	var eventCrateID int64
	err = tx.QueryRowContext(r.Context(), `SELECT crate_id,next_weight::text FROM stock_crate_events WHERE operation_id=$1`, req.OperationID).Scan(&eventCrateID, &oldWeight)
	if err == nil {
		if eventCrateID != id || !equalCrateWeights(oldWeight, req.Weight) {
			crateError(w, errOperationConflict)
			return
		}
		if err = tx.Commit(); err != nil {
			crateError(w, err)
			return
		}
		writeJSON(w, 200, c)
		return
	}
	if !errors.Is(err, sql.ErrNoRows) {
		crateError(w, err)
		return
	}
	// Never replace a label while a device may be printing its prior revision.
	if err = ensureCrateNoInFlightLabel(r.Context(), tx, id); err != nil {
		crateError(w, err)
		return
	}
	var diff string
	err = tx.QueryRowContext(r.Context(), `SELECT ($1::numeric-$2::numeric)::numeric(14,3)::text`, c.Weight, req.Weight).Scan(&diff)
	if err != nil {
		crateError(w, err)
		return
	}
	if strings.HasPrefix(diff, "-") {
		writeJSONError(w, 409, "O aumento de peso exige uma nova entrada ou uma correção auditada.")
		return
	}
	if equalCrateWeights(diff, "0") {
		writeJSON(w, 200, c)
		return
	}
	// A targeted exit allocates exclusively from this crate's lot.
	if err = lockStock(r.Context(), tx, c.SKUID, c.WarehouseID); err != nil {
		crateError(w, err)
		return
	}
	snapshot, err := getStockSnapshot(r.Context(), tx, c.SKUID, c.WarehouseID)
	if err != nil {
		crateError(w, err)
		return
	}
	held, err := matReserved(r.Context(), tx, c.SKUID, c.WarehouseID)
	if err != nil {
		crateError(w, err)
		return
	}
	enough, err := matEnough(r.Context(), tx, snapshot.Quantity, held, diff)
	if err != nil {
		crateError(w, err)
		return
	}
	if !enough {
		crateError(w, errInsufficientStock)
		return
	}
	var remaining string
	err = tx.QueryRowContext(r.Context(), `SELECT (l.initial_quantity-COALESCE(sum(a.quantity),0))::numeric(14,3)::text
  FROM stock_lots l LEFT JOIN stock_lot_allocations a ON a.stock_lot_id=l.id WHERE l.id=$1 GROUP BY l.id`, c.LotID).Scan(&remaining)
	if err != nil {
		crateError(w, err)
		return
	}
	if ok := quantityAtLeast(r.Context(), tx, remaining, diff); !ok {
		crateError(w, errInsufficientStock)
		return
	}
	movementReq := movementRequest{OperationID: req.OperationID, SKUID: c.SKUID, WarehouseID: c.WarehouseID, Quantity: diff, Notes: "Consumo apurado por pesagem " + c.Code + " " + req.Notes, UserID: userFromContext(r.Context()).ID}
	movementID, err := insertMovement(r.Context(), tx, "SAIDA", movementReq, crateHash(req))
	if err != nil {
		crateError(w, err)
		return
	}
	var lineID int64
	err = tx.QueryRowContext(r.Context(), `INSERT INTO movement_lines(movement_id,sku_id,warehouse_id,direction,quantity,unit_cost)
  VALUES($1,$2,$3,'OUT',$4,$5) RETURNING id`, movementID, c.SKUID, c.WarehouseID, diff, snapshot.AvgCost).Scan(&lineID)
	if err != nil {
		crateError(w, err)
		return
	}
	_, err = tx.ExecContext(r.Context(), `INSERT INTO stock_lot_allocations(movement_line_id,stock_lot_id,quantity) VALUES($1,$2,$3)`, lineID, c.LotID, diff)
	if err != nil {
		crateError(w, err)
		return
	}
	_, err = tx.ExecContext(r.Context(), `UPDATE stock_crates SET current_weight=$2,revision=revision+1,
  status=CASE WHEN $2::numeric=0 THEN 'EMPTY' ELSE 'ACTIVE' END,updated_at=now() WHERE id=$1`, id, req.Weight)
	if err != nil {
		crateError(w, err)
		return
	}
	_, err = tx.ExecContext(r.Context(), `INSERT INTO stock_crate_events(crate_id,operation_id,event_type,previous_weight,next_weight,movement_id,actor_id)
   VALUES($1,$2,'REWEIGH',$3,$4,$5,$6)`, id, req.OperationID, c.Weight, req.Weight, movementID, movementReq.UserID)
	if err != nil {
		crateError(w, err)
		return
	}
	if err = enqueueCrateLabel(r.Context(), tx, id, movementReq.UserID); err != nil {
		crateError(w, err)
		return
	}
	c, err = getCrate(r.Context(), tx, id, false)
	if err != nil {
		crateError(w, err)
		return
	}
	if err = tx.Commit(); err != nil {
		crateError(w, err)
		return
	}
	writeJSON(w, 200, c)
}
func equalCrateWeights(a, b string) bool {
	x, ok := new(big.Rat).SetString(a)
	if !ok {
		return false
	}
	y, ok := new(big.Rat).SetString(b)
	return ok && x.Cmp(y) == 0
}
func crateError(w http.ResponseWriter, err error) {
	status, msg := 500, "Não foi possível concluir esta operação."
	switch {
	case errors.Is(err, errNotFound):
		status, msg = 404, "Caixa ou produto não encontrado."
	case errors.Is(err, errOperationConflict):
		status, msg = 409, "Este identificador já foi usado com dados diferentes."
	case errors.Is(err, errCratePrintInFlight):
		status, msg = 409, "A etiqueta anterior está sendo enviada. Aguarde antes de alterar o peso."
	case errors.Is(err, errCrateTrackedStock):
		status, msg = 409, "Este estoque possui caixas identificadas. Use a pesagem da caixa."
	case errors.Is(err, errInsufficientStock):
		status, msg = 409, "Estoque insuficiente para registrar esta pesagem."
	case errors.Is(err, errWarehouseInactive):
		status, msg = 409, "Depósito desativado."
	case errors.Is(err, errCrateInvalid):
		status, msg = 400, "Dados de pesagem inválidos."
	}
	if status == 500 {
		log.Printf("crate operation failed: %v", err)
	}
	writeJSONError(w, status, msg)
}

// Transfer a identified crate without breaking FEFO allocations or its original lot identity.
type crateTransferRequest struct {
	OperationID   string `json:"operation_id"`
	ToWarehouseID int64  `json:"to_warehouse_id"`
}

func (a *app) handleCrateTransfer(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		writeJSONError(w, 400, "Caixa inválida.")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	var req crateTransferRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	req.OperationID = strings.TrimSpace(req.OperationID)
	if !matOp(req.OperationID) || req.ToWarehouseID <= 0 {
		writeJSONError(w, 400, "Informe o depósito de destino.")
		return
	}
	tx, err := a.db.BeginTx(r.Context(), nil)
	if err != nil {
		crateError(w, err)
		return
	}
	defer tx.Rollback()
	c, err := getCrate(r.Context(), tx, id, true)
	if err != nil {
		crateError(w, err)
		return
	}
	var existingCrateID, oldDest int64
	var existingType string
	err = tx.QueryRowContext(r.Context(), `SELECT crate_id,event_type,COALESCE(next_warehouse_id,0) FROM stock_crate_events WHERE operation_id=$1`, req.OperationID).Scan(&existingCrateID, &existingType, &oldDest)
	if err == nil {
		if existingType != "TRANSFERRED" || existingCrateID != id || oldDest != req.ToWarehouseID {
			crateError(w, errOperationConflict)
			return
		}
		if err = tx.Commit(); err != nil {
			crateError(w, err)
			return
		}
		writeJSON(w, 200, c)
		return
	}
	if !errors.Is(err, sql.ErrNoRows) {
		crateError(w, err)
		return
	}
	if c.Status != "ACTIVE" || equalCrateWeights(c.Weight, "0") {
		writeJSONError(w, 409, "Não é possível transferir uma caixa vazia.")
		return
	}
	if c.WarehouseID == req.ToWarehouseID {
		writeJSONError(w, 409, "A caixa já está neste depósito.")
		return
	}
	if err = lockActiveWarehouses(r.Context(), tx, c.WarehouseID, req.ToWarehouseID); err != nil {
		crateError(w, err)
		return
	}
	from, to := c.WarehouseID, req.ToWarehouseID
	if from > to {
		from, to = to, from
	}
	if err = lockStock(r.Context(), tx, c.SKUID, from); err != nil {
		crateError(w, err)
		return
	}
	if err = lockStock(r.Context(), tx, c.SKUID, to); err != nil {
		crateError(w, err)
		return
	}
	snapshot, err := getStockSnapshot(r.Context(), tx, c.SKUID, c.WarehouseID)
	if err != nil {
		crateError(w, err)
		return
	}
	held, err := matReserved(r.Context(), tx, c.SKUID, c.WarehouseID)
	if err != nil {
		crateError(w, err)
		return
	}
	enough, err := matEnough(r.Context(), tx, snapshot.Quantity, held, c.Weight)
	if err != nil {
		crateError(w, err)
		return
	}
	if !enough {
		crateError(w, errInsufficientStock)
		return
	}
	var lotRem string
	err = tx.QueryRowContext(r.Context(), `SELECT (l.initial_quantity-COALESCE(sum(a.quantity),0))::numeric(14,3)::text
 FROM stock_lots l LEFT JOIN stock_lot_allocations a ON a.stock_lot_id=l.id WHERE l.id=$1 GROUP BY l.id`, c.LotID).Scan(&lotRem)
	if err != nil {
		crateError(w, err)
		return
	}
	if !quantityAtLeast(r.Context(), tx, lotRem, c.Weight) {
		crateError(w, errInsufficientStock)
		return
	}
	actor := userFromContext(r.Context()).ID
	mreq := movementRequest{OperationID: req.OperationID, SKUID: c.SKUID, WarehouseID: c.WarehouseID,
		FromWarehouseID: c.WarehouseID, ToWarehouseID: req.ToWarehouseID, Quantity: c.Weight, UserID: actor,
		Notes: "Transferência da caixa " + c.Code}
	moveID, err := insertMovement(r.Context(), tx, "TRANSFERENCIA", mreq, crateHash(req))
	if err != nil {
		crateError(w, err)
		return
	}
	var outLine, inLine int64
	err = tx.QueryRowContext(r.Context(), `INSERT INTO movement_lines(movement_id,sku_id,warehouse_id,direction,quantity,unit_cost)
 VALUES($1,$2,$3,'OUT',$4,$5) RETURNING id`, moveID, c.SKUID, c.WarehouseID, c.Weight, snapshot.AvgCost).Scan(&outLine)
	if err != nil {
		crateError(w, err)
		return
	}
	err = tx.QueryRowContext(r.Context(), `INSERT INTO movement_lines(movement_id,sku_id,warehouse_id,direction,quantity,unit_cost)
 VALUES($1,$2,$3,'IN',$4,$5) RETURNING id`, moveID, c.SKUID, req.ToWarehouseID, c.Weight, snapshot.AvgCost).Scan(&inLine)
	if err != nil {
		crateError(w, err)
		return
	}
	_, err = tx.ExecContext(r.Context(), `INSERT INTO stock_lot_allocations(movement_line_id,stock_lot_id,quantity)
 VALUES($1,$2,$3)`, outLine, c.LotID, c.Weight)
	if err != nil {
		crateError(w, err)
		return
	}
	if err = createTransferLots(r.Context(), tx, outLine, inLine); err != nil {
		crateError(w, err)
		return
	}
	var nextLot int64
	err = tx.QueryRowContext(r.Context(), `SELECT id FROM stock_lots WHERE source_movement_line_id=$1 AND source_lot_id=$2`, inLine, c.LotID).Scan(&nextLot)
	if err != nil {
		crateError(w, err)
		return
	}
	_, err = tx.ExecContext(r.Context(), `UPDATE stock_crates SET stock_lot_id=$2,warehouse_id=$3,updated_at=now() WHERE id=$1`, c.ID, nextLot, req.ToWarehouseID)
	if err != nil {
		crateError(w, err)
		return
	}
	_, err = tx.ExecContext(r.Context(), `INSERT INTO stock_crate_events(crate_id,operation_id,event_type,previous_weight,next_weight,
 movement_id,actor_id,previous_warehouse_id,next_warehouse_id)
 VALUES($1,$2,'TRANSFERRED',$3,$3,$4,$5,$6,$7)`, c.ID, req.OperationID, c.Weight, moveID, actor, c.WarehouseID, req.ToWarehouseID)
	if err != nil {
		crateError(w, err)
		return
	}
	c, err = getCrate(r.Context(), tx, id, false)
	if err != nil {
		crateError(w, err)
		return
	}
	if err = tx.Commit(); err != nil {
		crateError(w, err)
		return
	}
	writeJSON(w, 200, c)
}

var errCratePrintInFlight = errors.New("crate label currently being printed")

func ensureCrateNoInFlightLabel(ctx context.Context, tx *sql.Tx, id int64) error {
	// An expired lease is UNCERTAIN; the operator must reconcile before reprinting.
	if _, err := tx.ExecContext(ctx, `UPDATE print_jobs SET status='UNCERTAIN', last_error='Tempo de confirmação esgotado'
       WHERE crate_id=$1 AND status='CLAIMED' AND claim_expires_at<now()`, id); err != nil {
		return err
	}
	var busy bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM print_jobs WHERE crate_id=$1 AND status='CLAIMED')`, id).Scan(&busy); err != nil {
		return err
	}
	if busy {
		return errCratePrintInFlight
	}
	return nil
}
