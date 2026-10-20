package main

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
)

// Criação idempotente de item, marca e produto.
//
// Reaproveita o contrato das movimentações (writeMovementTx) e das contagens
// (inventorySelectionByOperationTx): operation_id + request_hash gravados no
// próprio registro. O mesmo envio devolve o cadastro original; o mesmo envio
// com outros dados é recusado. Edição (PATCH) continua com a concorrência
// otimista por versão e não usa este contrato.

const (
	catalogOperationMissingMessage  = "Não foi possível identificar este envio. Atualize a página e tente de novo."
	catalogOperationConflictMessage = "Este cadastro já foi enviado com outros dados. Confira a lista antes de cadastrar de novo."
)

// catalogDomainError é uma recusa de regra de negócio dentro da transação de
// criação. A transação é desfeita, então o envio não fica consumido.
type catalogDomainError struct {
	status int
	msg    string
}

func (e catalogDomainError) Error() string { return e.msg }

type catalogCreateResult struct {
	ID       int64
	Version  int64
	Replayed bool
}

// Consultas fixas por tabela: o nome da tabela nunca vem da requisição.
var catalogOperationLookup = map[string]string{
	"inventory_items": `SELECT id, request_hash, version FROM inventory_items WHERE operation_id=$1`,
	"brands":          `SELECT id, request_hash, version FROM brands WHERE operation_id=$1`,
	"product_skus":    `SELECT id, request_hash, version FROM product_skus WHERE operation_id=$1`,
}

func catalogOperationID(raw string) string { return strings.TrimSpace(raw) }

// catalogRequestHash identifica o conteúdo do envio e quem o fez, como nas
// movimentações: outro operador com o mesmo identificador recebe conflito.
func catalogRequestHash(userID int64, table string, req any) string {
	body, _ := json.Marshal(struct {
		UserID int64  `json:"user_id"`
		Table  string `json:"table"`
		Req    any    `json:"req"`
	}{userID, table, req})
	h := sha256.Sum256(body)
	return hex.EncodeToString(h[:])
}

// createCatalogRecord serializa envios com o mesmo identificador, devolve o
// registro já gravado quando o envio se repete e, se ainda não existe, chama
// insert dentro da mesma transação.
func (a *app) createCatalogRecord(ctx context.Context, table, operationID, hash string, insert func(*sql.Tx) (int64, error)) (catalogCreateResult, error) {
	lookup, ok := catalogOperationLookup[table]
	if !ok {
		return catalogCreateResult{}, errors.New("catalog create: unsupported table " + table)
	}
	tx, err := a.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	if err != nil {
		return catalogCreateResult{}, err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtext($1))`, "catalog-create:"+table+":"+operationID); err != nil {
		return catalogCreateResult{}, err
	}
	var existingID, existingVersion int64
	var existingHash string
	err = tx.QueryRowContext(ctx, lookup, operationID).Scan(&existingID, &existingHash, &existingVersion)
	if err == nil {
		if existingHash != hash {
			return catalogCreateResult{}, errOperationConflict
		}
		return catalogCreateResult{ID: existingID, Version: existingVersion, Replayed: true}, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return catalogCreateResult{}, err
	}
	id, err := insert(tx)
	if err != nil {
		return catalogCreateResult{}, err
	}
	if err := tx.Commit(); err != nil {
		return catalogCreateResult{}, err
	}
	return catalogCreateResult{ID: id, Version: 1}, nil
}

// writeCatalogCreateResult responde 201 na criação e 200 quando o envio já
// estava gravado, com o mesmo corpo nos dois casos.
func writeCatalogCreateResult(w http.ResponseWriter, res catalogCreateResult) {
	status := http.StatusCreated
	body := map[string]any{"id": res.ID, "version": res.Version}
	if res.Replayed {
		status = http.StatusOK
		body["replayed"] = true
	}
	writeJSON(w, status, body)
}

// writeCatalogCreateError trata os erros comuns da criação e devolve false
// quando o erro é do banco e deve seguir para o tradutor de cada cadastro.
func writeCatalogCreateError(w http.ResponseWriter, err error) bool {
	var domain catalogDomainError
	switch {
	case errors.As(err, &domain):
		writeJSONError(w, domain.status, domain.msg)
	case errors.Is(err, errOperationConflict):
		writeJSONError(w, http.StatusConflict, catalogOperationConflictMessage)
	default:
		return false
	}
	return true
}
