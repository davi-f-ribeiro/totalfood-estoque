package main

import (
	"database/sql"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"strings"

	"golang.org/x/crypto/bcrypt"
)

type adminUserRequest struct {
	Login     string `json:"login"`
	ProfileID int64  `json:"profile_id"`
	Active    *bool  `json:"active"`
}

type adminCreateUserRequest struct {
	Login     string `json:"login"`
	Password  string `json:"password"`
	ProfileID int64  `json:"profile_id"`
	Active    *bool  `json:"active"`
}

type adminPasswordRequest struct {
	Password string `json:"password"`
}

type adminProfileRequest struct {
	Name string `json:"name"`
}

type adminProfilePermissionsRequest struct {
	PermissionIDs []int64 `json:"permission_ids"`
}

func (a *app) registerAdminHandlers(mux *http.ServeMux) {
	mux.HandleFunc("GET /admin", a.uiPage("admin", "Administração", "users", "write"))
	mux.HandleFunc("GET /api/admin/users", a.requireAuth("users", "write", a.handleAdminUsers))
	mux.HandleFunc("POST /api/admin/users", a.requireAuth("users", "write", a.handleAdminCreateUser))
	mux.HandleFunc("PATCH /api/admin/users/{id}", a.requireAuth("users", "write", a.handleAdminUpdateUser))
	mux.HandleFunc("PATCH /api/admin/users/{id}/password", a.requireAuth("users", "write", a.handleAdminResetPassword))
	mux.HandleFunc("GET /api/admin/profiles", a.requireAuth("profiles", "write", a.handleAdminProfiles))
	mux.HandleFunc("POST /api/admin/profiles", a.requireAuth("profiles", "write", a.handleAdminCreateProfile))
	mux.HandleFunc("PATCH /api/admin/profiles/{id}", a.requireAuth("profiles", "write", a.handleAdminUpdateProfile))
	mux.HandleFunc("PUT /api/admin/profiles/{id}/permissions", a.requireAuth("profiles", "write", a.handleAdminProfilePermissions))
	mux.HandleFunc("GET /api/admin/permissions", a.requireAuth("profiles", "write", a.handleAdminPermissions))
}

func (a *app) handleAdminUsers(w http.ResponseWriter, r *http.Request) {
	rows, err := a.db.QueryContext(r.Context(), `SELECT u.id,u.login,u.profile_id,p.name,u.active,u.created_at::text FROM users u JOIN profiles p ON p.id=u.profile_id ORDER BY u.login`)
	if err != nil {
		writeJSONError(w, 500, "internal error")
		return
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var id, pid int64
		var login, pname, created string
		var active bool
		if err := rows.Scan(&id, &login, &pid, &pname, &active, &created); err != nil {
			writeJSONError(w, 500, "internal error")
			return
		}
		out = append(out, map[string]any{"id": id, "login": login, "profile_id": pid, "profile": pname, "active": active, "created_at": created})
	}
	writeJSON(w, 200, out)
}

func (a *app) handleAdminCreateUser(w http.ResponseWriter, r *http.Request) {
	var req adminCreateUserRequest
	if !decodeAdminJSON(w, r, &req) {
		return
	}
	login := strings.TrimSpace(req.Login)
	if login == "" || req.Password == "" || req.ProfileID <= 0 {
		writeJSONError(w, 400, "invalid request")
		return
	}
	active := true
	if req.Active != nil {
		active = *req.Active
	}
	if !a.profileExists(r, req.ProfileID) {
		writeJSONError(w, 400, "invalid profile")
		return
	}
	h, err := bcrypt.GenerateFromPassword([]byte(req.Password), bcryptCost)
	if err != nil {
		writeJSONError(w, 500, "internal error")
		return
	}
	var id int64
	err = a.db.QueryRowContext(r.Context(), `INSERT INTO users (login,password_hash,profile_id,active) VALUES ($1,$2,$3,$4) RETURNING id`, login, string(h), req.ProfileID, active).Scan(&id)
	if err != nil {
		log.Printf("admin user create failed: %v", err)
		writeJSONError(w, 409, "user not created")
		return
	}
	writeJSON(w, 201, map[string]any{"id": id, "login": login, "profile_id": req.ProfileID, "active": active})
}

