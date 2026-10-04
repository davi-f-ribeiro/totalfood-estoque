package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"regexp"
	"strings"
)

const presentationQuantitySemantics = "presentation"

var decimalPattern = regexp.MustCompile(`^(0|[1-9][0-9]*)(\.[0-9]{1,4})?$`)

var (
	errValidation                    = errors.New("validation rejected")
	errMovementOperationIDMissing    = fmt.Errorf("%w: operation id missing", errValidation)
	errMovementProductRequired       = fmt.Errorf("%w: product required", errValidation)
	errMovementQuantityInvalid       = fmt.Errorf("%w: invalid quantity", errValidation)
	errMovementCostInvalid           = fmt.Errorf("%w: invalid unit cost", errValidation)
	errMovementWarehouseRequired     = fmt.Errorf("%w: warehouse required", errValidation)
	errTransferWarehousesRequired    = fmt.Errorf("%w: transfer warehouses required", errValidation)
	errTransferSameWarehouse         = fmt.Errorf("%w: transfer warehouses must differ", errValidation)
	errNotFound                      = errors.New("resource not found")
	errInsufficientStock             = errors.New("insufficient stock")
	errOperationConflict             = errors.New("operation_id conflicts with existing operation")
	errOperationNotFound             = errors.New("operation not found")
	errDuplicateFiscalDocument       = errors.New("fiscal document already confirmed")
)

type insufficientStockError struct {
	Available string
	Unit      string
}

func (e insufficientStockError) Error() string { return errInsufficientStock.Error() }
func (e insufficientStockError) Unwrap() error { return errInsufficientStock }

type movementRequest struct {
	OperationID     string `json:"operation_id"`
	SKUID           int64  `json:"sku_id"`
	WarehouseID     int64  `json:"warehouse_id"`
	FromWarehouseID int64  `json:"warehouse_from_id"`
	ToWarehouseID   int64  `json:"warehouse_to_id"`
	Quantity        string `json:"quantity"`
	QuantitySemantics string `json:"quantity_semantics,omitempty"`
	UnitCost        string `json:"unit_cost"`
	DocumentNumber  string `json:"document_number"`
	Responsible     string `json:"responsible"`
	SupplierName    string `json:"supplier_name"`
	Notes           string `json:"notes"`
	UserID          int64  `json:"-"`
}

type operationResult struct {
	OperationID string `json:"operation_id"`
	MovementID  int64  `json:"movement_id"`
	Status      string `json:"status"`
	Type        string `json:"type"`
	UserID      int64  `json:"-"`
}

type stockSnapshot struct {
	Quantity string
	Value    string
	AvgCost  string
	Unit     string
}

func (a *app) registerMovementHandlers(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/movements/entry", a.requireAuth("movement_entry", "write", a.handleEntry))
	mux.HandleFunc("POST /api/movements/exit", a.requireAuth("movement_exit", "write", a.handleExit))
	mux.HandleFunc("POST /api/movements/transfer", a.requireAuth("movement_transfer", "write", a.handleTransfer))
	mux.HandleFunc("GET /api/operations/{operation_id}", a.requireAuthenticatedRead(a.handleGetOperation))
	mux.HandleFunc("GET /api/reports/movements", a.requireAuth("operations", "read", a.handleMovementReport))
	mux.HandleFunc("GET /api/reports/balance", a.requireAuth("operations", "read", a.handleBalanceReport))
	mux.HandleFunc("GET /api/reports/warehouses", a.requireAuth("operations", "read", a.handleWarehouses))
}

func (a *app) handleEntry(w http.ResponseWriter, r *http.Request) {
	req, hash, ok := decodeMovementRequest(w, r, true)
	if !ok {
		return
	}
	res, err := a.createEntry(r.Context(), withAuthUser(req, r), hash)
	a.writeOperationResponse(w, "ENTRADA", req.OperationID, res, err)
}

func (a *app) handleExit(w http.ResponseWriter, r *http.Request) {
	req, hash, ok := decodeMovementRequest(w, r, false)
	if !ok {
		return
	}
	res, err := a.createExit(r.Context(), withAuthUser(req, r), hash)
	a.writeOperationResponse(w, "SAIDA", req.OperationID, res, err)
}

