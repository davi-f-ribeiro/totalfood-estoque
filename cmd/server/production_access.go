package main

import (
	"context"
	"database/sql"
	"strings"

	"golang.org/x/crypto/bcrypt"
)

type productionAccessResetResult struct {
	UsersBefore          int
	AuthenticatableAfter int
	Strategy             string
}

func (a *app) resetProductionAccess(ctx context.Context, initialPassword string) (productionAccessResetResult, error) {
	if strings.TrimSpace(initialPassword) == "" {
		return productionAccessResetResult{}, errValidation
	}
	tx, err := a.db.BeginTx(ctx, &sql.TxOptions{})
	if err != nil {
		return productionAccessResetResult{}, err
	}
	defer tx.Rollback()
	var res productionAccessResetResult
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM users`).Scan(&res.UsersBefore); err != nil {
		return res, err
	}
	var profileID int64
	if err := tx.QueryRowContext(ctx, `INSERT INTO profiles (name) VALUES ('Diretor') ON CONFLICT (name) DO UPDATE SET name=EXCLUDED.name RETURNING id`).Scan(&profileID); err != nil {
		return res, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO profile_permissions (profile_id, permission_id) SELECT $1,id FROM permissions ON CONFLICT DO NOTHING`, profileID); err != nil {
		return res, err
	}
	h, err := bcrypt.GenerateFromPassword([]byte(initialPassword), bcryptCost)
	if err != nil {
		return res, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE users SET active=false WHERE login <> 'diretor'`); err != nil {
		return res, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO users (login,password_hash,profile_id,active,must_change_password) VALUES ('diretor',$1,$2,true,true) ON CONFLICT (login) DO UPDATE SET password_hash=EXCLUDED.password_hash, profile_id=EXCLUDED.profile_id, active=true, must_change_password=true`, string(h), profileID); err != nil {
		return res, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE sessions SET revoked_at=now() WHERE revoked_at IS NULL`); err != nil {
		return res, err
	}
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM users WHERE active=true`).Scan(&res.AuthenticatableAfter); err != nil {
		return res, err
	}
	res.Strategy = "desativação"
	return res, tx.Commit()
}
