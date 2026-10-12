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
	"net/http"
	"sort"
	"strings"
)

// Lote: vários produtos em um único envio, gravados juntos ou nenhum.
// Cada linha vira um lançamento próprio com operation_id "<lote>:<n>", o que
// preserva o histórico e os relatórios existentes e mantém o envio repetível.

const maxBatchLines = 100

var errDuplicateBatchSKU = errors.New("duplicate sku in batch")

type batchLineRequest struct {
	SKUID    int64  `json:"sku_id"`
	Quantity string `json:"quantity"`
	UnitCost string `json:"unit_cost"`
}

type batchRequest struct {
	OperationID       string             `json:"operation_id"`
	WarehouseID       int64              `json:"warehouse_id"`
	FromWarehouseID   int64              `json:"warehouse_from_id"`
	ToWarehouseID     int64              `json:"warehouse_to_id"`
	QuantitySemantics string             `json:"quantity_semantics,omitempty"`
	DocumentNumber    string             `json:"document_number"`
	SupplierName      string             `json:"supplier_name"`
	Notes             string             `json:"notes"`
	Lines             []batchLineRequest `json:"lines"`
}

type batchResult struct {
	OperationID string  `json:"operation_id"`
	Status      string  `json:"status"`
	Type        string  `json:"type"`
	Count       int     `json:"count"`
	MovementIDs []int64 `json:"movement_ids"`
}

// batchLineError aponta qual linha do lote causou a recusa.
type batchLineError struct {
	Index int // posição na lista enviada, começando em 0
	Err   error
}

func (e batchLineError) Error() string { return fmt.Sprintf("line %d: %v", e.Index, e.Err) }
func (e batchLineError) Unwrap() error { return e.Err }

func (a *app) handleBatch(typ string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
		defer r.Body.Close()
		dec := json.NewDecoder(r.Body)
		dec.DisallowUnknownFields()
		var req batchRequest
		if err := dec.Decode(&req); err != nil {
			writeJSONError(w, http.StatusBadRequest, "Dados inválidos. Confira os campos e tente de novo.")
			return
		}
		req.OperationID = strings.TrimSpace(req.OperationID)
		req.QuantitySemantics = strings.TrimSpace(req.QuantitySemantics)
		if req.OperationID == "" || strings.Contains(req.OperationID, ":") {
			writeJSONError(w, http.StatusBadRequest, "Não foi possível identificar este envio. Atualize a página e tente de novo.")
			return
		}
		if req.QuantitySemantics != "" && req.QuantitySemantics != presentationQuantitySemantics {
			writeJSONError(w, http.StatusBadRequest, "Dados inválidos. Confira os campos e tente de novo.")
			return
		}
		if len(req.Lines) == 0 {
			writeJSONError(w, http.StatusBadRequest, "Adicione ao menos um item ao lote.")
			return
		}
		if len(req.Lines) > maxBatchLines {
			writeJSONError(w, http.StatusBadRequest, fmt.Sprintf("Um lote aceita até %d itens.", maxBatchLines))
			return
		}
		res, err := a.createBatch(r.Context(), typ, req, userFromContext(r.Context()).ID)
		if err == nil {
			writeJSON(w, http.StatusOK, res)
			return
		}
		var le batchLineError
		if errors.As(err, &le) {
			a.writeBatchLineError(w, req.OperationID, typ, le)
			return
		}
		a.writeOperationResponse(w, typ, req.OperationID, operationResult{}, err)
	}
}

func (a *app) writeBatchLineError(w http.ResponseWriter, opID, typ string, le batchLineError) {
	n := le.Index + 1
	status := http.StatusBadRequest
	msg := ""
	switch {
	case errors.Is(le.Err, errMovementProductRequired):
		msg = fmt.Sprintf("Item %d: escolha um produto.", n)
	case errors.Is(le.Err, errMovementQuantityInvalid):
		msg = fmt.Sprintf("Item %d: digite uma quantidade maior que zero, com até 4 casas decimais.", n)
	case errors.Is(le.Err, errMovementCostInvalid):
		msg = fmt.Sprintf("Item %d: digite um preço válido, de zero ou mais, com até 4 casas decimais.", n)
	case errors.Is(le.Err, errDuplicateBatchSKU):
		msg = fmt.Sprintf("Item %d: este produto já está no lote.", n)
	case errors.Is(le.Err, errInsufficientStock):
		status = http.StatusConflict
		msg = fmt.Sprintf("Item %d: estoque insuficiente.", n)
		var e insufficientStockError
		if errors.As(le.Err, &e) && e.Available != "" && e.Unit != "" {
			msg = fmt.Sprintf("Item %d: estoque insuficiente. Só há %s %s.", n, e.Available, e.Unit)
		}
	case errors.Is(le.Err, errNotFound):
		status = http.StatusNotFound
		msg = fmt.Sprintf("Item %d: produto não encontrado.", n)
	default:
		// Erros sem texto próprio seguem o tratamento comum (depósito, conflito etc.).
		a.writeOperationResponse(w, typ, opID, operationResult{}, le.Err)
		return
	}
	writeJSON(w, status, map[string]any{"error": msg, "line_index": le.Index})
}

