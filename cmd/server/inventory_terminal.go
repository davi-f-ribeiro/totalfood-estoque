package main

import (
	"context"
	"database/sql"
	"errors"
	"log"
	"net/http"
	"strings"
	"time"
)

var (
	errInventoryCannotComplete = errors.New("inventory cannot be completed")
	errInventoryCannotCancel   = errors.New("inventory cannot be cancelled")
	errInventoryTerminal       = errors.New("inventory is terminal")
	errInventoryNoteRequired   = errors.New("completion note required")
	errInventoryReasonInvalid  = errors.New("cancellation reason invalid")
)

const (
	inventoryReasonMin = 5
	inventoryTextMax   = 1000
)

type inventoryCompleteRequest struct {
	CompletionNote string `json:"completion_note"`
}

type inventoryCancelRequest struct {
	CancellationReason string `json:"cancellation_reason"`
}

type inventoryHistoryLine struct {
	SKUID             int64      `json:"sku_id"`
	Code              string     `json:"code"`
	Description       string     `json:"description"`
	Unit              string     `json:"unit"`
	Barcode           string     `json:"barcode,omitempty"`
	FirstQuantity     *string    `json:"first_quantity,omitempty"`
	FirstCountedAt    *time.Time `json:"first_counted_at,omitempty"`
	FirstCountedBy    string     `json:"first_counted_by,omitempty"`
	RecountQuantity   *string    `json:"recount_quantity,omitempty"`
	RecountedAt       *time.Time `json:"recounted_at,omitempty"`
	RecountedBy       string     `json:"recounted_by,omitempty"`
	ReferenceQuantity *string    `json:"reference_quantity,omitempty"`
	EffectiveQuantity *string    `json:"effective_quantity,omitempty"`
	Difference        *string    `json:"difference,omitempty"`
	State             *string    `json:"state,omitempty"`
}

type inventoryHistoryView struct {
	ID                 int64                  `json:"id"`
	Type               string                 `json:"type"`
	WarehouseID        int64                  `json:"warehouse_id"`
	Warehouse          string                 `json:"warehouse"`
	Status             string                 `json:"status"`
	ResponsibleBy      string                 `json:"responsible_by"`
	StartedAt          *time.Time             `json:"started_at,omitempty"`
	SnapshotAt         *time.Time             `json:"snapshot_at,omitempty"`
	CompletedBy        string                 `json:"completed_by,omitempty"`
	CompletedAt        *time.Time             `json:"completed_at,omitempty"`
	CompletionNote     string                 `json:"completion_note,omitempty"`
	CancelledBy        string                 `json:"cancelled_by,omitempty"`
	CancelledAt        *time.Time             `json:"cancelled_at,omitempty"`
	CancellationReason string                 `json:"cancellation_reason,omitempty"`
	ReferenceRevealed  bool                   `json:"reference_revealed"`
	Lines              []inventoryHistoryLine `json:"lines"`
}

func (a *app) registerInventoryTerminalHandlers(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/inventory-validations/{id}/complete", a.requireAuth("inventory_validation", "write", a.handleCompleteInventory))
	mux.HandleFunc("POST /api/inventory-validations/{id}/cancel", a.requireAuth("inventory_validation", "write", a.handleCancelInventory))
	mux.HandleFunc("GET /api/inventory-validations/{id}/history", a.requireAuth("inventory_validation", "read", a.handleGetInventoryHistory))
}

func normalizeAuditText(v string) string {
	return strings.TrimSpace(v)
}

func validCancellationReason(v string) bool {
	n := len([]rune(v))
	return n >= inventoryReasonMin && n <= inventoryTextMax
}

func validCompletionNote(v string, required bool) bool {
	n := len([]rune(v))
	if required {
		return n >= inventoryReasonMin && n <= inventoryTextMax
	}
	return n <= inventoryTextMax
}

func (a *app) handleCompleteInventory(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var req inventoryCompleteRequest
	if !decodeInventoryJSON(w, r, &req, true) {
		return
	}
	req.CompletionNote = normalizeAuditText(req.CompletionNote)
	out, err := a.completeInventory(r.Context(), id, userFromContext(r.Context()).ID, req.CompletionNote)
	switch {
	case err == nil:
		writeJSON(w, http.StatusOK, out)
	case errors.Is(err, errInventoryValidationNotFound):
		writeJSONError(w, http.StatusNotFound, "Inventário não encontrado.")
	case errors.Is(err, errInventoryNoteRequired):
		writeJSONError(w, http.StatusBadRequest, "Inventário divergente exige observação de conclusão válida.")
	case errors.Is(err, errInventoryCannotComplete), errors.Is(err, errInventoryTerminal):
		writeJSONError(w, http.StatusConflict, "Inventário não pode ser concluído neste estado.")
	default:
		log.Printf("inventory complete failed validation_id=%d error=%v", id, err)
		writeJSONError(w, http.StatusInternalServerError, "Erro interno.")
	}
}