func (a *app) handleAdminUpdateUser(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var req adminUserRequest
	if !decodeAdminJSON(w, r, &req) {
		return
	}
	login := strings.TrimSpace(req.Login)
	if login == "" || req.ProfileID <= 0 {
		writeJSONError(w, 400, "invalid request")
		return
	}
	active := true
	if req.Active != nil {
		active = *req.Active
	}
	if !a.profileExists(r, req.ProfileID) {
		writeJSONError(w, 400, "invalid profile")
		return
	}
	u := userFromContext(r.Context())
	if id == u.ID && !active {
		writeJSONError(w, 409, "não é permitido desativar o próprio administrador")
		return
	}
	newAdmin, err := a.profileHasAdmin(r, req.ProfileID)
	if err != nil {
		writeJSONError(w, 500, "internal error")
		return
	}
	if id == u.ID && !newAdmin {
		writeJSONError(w, 409, "não é permitido remover o próprio acesso administrativo")
		return
	}
	oldAdmin, err := a.userIsAdmin(r, id)
	if err != nil {
		writeJSONError(w, 500, "internal error")
		return
	}
	if oldAdmin && (!active || !newAdmin) {
		cnt, err := a.activeAdminCountExcluding(r, id)
		if err != nil {
			writeJSONError(w, 500, "internal error")
			return
		}
		if cnt == 0 {
			writeJSONError(w, 409, "não é permitido remover o último administrador ativo")
			return
		}
	}
	res, err := a.db.ExecContext(r.Context(), `UPDATE users SET login=$1, profile_id=$2, active=$3 WHERE id=$4`, login, req.ProfileID, active, id)
	if err != nil {
		log.Printf("admin user update failed user_id=%d error=%v", id, err)
		writeJSONError(w, 409, "user not updated")
		return
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		writeJSONError(w, 404, "user not found")
		return
	}
	_, _ = a.db.ExecContext(r.Context(), `UPDATE sessions SET revoked_at=now() WHERE user_id=$1 AND revoked_at IS NULL`, id)
	writeJSON(w, 200, map[string]any{"status": "ok"})
}

func (a *app) handleAdminResetPassword(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var req adminPasswordRequest
	if !decodeAdminJSON(w, r, &req) {
		return
	}
	if req.Password == "" {
		writeJSONError(w, 400, "invalid request")
		return
	}
	h, err := bcrypt.GenerateFromPassword([]byte(req.Password), bcryptCost)
	if err != nil {
		writeJSONError(w, 500, "internal error")
		return
	}
	res, err := a.db.ExecContext(r.Context(), `UPDATE users SET password_hash=$1 WHERE id=$2`, string(h), id)
	if err != nil {
		log.Printf("admin password reset failed user_id=%d error=%v", id, err)
		writeJSONError(w, 500, "internal error")
		return
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		writeJSONError(w, 404, "user not found")
		return
	}
	_, _ = a.db.ExecContext(r.Context(), `UPDATE sessions SET revoked_at=now() WHERE user_id=$1 AND revoked_at IS NULL`, id)
	writeJSON(w, 200, map[string]any{"status": "ok"})
}

func (a *app) handleAdminProfiles(w http.ResponseWriter, r *http.Request) {
	rows, err := a.db.QueryContext(r.Context(), `SELECT id,name FROM profiles ORDER BY name`)
	if err != nil {
		writeJSONError(w, 500, "internal error")
		return
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var id int64
		var name string
		rows.Scan(&id, &name)
		prows, err := a.db.QueryContext(r.Context(), `SELECT permission_id FROM profile_permissions WHERE profile_id=$1 ORDER BY permission_id`, id)
		if err != nil {
			writeJSONError(w, 500, "internal error")
			return
		}
		ids := []int64{}
		for prows.Next() {
			var pid int64
			prows.Scan(&pid)
			ids = append(ids, pid)
		}
		prows.Close()
		out = append(out, map[string]any{"id": id, "name": name, "permission_ids": ids})
	}
	writeJSON(w, 200, out)
}

func (a *app) handleAdminCreateProfile(w http.ResponseWriter, r *http.Request) {
	var req adminProfileRequest
	if !decodeAdminJSON(w, r, &req) {
		return
	}
	name := strings.TrimSpace(req.Name)
	if name == "" {
		writeJSONError(w, 400, "invalid request")
		return
	}
	var id int64
	if err := a.db.QueryRowContext(r.Context(), `INSERT INTO profiles (name) VALUES ($1) RETURNING id`, name).Scan(&id); err != nil {
		writeJSONError(w, 409, "profile not created")
		return
	}
	writeJSON(w, 201, map[string]any{"id": id, "name": name})
}

func (a *app) handleAdminUpdateProfile(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var old string
	if err := a.db.QueryRowContext(r.Context(), `SELECT name FROM profiles WHERE id=$1`, id).Scan(&old); errors.Is(err, sql.ErrNoRows) {
		writeJSONError(w, 404, "profile not found")
		return
	} else if err != nil {
		writeJSONError(w, 500, "internal error")
		return
	}
	if old == "Diretor" {
		writeJSONError(w, 409, "perfil Diretor não pode ser renomeado")
		return
	}
	var req adminProfileRequest
	if !decodeAdminJSON(w, r, &req) {
		return
	}
	name := strings.TrimSpace(req.Name)
	if name == "" {
		writeJSONError(w, 400, "invalid request")
		return
	}
	if _, err := a.db.ExecContext(r.Context(), `UPDATE profiles SET name=$1 WHERE id=$2`, name, id); err != nil {
		writeJSONError(w, 409, "profile not updated")
		return
	}
	writeJSON(w, 200, map[string]any{"status": "ok"})
}

