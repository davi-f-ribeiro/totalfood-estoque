package main

import (
	"errors"

	"github.com/jackc/pgx/v5/pgconn"
)

// Erros do PostgreSQL identificados pelo código SQLSTATE e pelo nome exato da
// constraint, nunca pelo texto da mensagem: o texto muda com a versão e o
// idioma do servidor, e um trecho como "assets_tag" casava tanto a unicidade
// (assets_tag_key) quanto a validação (assets_tag_check).
// TestMatchedConstraintNamesExistInSchema confere que cada nome usado em
// violates() existe no esquema.

func pgError(err error) *pgconn.PgError {
	var pg *pgconn.PgError
	if errors.As(err, &pg) {
		return pg
	}
	return nil
}

// violates informa se err é a violação da constraint ou do índice único com
// exatamente esse nome (unicidade, chave estrangeira, CHECK ou exclusão).
func violates(err error, name string) bool {
	pg := pgError(err)
	return pg != nil && len(pg.Code) == 5 && pg.Code[:2] == "23" && pg.ConstraintName == name
}

// isForeignKeyViolation informa se err é qualquer violação de chave estrangeira.
func isForeignKeyViolation(err error) bool {
	pg := pgError(err)
	return pg != nil && pg.Code == "23503"
}

// raised informa se err veio de um RAISE EXCEPTION com exatamente essa mensagem
// (funções e gatilhos das migrations).
func raised(err error, message string) bool {
	pg := pgError(err)
	return pg != nil && pg.Code == "P0001" && pg.Message == message
}

// isInvalidDate informa se o banco recusou uma data mal formada ou fora da faixa.
func isInvalidDate(err error) bool {
	pg := pgError(err)
	return pg != nil && (pg.Code == "22007" || pg.Code == "22008")
}