func (a *app) handleCancelInventory(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var req inventoryCancelRequest
	if !decodeInventoryJSON(w, r, &req, false) {
		return
	}
	req.CancellationReason = normalizeAuditText(req.CancellationReason)
	if !validCancellationReason(req.CancellationReason) {
		writeJSONError(w, http.StatusBadRequest, "Motivo de cancelamento inválido.")
		return
	}
	out, err := a.cancelInventory(r.Context(), id, userFromContext(r.Context()).ID, req.CancellationReason)
	switch {
	case err == nil:
		writeJSON(w, http.StatusOK, out)
	case errors.Is(err, errInventoryValidationNotFound):
		writeJSONError(w, http.StatusNotFound, "Inventário não encontrado.")
	case errors.Is(err, errInventoryCannotCancel), errors.Is(err, errInventoryTerminal):
		writeJSONError(w, http.StatusConflict, "Inventário não pode ser cancelado neste estado.")
	default:
		log.Printf("inventory cancel failed validation_id=%d error=%v", id, err)
		writeJSONError(w, http.StatusInternalServerError, "Erro interno.")
	}
}

func (a *app) handleGetInventoryHistory(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	out, err := a.getInventoryHistory(r.Context(), id)
	switch {
	case err == nil:
		writeJSON(w, http.StatusOK, out)
	case errors.Is(err, errInventoryValidationNotFound):
		writeJSONError(w, http.StatusNotFound, "Inventário não encontrado.")
	case errors.Is(err, errInventoryTerminal):
		writeJSONError(w, http.StatusConflict, "Inventário ainda não está encerrado.")
	default:
		log.Printf("inventory history failed validation_id=%d error=%v", id, err)
		writeJSONError(w, http.StatusInternalServerError, "Erro interno.")
	}
}

func (a *app) completeInventory(ctx context.Context, id, userID int64, note string) (inventoryHistoryView, error) {
	tx, err := a.db.BeginTx(ctx, nil)
	if err != nil {
		return inventoryHistoryView{}, err
	}
	defer tx.Rollback()

	var status string
	var completedBy sql.NullInt64
	var existingNote sql.NullString
	if err := tx.QueryRowContext(ctx, `
SELECT status,completed_by_user_id,completion_note
FROM inventory_validations
WHERE id=$1
FOR UPDATE`, id).Scan(&status, &completedBy, &existingNote); errors.Is(err, sql.ErrNoRows) {
		return inventoryHistoryView{}, errInventoryValidationNotFound
	} else if err != nil {
		return inventoryHistoryView{}, err
	}

	if status == "COMPLETED" {
		if completedBy.Valid && completedBy.Int64 == userID && normalizeAuditText(existingNote.String) == note {
			out, err := inventoryHistoryTx(ctx, tx, id)
			if err != nil {
				return inventoryHistoryView{}, err
			}
			if err := tx.Commit(); err != nil {
				return inventoryHistoryView{}, err
			}
			return out, nil
		}
		return inventoryHistoryView{}, errInventoryTerminal
	}
	if status != "REVIEW" {
		return inventoryHistoryView{}, errInventoryCannotComplete
	}

	var pending, unrecountedDivergent, finalDivergent int
	if err := tx.QueryRowContext(ctx, `
SELECT
  count(*) FILTER (WHERE l.physical_quantity IS NULL),
  count(*) FILTER (
    WHERE l.physical_quantity IS NOT NULL
      AND l.physical_quantity <> l.system_quantity_snapshot
      AND r.validation_id IS NULL
  ),
  count(*) FILTER (
    WHERE COALESCE(r.quantity,l.physical_quantity) <> l.system_quantity_snapshot
  )
FROM inventory_validation_lines l
LEFT JOIN inventory_validation_recounts r
  ON r.validation_id=l.validation_id AND r.sku_id=l.sku_id
WHERE l.validation_id=$1`, id).Scan(&pending, &unrecountedDivergent, &finalDivergent); err != nil {
		return inventoryHistoryView{}, err
	}
	if pending != 0 || unrecountedDivergent != 0 {
		return inventoryHistoryView{}, errInventoryCannotComplete
	}
	if !validCompletionNote(note, finalDivergent > 0) {
		return inventoryHistoryView{}, errInventoryNoteRequired
	}

	if _, err := tx.ExecContext(ctx, `
UPDATE inventory_validations
SET status='COMPLETED',
    completed_by_user_id=$2,
    completed_at=statement_timestamp(),
    completion_note=NULLIF($3,'')
WHERE id=$1 AND status='REVIEW'`, id, userID, note); err != nil {
		return inventoryHistoryView{}, err
	}
	out, err := inventoryHistoryTx(ctx, tx, id)
	if err != nil {
		return inventoryHistoryView{}, err
	}
	if err := tx.Commit(); err != nil {
		return inventoryHistoryView{}, err
	}
	return out, nil
}