func (a *app) handleAdminPermissions(w http.ResponseWriter, r *http.Request) {
	rows, err := a.db.QueryContext(r.Context(), `SELECT id,resource,action FROM permissions ORDER BY resource,action`)
	if err != nil {
		writeJSONError(w, 500, "internal error")
		return
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var id int64
		var res, act string
		rows.Scan(&id, &res, &act)
		out = append(out, map[string]any{"id": id, "resource": res, "action": act})
	}
	writeJSON(w, 200, out)
}

func (a *app) handleAdminProfilePermissions(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var name string
	if err := a.db.QueryRowContext(r.Context(), `SELECT name FROM profiles WHERE id=$1`, id).Scan(&name); errors.Is(err, sql.ErrNoRows) {
		writeJSONError(w, 404, "profile not found")
		return
	} else if err != nil {
		writeJSONError(w, 500, "internal error")
		return
	}
	if name == "Diretor" {
		writeJSONError(w, 409, "permissões do Diretor são completas")
		return
	}
	var req adminProfilePermissionsRequest
	if !decodeAdminJSON(w, r, &req) {
		return
	}
	tx, err := a.db.BeginTx(r.Context(), nil)
	if err != nil {
		writeJSONError(w, 500, "internal error")
		return
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(r.Context(), `DELETE FROM profile_permissions WHERE profile_id=$1`, id); err != nil {
		writeJSONError(w, 500, "internal error")
		return
	}
	for _, pid := range req.PermissionIDs {
		if _, err = tx.ExecContext(r.Context(), `INSERT INTO profile_permissions (profile_id, permission_id) SELECT $1,id FROM permissions WHERE id=$2`, id, pid); err != nil {
			writeJSONError(w, 400, "invalid permission")
			return
		}
	}
	if err = tx.Commit(); err != nil {
		writeJSONError(w, 500, "internal error")
		return
	}
	_, _ = a.db.ExecContext(r.Context(), `UPDATE sessions SET revoked_at=now() WHERE user_id IN (SELECT id FROM users WHERE profile_id=$1) AND revoked_at IS NULL`, id)
	writeJSON(w, 200, map[string]any{"status": "ok"})
}

func decodeAdminJSON(w http.ResponseWriter, r *http.Request, dst any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	defer r.Body.Close()
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		writeJSONError(w, 400, "invalid request")
		return false
	}
	return true
}

func (a *app) profileExists(r *http.Request, id int64) bool {
	var ok bool
	_ = a.db.QueryRowContext(r.Context(), `SELECT EXISTS(SELECT 1 FROM profiles WHERE id=$1)`, id).Scan(&ok)
	return ok
}
func (a *app) profileHasAdmin(r *http.Request, id int64) (bool, error) {
	var ok bool
	err := a.db.QueryRowContext(r.Context(), `SELECT EXISTS(SELECT 1 FROM profile_permissions pp JOIN permissions p ON p.id=pp.permission_id WHERE pp.profile_id=$1 AND p.resource='users' AND p.action='write') AND EXISTS(SELECT 1 FROM profile_permissions pp JOIN permissions p ON p.id=pp.permission_id WHERE pp.profile_id=$1 AND p.resource='profiles' AND p.action='write')`, id).Scan(&ok)
	return ok, err
}
func (a *app) userIsAdmin(r *http.Request, id int64) (bool, error) {
	var ok bool
	err := a.db.QueryRowContext(r.Context(), `SELECT EXISTS(SELECT 1 FROM users u JOIN profile_permissions pp ON pp.profile_id=u.profile_id JOIN permissions p ON p.id=pp.permission_id WHERE u.id=$1 AND u.active=true AND p.resource='users' AND p.action='write') AND EXISTS(SELECT 1 FROM users u JOIN profile_permissions pp ON pp.profile_id=u.profile_id JOIN permissions p ON p.id=pp.permission_id WHERE u.id=$1 AND u.active=true AND p.resource='profiles' AND p.action='write')`, id).Scan(&ok)
	return ok, err
}
func (a *app) activeAdminCountExcluding(r *http.Request, id int64) (int, error) {
	var n int
	err := a.db.QueryRowContext(r.Context(), `SELECT count(DISTINCT u.id) FROM users u WHERE u.active=true AND u.id<>$1 AND EXISTS(SELECT 1 FROM profile_permissions pp JOIN permissions p ON p.id=pp.permission_id WHERE pp.profile_id=u.profile_id AND p.resource='users' AND p.action='write') AND EXISTS(SELECT 1 FROM profile_permissions pp JOIN permissions p ON p.id=pp.permission_id WHERE pp.profile_id=u.profile_id AND p.resource='profiles' AND p.action='write')`, id).Scan(&n)
	return n, err
}