func (a *app) handleTransfer(w http.ResponseWriter, r *http.Request) {
	req, hash, ok := decodeMovementRequest(w, r, false)
	if !ok {
		return
	}
	res, err := a.createTransfer(r.Context(), withAuthUser(req, r), hash)
	a.writeOperationResponse(w, "TRANSFERENCIA", req.OperationID, res, err)
}

func (a *app) handleGetOperation(w http.ResponseWriter, r *http.Request) {
	operationID := strings.TrimSpace(r.PathValue("operation_id"))
	if operationID == "" {
		writeJSONError(w, http.StatusBadRequest, "invalid operation_id")
		return
	}
	res, err := a.getOperation(r.Context(), operationID)
	if err != nil {
		if errors.Is(err, errOperationNotFound) {
			writeJSONError(w, http.StatusNotFound, "Operação não encontrada.")
			return
		}
		log.Printf("operation_id=%s type=GET stage=lookup error=%v", operationID, err)
		writeJSONError(w, http.StatusInternalServerError, "internal error")
		return
	}
	u := userFromContext(r.Context())
	global, err := a.hasPermission(r.Context(), u.ID, "operations", "read")
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "internal error")
		return
	}
	if !global {
		resource := map[string]string{"ENTRADA": "movement_entry", "SAIDA": "movement_exit", "TRANSFERENCIA": "movement_transfer", "AJUSTE": "stock_adjustment"}[res.Type]
		own := res.UserID > 0 && res.UserID == u.ID
		allowed := false
		if own && resource != "" {
			allowed, err = a.hasPermission(r.Context(), u.ID, resource, "write")
			if err != nil {
				writeJSONError(w, http.StatusInternalServerError, "internal error")
				return
			}
		}
		if !allowed {
			writeJSONError(w, http.StatusForbidden, "forbidden")
			return
		}
	}
	writeJSON(w, http.StatusOK, res)
}

func decodeMovementRequest(w http.ResponseWriter, r *http.Request, requireCost bool) (movementRequest, string, bool) {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	defer r.Body.Close()
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	var req movementRequest
	if err := dec.Decode(&req); err != nil {
		writeJSONError(w, http.StatusBadRequest, "Os dados da movimentação enviados são inválidos.")
		return req, "", false
	}
	req.OperationID = strings.TrimSpace(req.OperationID)
	req.Quantity = normalizeDecimalInput(req.Quantity)
	req.QuantitySemantics = strings.TrimSpace(req.QuantitySemantics)
	req.UnitCost = normalizeDecimalInput(req.UnitCost)
	if req.QuantitySemantics != "" && req.QuantitySemantics != presentationQuantitySemantics {
		writeJSONError(w, http.StatusBadRequest, "Semântica de quantidade inválida.")
		return req, "", false
	}
	body, _ := json.Marshal(req)
	h := sha256.Sum256(body)
	if req.OperationID == "" {
		writeJSONError(w, http.StatusBadRequest, "Não foi possível identificar esta operação. Recarregue a página antes de tentar novamente.")
		return req, "", false
	}
	if req.SKUID <= 0 {
		writeJSONError(w, http.StatusBadRequest, "Selecione um produto antes de registrar a movimentação.")
		return req, "", false
	}
	if !positiveDecimal(req.Quantity) {
		writeJSONError(w, http.StatusBadRequest, "Informe uma quantidade maior que zero, com até 4 casas decimais.")
		return req, "", false
	}
	if requireCost && !nonNegativeDecimal(req.UnitCost) {
		writeJSONError(w, http.StatusBadRequest, "Informe um custo unitário válido, igual ou maior que zero, com até 4 casas decimais.")
		return req, "", false
	}
	return req, hex.EncodeToString(h[:]), true
}

func positiveDecimal(v string) bool {
	if !decimalPattern.MatchString(v) {
		return false
	}
	return v != "0" && v != "0.0" && v != "0.00" && v != "0.000" && v != "0.0000"
}

func nonNegativeDecimal(v string) bool { return decimalPattern.MatchString(v) }

func normalizeDecimalInput(v string) string {
	return strings.ReplaceAll(strings.TrimSpace(v), ",", ".")
}

func withAuthUser(req movementRequest, r *http.Request) movementRequest {
	req.UserID = userFromContext(r.Context()).ID
	req.Responsible = ""
	return req
}
