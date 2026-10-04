package main

import (
	"errors"
	"log"
	"net/http"
	"strconv"
	"strings"
)

type inventoryAdjustmentRequest struct {
	OperationID string `json:"operation_id"`
}

func (a *app) registerInventoryAdjustmentHandlers(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/inventory-validations/{id}/lines/{sku_id}/adjust",
		a.requireAuth("stock_adjustment", "write", a.handleInventoryStockAdjustment))
}

func (a *app) handleInventoryStockAdjustment(w http.ResponseWriter, r *http.Request) {
	validationID, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || validationID <= 0 {
		writeJSONError(w, http.StatusBadRequest, "Inventário inválido.")
		return
	}
	skuID, err := strconv.ParseInt(r.PathValue("sku_id"), 10, 64)
	if err != nil || skuID <= 0 {
		writeJSONError(w, http.StatusBadRequest, "Produto inválido.")
		return
	}
	var req inventoryAdjustmentRequest
	if !decodeInventoryJSON(w, r, &req, false) {
		return
	}
	req.OperationID = strings.TrimSpace(req.OperationID)
	if req.OperationID == "" {
		writeJSONError(w, http.StatusBadRequest, "Não foi possível identificar este ajuste. Recarregue a página antes de tentar novamente.")
		return
	}

	res, err := a.applyInventoryStockAdjustment(r.Context(), validationID, skuID, userFromContext(r.Context()).ID, req.OperationID)
	switch {
	case err == nil:
		writeJSON(w, http.StatusOK, res)
	case errors.Is(err, errInventoryValidationNotFound), errors.Is(err, errInventoryLineNotFound):
		writeJSONError(w, http.StatusNotFound, "Item da auditoria não encontrado.")
	case errors.Is(err, errInventoryAdjustmentNotEligible):
		writeJSONError(w, http.StatusConflict, "Este item não possui divergência concluída disponível para ajuste.")
	case errors.Is(err, errInventoryAdjustmentAlreadyApplied):
		writeJSONError(w, http.StatusConflict, "Esta divergência já foi ajustada.")
	case errors.Is(err, errInventoryAdjustmentCostUnavailable):
		writeJSONError(w, http.StatusConflict, "Não há custo válido para registrar este ajuste.")
	case errors.Is(err, errInsufficientStock):
		writeJSONError(w, http.StatusConflict, "O saldo atual não permite aplicar esta redução de estoque.")
	case errors.Is(err, errOperationConflict):
		writeJSONError(w, http.StatusConflict, "Esta identificação de operação já foi usada em outro ajuste.")
	default:
		log.Printf("inventory adjustment failed validation_id=%d sku_id=%d operation_id=%s error=%v", validationID, skuID, req.OperationID, err)
		writeJSONError(w, http.StatusInternalServerError, "Erro interno.")
	}
}