func (a *app) cancelInventory(ctx context.Context, id, userID int64, reason string) (inventoryHistoryView, error) {
	if !validCancellationReason(reason) {
		return inventoryHistoryView{}, errInventoryReasonInvalid
	}
	tx, err := a.db.BeginTx(ctx, nil)
	if err != nil {
		return inventoryHistoryView{}, err
	}
	defer tx.Rollback()

	var status string
	var cancelledBy sql.NullInt64
	var existingReason sql.NullString
	if err := tx.QueryRowContext(ctx, `
SELECT status,cancelled_by_user_id,cancellation_reason
FROM inventory_validations
WHERE id=$1
FOR UPDATE`, id).Scan(&status, &cancelledBy, &existingReason); errors.Is(err, sql.ErrNoRows) {
		return inventoryHistoryView{}, errInventoryValidationNotFound
	} else if err != nil {
		return inventoryHistoryView{}, err
	}

	if status == "CANCELLED" {
		if cancelledBy.Valid && cancelledBy.Int64 == userID && existingReason.String == reason {
			out, err := inventoryHistoryTx(ctx, tx, id)
			if err != nil {
				return inventoryHistoryView{}, err
			}
			if err := tx.Commit(); err != nil {
				return inventoryHistoryView{}, err
			}
			return out, nil
		}
		return inventoryHistoryView{}, errInventoryTerminal
	}
	if status != "OPEN" && status != "COUNTING" && status != "REVIEW" {
		return inventoryHistoryView{}, errInventoryCannotCancel
	}
	if _, err := tx.ExecContext(ctx, `
UPDATE inventory_validations
SET status='CANCELLED',
    cancelled_by_user_id=$2,
    cancelled_at=statement_timestamp(),
    cancellation_reason=$3
WHERE id=$1 AND status IN ('OPEN','COUNTING','REVIEW')`, id, userID, reason); err != nil {
		return inventoryHistoryView{}, err
	}
	out, err := inventoryHistoryTx(ctx, tx, id)
	if err != nil {
		return inventoryHistoryView{}, err
	}
	if err := tx.Commit(); err != nil {
		return inventoryHistoryView{}, err
	}
	return out, nil
}

func (a *app) getInventoryHistory(ctx context.Context, id int64) (inventoryHistoryView, error) {
	tx, err := a.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return inventoryHistoryView{}, err
	}
	defer tx.Rollback()
	out, err := inventoryHistoryTx(ctx, tx, id)
	if err != nil {
		return inventoryHistoryView{}, err
	}
	if err := tx.Commit(); err != nil {
		return inventoryHistoryView{}, err
	}
	return out, nil
}