func lineOperationID(batchID string, n int) string { return fmt.Sprintf("%s:%d", batchID, n) }

func (a *app) createBatch(ctx context.Context, typ string, req batchRequest, userID int64) (batchResult, error) {
	movementType := map[string]string{"entry": "ENTRADA", "exit": "SAIDA", "transfer": "TRANSFERENCIA"}[typ]
	seen := make(map[int64]struct{}, len(req.Lines))
	for i := range req.Lines {
		l := &req.Lines[i]
		l.Quantity = normalizeDecimalInput(l.Quantity)
		l.UnitCost = normalizeDecimalInput(l.UnitCost)
		if l.SKUID <= 0 {
			return batchResult{}, batchLineError{i, errMovementProductRequired}
		}
		if !positiveDecimal(l.Quantity) {
			return batchResult{}, batchLineError{i, errMovementQuantityInvalid}
		}
		if typ == "entry" && !nonNegativeDecimal(l.UnitCost) {
			return batchResult{}, batchLineError{i, errMovementCostInvalid}
		}
		if _, dup := seen[l.SKUID]; dup {
			return batchResult{}, batchLineError{i, errDuplicateBatchSKU}
		}
		seen[l.SKUID] = struct{}{}
	}
	switch typ {
	case "entry", "exit":
		if req.WarehouseID <= 0 {
			return batchResult{}, errMovementWarehouseRequired
		}
	case "transfer":
		if req.FromWarehouseID <= 0 || req.ToWarehouseID <= 0 {
			return batchResult{}, errTransferWarehousesRequired
		}
		if req.FromWarehouseID == req.ToWarehouseID {
			return batchResult{}, errTransferSameWarehouse
		}
	}

	// Ordem fixa por produto evita impasse entre dois lotes simultâneos.
	order := make([]int, len(req.Lines))
	for i := range order {
		order[i] = i
	}
	sort.Slice(order, func(x, y int) bool { return req.Lines[order[x]].SKUID < req.Lines[order[y]].SKUID })

	ids := make([]int64, len(req.Lines))
	_, err := a.inMovementTx(ctx, func(tx *sql.Tx) (operationResult, error) {
		for _, i := range order {
			l := req.Lines[i]
			mr := movementRequest{
				OperationID:       lineOperationID(req.OperationID, i+1),
				SKUID:             l.SKUID,
				WarehouseID:       req.WarehouseID,
				FromWarehouseID:   req.FromWarehouseID,
				ToWarehouseID:     req.ToWarehouseID,
				Quantity:          l.Quantity,
				QuantitySemantics: req.QuantitySemantics,
				UnitCost:          l.UnitCost,
				DocumentNumber:    strings.TrimSpace(req.DocumentNumber),
				SupplierName:      strings.TrimSpace(req.SupplierName),
				Notes:             strings.TrimSpace(req.Notes),
				UserID:            userID,
			}
			body, _ := json.Marshal(mr)
			sum := sha256.Sum256(body)
			hash := hex.EncodeToString(sum[:])
			var res operationResult
			var err error
			switch typ {
			case "entry":
				res, err = createEntryTx(ctx, tx, mr, hash)
			case "exit":
				res, err = createExitTx(ctx, tx, mr, hash)
			default:
				res, err = createTransferTx(ctx, tx, mr, hash)
			}
			if err != nil {
				return operationResult{}, batchLineError{i, err}
			}
			ids[i] = res.MovementID
		}
		// Reenvio com menos itens que o original não pode passar como idêntico.
		var extra bool
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM movements WHERE operation_id=$1)`, lineOperationID(req.OperationID, len(req.Lines)+1)).Scan(&extra); err != nil {
			return operationResult{}, err
		}
		if extra {
			return operationResult{}, errOperationConflict
		}
		return operationResult{}, nil
	})
	if err != nil {
		var le batchLineError
		if !errors.As(err, &le) {
			log.Printf("operation_id=%s type=%s stage=batch error=%v", req.OperationID, movementType, err)
		}
		return batchResult{}, err
	}
	return batchResult{OperationID: req.OperationID, Status: "confirmed", Type: movementType, Count: len(ids), MovementIDs: ids}, nil
}