func inventoryHistoryTx(ctx context.Context, tx *sql.Tx, id int64) (inventoryHistoryView, error) {
	var out inventoryHistoryView
	var startedAt, snapshotAt, completedAt, cancelledAt sql.NullTime
	var completionNote, cancellationReason sql.NullString
	if err := tx.QueryRowContext(ctx, `
SELECT v.id,v.type,v.warehouse_id,w.name,v.status,
       COALESCE(ru.login,''),
       v.started_at,v.snapshot_at,
       COALESCE(cu.login,''),v.completed_at,v.completion_note,
       COALESCE(xu.login,''),v.cancelled_at,v.cancellation_reason
FROM inventory_validations v
JOIN warehouses w ON w.id=v.warehouse_id
LEFT JOIN users ru ON ru.id=v.responsible_user_id
LEFT JOIN users cu ON cu.id=v.completed_by_user_id
LEFT JOIN users xu ON xu.id=v.cancelled_by_user_id
WHERE v.id=$1`, id).Scan(
		&out.ID,&out.Type,&out.WarehouseID,&out.Warehouse,&out.Status,
		&out.ResponsibleBy,&startedAt,&snapshotAt,
		&out.CompletedBy,&completedAt,&completionNote,
		&out.CancelledBy,&cancelledAt,&cancellationReason,
	); errors.Is(err, sql.ErrNoRows) {
		return inventoryHistoryView{}, errInventoryValidationNotFound
	} else if err != nil {
		return inventoryHistoryView{}, err
	}
	if out.Status != "COMPLETED" && out.Status != "CANCELLED" {
		return inventoryHistoryView{}, errInventoryTerminal
	}
	if startedAt.Valid { t:=startedAt.Time; out.StartedAt=&t }
	if snapshotAt.Valid { t:=snapshotAt.Time; out.SnapshotAt=&t }
	if completedAt.Valid { t:=completedAt.Time; out.CompletedAt=&t }
	if cancelledAt.Valid { t:=cancelledAt.Time; out.CancelledAt=&t }
	if completionNote.Valid { out.CompletionNote=completionNote.String }
	if cancellationReason.Valid { out.CancellationReason=cancellationReason.String }

	var pending int
	if err := tx.QueryRowContext(ctx, `
SELECT count(*) FROM inventory_validation_lines
WHERE validation_id=$1 AND physical_quantity IS NULL`, id).Scan(&pending); err != nil {
		return inventoryHistoryView{}, err
	}
	out.ReferenceRevealed = !(out.Type=="BLIND" && out.Status=="CANCELLED" && pending>0)

	rows, err := tx.QueryContext(ctx, `
SELECT l.sku_id,
       COALESCE(NULLIF(s.name,''),NULLIF(s.brand,''),s.id::text),
       i.name,i.unit_code,COALESCE(s.barcode,''),
       l.physical_quantity::text,l.counted_at,COALESCE(cu.login,''),
       r.quantity::text,r.recounted_at,COALESCE(ru.login,''),
       l.system_quantity_snapshot::text,
       COALESCE(r.quantity,l.physical_quantity)::text,
       (COALESCE(r.quantity,l.physical_quantity)-l.system_quantity_snapshot)::text,
       CASE WHEN COALESCE(r.quantity,l.physical_quantity)=l.system_quantity_snapshot THEN 'MATCH' ELSE 'DIVERGENT' END
FROM inventory_validation_lines l
JOIN product_skus s ON s.id=l.sku_id
JOIN inventory_items i ON i.id=s.item_id
LEFT JOIN users cu ON cu.id=l.counted_by_user_id
LEFT JOIN inventory_validation_recounts r ON r.validation_id=l.validation_id AND r.sku_id=l.sku_id
LEFT JOIN users ru ON ru.id=r.recounted_by_user_id
WHERE l.validation_id=$1
ORDER BY i.name,l.sku_id`, id)
	if err != nil {
		return inventoryHistoryView{}, err
	}
	defer rows.Close()
	out.Lines=[]inventoryHistoryLine{}
	for rows.Next() {
		var line inventoryHistoryLine
		var firstQty,recountQty,reference,effective,difference,state sql.NullString
		var firstAt,recountAt sql.NullTime
		if err := rows.Scan(
			&line.SKUID,&line.Code,&line.Description,&line.Unit,&line.Barcode,
			&firstQty,&firstAt,&line.FirstCountedBy,
			&recountQty,&recountAt,&line.RecountedBy,
			&reference,&effective,&difference,&state,
		); err != nil {
			return inventoryHistoryView{}, err
		}
		if firstQty.Valid { q:=firstQty.String; line.FirstQuantity=&q }
		if firstAt.Valid { t:=firstAt.Time; line.FirstCountedAt=&t }
		if recountQty.Valid { q:=recountQty.String; line.RecountQuantity=&q }
		if recountAt.Valid { t:=recountAt.Time; line.RecountedAt=&t }
		if out.ReferenceRevealed {
			if reference.Valid { q:=reference.String; line.ReferenceQuantity=&q }
			if effective.Valid { q:=effective.String; line.EffectiveQuantity=&q }
			if difference.Valid { q:=difference.String; line.Difference=&q }
			if state.Valid { q:=state.String; line.State=&q }
		}
		out.Lines=append(out.Lines,line)
	}
	return out, rows.Err()
}
