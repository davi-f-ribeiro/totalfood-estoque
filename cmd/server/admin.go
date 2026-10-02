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
	Password  string `json:"password,omitempty"`
	ProfileID int64  `json:"profile_id"`
	Active    *bool  `json:"active"`
	Version   int64  `json:"version"`
}

type adminCreateUserRequest struct {
	Login     string `json:"login"`
	Password  string `json:"password"`
	ProfileID int64  `json:"profile_id"`
	Active    *bool  `json:"active"`
}

type adminPasswordRequest struct {
	Password string `json:"password"`
	Version  int64  `json:"version"`
}

type adminProfileRequest struct {
	Name    string `json:"name"`
	Version int64  `json:"version,omitempty"`
}

type adminProfilePermissionsRequest struct {
	PermissionIDs []int64 `json:"permission_ids"`
	Version       int64   `json:"version"`
}

func (a *app) registerAdminHandlers(mux *http.ServeMux) {
	mux.HandleFunc("GET /admin", a.adminPage)
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
	rows, err := a.db.QueryContext(r.Context(), `SELECT u.id,u.login,u.profile_id,p.name,u.active,u.created_at::text,u.version FROM users u JOIN profiles p ON p.id=u.profile_id ORDER BY u.login`)
	if err != nil {
		writeJSONError(w, 500, "internal error")
		return
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var id, pid, version int64
		var login, pname, created string
		var active bool
		if err := rows.Scan(&id, &login, &pid, &pname, &active, &created, &version); err != nil {
			writeJSONError(w, 500, "internal error")
			return
		}
		out = append(out, map[string]any{"id": id, "login": login, "profile_id": pid, "profile": pname, "active": active, "created_at": created, "version": version})
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
		writeJSONError(w, 400, "Dados informados são inválidos.")
		return
	}
	active := true
	if req.Active != nil {
		active = *req.Active
	}
	if !a.profileExists(r, req.ProfileID) {
		writeJSONError(w, 400, "Perfil informado é inválido.")
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
		writeJSONError(w, 409, "Usuário não pôde ser criado com os dados informados.")
		return
	}
	writeJSON(w, 201, map[string]any{"id": id, "login": login, "profile_id": req.ProfileID, "active": active, "version": int64(1)})
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
	if login == "" || req.ProfileID <= 0 || req.Version <= 0 {
		writeJSONError(w, http.StatusBadRequest, "Dados informados são inválidos.")
		return
	}
	active := true
	if req.Active != nil {
		active = *req.Active
	}
	var passwordHash string
	if req.Password != "" {
		h, err := bcrypt.GenerateFromPassword([]byte(req.Password), bcryptCost)
		if err != nil {
			writeJSONError(w, http.StatusInternalServerError, "internal error")
			return
		}
		passwordHash = string(h)
	}

	tx, err := a.db.BeginTx(r.Context(), nil)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "internal error")
		return
	}
	defer tx.Rollback()

	if _, err = tx.ExecContext(r.Context(), `LOCK TABLE users IN SHARE ROW EXCLUSIVE MODE`); err != nil {
		writeJSONError(w, http.StatusInternalServerError, "internal error")
		return
	}
	if _, err = tx.ExecContext(r.Context(), `LOCK TABLE profile_permissions IN SHARE ROW EXCLUSIVE MODE`); err != nil {
		writeJSONError(w, http.StatusInternalServerError, "internal error")
		return
	}

	var currentProfileID, currentVersion int64
	var currentActive bool
	if err = tx.QueryRowContext(r.Context(), `SELECT profile_id,active,version FROM users WHERE id=$1 FOR UPDATE`, id).Scan(&currentProfileID, &currentActive, &currentVersion); errors.Is(err, sql.ErrNoRows) {
		writeJSONError(w, http.StatusNotFound, "user not found")
		return
	} else if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "internal error")
		return
	}
	if currentVersion != req.Version {
		writeConcurrentEditError(w)
		return
	}

	var profileExists, newAdmin bool
	if err = tx.QueryRowContext(r.Context(), `
SELECT EXISTS(SELECT 1 FROM profiles WHERE id=$1),
       EXISTS(SELECT 1 FROM profile_permissions pp JOIN permissions p ON p.id=pp.permission_id WHERE pp.profile_id=$1 AND p.resource='users' AND p.action='write')
       AND EXISTS(SELECT 1 FROM profile_permissions pp JOIN permissions p ON p.id=pp.permission_id WHERE pp.profile_id=$1 AND p.resource='profiles' AND p.action='write')`, req.ProfileID).Scan(&profileExists, &newAdmin); err != nil {
		writeJSONError(w, http.StatusInternalServerError, "internal error")
		return
	}
	if !profileExists {
		writeJSONError(w, http.StatusBadRequest, "Perfil informado é inválido.")
		return
	}

	u := userFromContext(r.Context())
	if id == u.ID && !active {
		writeJSONError(w, http.StatusConflict, "não é permitido desativar o próprio administrador")
		return
	}
	if id == u.ID && !newAdmin {
		writeJSONError(w, http.StatusConflict, "não é permitido remover o próprio acesso administrativo")
		return
	}

	var newVersion int64
	if passwordHash != "" {
		err = tx.QueryRowContext(r.Context(), `UPDATE users SET login=$1,profile_id=$2,active=$3,password_hash=$4,version=version+1 WHERE id=$5 AND version=$6 RETURNING version`, login, req.ProfileID, active, passwordHash, id, req.Version).Scan(&newVersion)
	} else {
		err = tx.QueryRowContext(r.Context(), `UPDATE users SET login=$1,profile_id=$2,active=$3,version=version+1 WHERE id=$4 AND version=$5 RETURNING version`, login, req.ProfileID, active, id, req.Version).Scan(&newVersion)
	}
	if errors.Is(err, sql.ErrNoRows) {
		writeConcurrentEditError(w)
		return
	}
	if err != nil {
		log.Printf("admin user update failed user_id=%d error=%v", id, err)
		writeJSONError(w, http.StatusConflict, "Usuário não pôde ser atualizado com os dados informados.")
		return
	}

	var activeAdmins int
	if err = tx.QueryRowContext(r.Context(), `
SELECT count(DISTINCT u.id)
FROM users u
WHERE u.active=true
  AND EXISTS(SELECT 1 FROM profile_permissions pp JOIN permissions p ON p.id=pp.permission_id WHERE pp.profile_id=u.profile_id AND p.resource='users' AND p.action='write')
  AND EXISTS(SELECT 1 FROM profile_permissions pp JOIN permissions p ON p.id=pp.permission_id WHERE pp.profile_id=u.profile_id AND p.resource='profiles' AND p.action='write')`).Scan(&activeAdmins); err != nil {
		writeJSONError(w, http.StatusInternalServerError, "internal error")
		return
	}
	if activeAdmins == 0 {
		writeJSONError(w, http.StatusConflict, "não é permitido remover o último administrador ativo")
		return
	}

	if _, err = tx.ExecContext(r.Context(), `UPDATE sessions SET revoked_at=now() WHERE user_id=$1 AND revoked_at IS NULL`, id); err != nil {
		writeJSONError(w, http.StatusInternalServerError, "internal error")
		return
	}
	if err = tx.Commit(); err != nil {
		writeJSONError(w, http.StatusInternalServerError, "internal error")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": "ok", "reauth_required": id == u.ID, "profile_id": req.ProfileID, "active": active, "previous_profile_id": currentProfileID, "previous_active": currentActive, "version": newVersion})
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
	if req.Password == "" || req.Version <= 0 {
		writeJSONError(w, http.StatusBadRequest, "Dados informados são inválidos.")
		return
	}
	h, err := bcrypt.GenerateFromPassword([]byte(req.Password), bcryptCost)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "internal error")
		return
	}
	tx, err := a.db.BeginTx(r.Context(), nil)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "internal error")
		return
	}
	defer tx.Rollback()
	var currentVersion int64
	if err = tx.QueryRowContext(r.Context(), `SELECT version FROM users WHERE id=$1 FOR UPDATE`, id).Scan(&currentVersion); errors.Is(err, sql.ErrNoRows) {
		writeJSONError(w, http.StatusNotFound, "user not found")
		return
	} else if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "internal error")
		return
	}
	if currentVersion != req.Version {
		writeConcurrentEditError(w)
		return
	}
	var newVersion int64
	if err = tx.QueryRowContext(r.Context(), `UPDATE users SET password_hash=$1,version=version+1 WHERE id=$2 AND version=$3 RETURNING version`, string(h), id, req.Version).Scan(&newVersion); errors.Is(err, sql.ErrNoRows) {
		writeConcurrentEditError(w)
		return
	} else if err != nil {
		log.Printf("admin password reset failed user_id=%d error=%v", id, err)
		writeJSONError(w, http.StatusInternalServerError, "internal error")
		return
	}
	if _, err = tx.ExecContext(r.Context(), `UPDATE sessions SET revoked_at=now() WHERE user_id=$1 AND revoked_at IS NULL`, id); err != nil {
		writeJSONError(w, http.StatusInternalServerError, "internal error")
		return
	}
	if err = tx.Commit(); err != nil {
		writeJSONError(w, http.StatusInternalServerError, "internal error")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": "ok", "reauth_required": id == userFromContext(r.Context()).ID, "version": newVersion})
}

func (a *app) handleAdminProfiles(w http.ResponseWriter, r *http.Request) {
	rows, err := a.db.QueryContext(r.Context(), `SELECT id,name,version FROM profiles ORDER BY name`)
	if err != nil {
		writeJSONError(w, 500, "internal error")
		return
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var id, version int64
		var name string
		if err := rows.Scan(&id, &name, &version); err != nil {
			writeJSONError(w, 500, "internal error")
			return
		}
		prows, err := a.db.QueryContext(r.Context(), `SELECT permission_id FROM profile_permissions WHERE profile_id=$1 ORDER BY permission_id`, id)
		if err != nil {
			writeJSONError(w, 500, "internal error")
			return
		}
		ids := []int64{}
		for prows.Next() {
			var pid int64
			if err := prows.Scan(&pid); err != nil {
				prows.Close()
				writeJSONError(w, 500, "internal error")
				return
			}
			ids = append(ids, pid)
		}
		prows.Close()
		out = append(out, map[string]any{"id": id, "name": name, "permission_ids": ids, "version": version})
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
		writeJSONError(w, 400, "Dados informados são inválidos.")
		return
	}
	var id int64
	if err := a.db.QueryRowContext(r.Context(), `INSERT INTO profiles (name) VALUES ($1) RETURNING id`, name).Scan(&id); err != nil {
		writeJSONError(w, 409, "Perfil não pôde ser criado com os dados informados.")
		return
	}
	writeJSON(w, 201, map[string]any{"id": id, "name": name, "version": int64(1)})
}

func (a *app) handleAdminUpdateProfile(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var req adminProfileRequest
	if !decodeAdminJSON(w, r, &req) {
		return
	}
	name := strings.TrimSpace(req.Name)
	if name == "" || req.Version <= 0 {
		writeJSONError(w, http.StatusBadRequest, "Dados informados são inválidos.")
		return
	}
	var old string
	var currentVersion int64
	if err := a.db.QueryRowContext(r.Context(), `SELECT name,version FROM profiles WHERE id=$1`, id).Scan(&old, &currentVersion); errors.Is(err, sql.ErrNoRows) {
		writeJSONError(w, http.StatusNotFound, "profile not found")
		return
	} else if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "internal error")
		return
	}
	if currentVersion != req.Version {
		writeConcurrentEditError(w)
		return
	}
	if old == "Diretor" {
		writeJSONError(w, http.StatusConflict, "perfil Diretor não pode ser renomeado")
		return
	}
	var newVersion int64
	err := a.db.QueryRowContext(r.Context(), `UPDATE profiles SET name=$1,version=version+1 WHERE id=$2 AND version=$3 RETURNING version`, name, id, req.Version).Scan(&newVersion)
	if errors.Is(err, sql.ErrNoRows) {
		writeConcurrentEditError(w)
		return
	}
	if err != nil {
		writeJSONError(w, http.StatusConflict, "Perfil não pôde ser atualizado com os dados informados.")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": "ok", "version": newVersion})
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
	var req adminProfilePermissionsRequest
	if !decodeAdminJSON(w, r, &req) {
		return
	}
	if req.Version <= 0 {
		writeJSONError(w, http.StatusBadRequest, "Dados informados são inválidos.")
		return
	}

	tx, err := a.db.BeginTx(r.Context(), nil)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "internal error")
		return
	}
	defer tx.Rollback()

	if _, err = tx.ExecContext(r.Context(), `LOCK TABLE users IN SHARE ROW EXCLUSIVE MODE`); err != nil {
		writeJSONError(w, http.StatusInternalServerError, "internal error")
		return
	}
	if _, err = tx.ExecContext(r.Context(), `LOCK TABLE profile_permissions IN SHARE ROW EXCLUSIVE MODE`); err != nil {
		writeJSONError(w, http.StatusInternalServerError, "internal error")
		return
	}

	var name string
	var currentVersion int64
	if err = tx.QueryRowContext(r.Context(), `SELECT name,version FROM profiles WHERE id=$1 FOR UPDATE`, id).Scan(&name, &currentVersion); errors.Is(err, sql.ErrNoRows) {
		writeJSONError(w, http.StatusNotFound, "profile not found")
		return
	} else if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "internal error")
		return
	}
	if currentVersion != req.Version {
		writeConcurrentEditError(w)
		return
	}
	if name == "Diretor" {
		writeJSONError(w, http.StatusConflict, "permissões do Diretor são completas")
		return
	}

	seen := map[int64]bool{}
	newUsersWrite, newProfilesWrite := false, false
	for _, pid := range req.PermissionIDs {
		if pid <= 0 || seen[pid] {
			writeJSONError(w, http.StatusBadRequest, "Permissão informada é inválida.")
			return
		}
		seen[pid] = true
		var resource, action string
		if err = tx.QueryRowContext(r.Context(), `SELECT resource,action FROM permissions WHERE id=$1`, pid).Scan(&resource, &action); errors.Is(err, sql.ErrNoRows) {
			writeJSONError(w, http.StatusBadRequest, "Permissão informada é inválida.")
			return
		} else if err != nil {
			writeJSONError(w, http.StatusInternalServerError, "internal error")
			return
		}
		if resource == "users" && action == "write" {
			newUsersWrite = true
		}
		if resource == "profiles" && action == "write" {
			newProfilesWrite = true
		}
	}

	u := userFromContext(r.Context())
	if id == u.ProfileID && !(newUsersWrite && newProfilesWrite) {
		writeJSONError(w, http.StatusConflict, "não é permitido remover o próprio acesso administrativo")
		return
	}

	if _, err = tx.ExecContext(r.Context(), `DELETE FROM profile_permissions WHERE profile_id=$1`, id); err != nil {
		writeJSONError(w, http.StatusInternalServerError, "internal error")
		return
	}
	for _, pid := range req.PermissionIDs {
		res, execErr := tx.ExecContext(r.Context(), `INSERT INTO profile_permissions (profile_id,permission_id) VALUES ($1,$2)`, id, pid)
		if execErr != nil {
			writeJSONError(w, http.StatusBadRequest, "Permissão informada é inválida.")
			return
		}
		if n, _ := res.RowsAffected(); n != 1 {
			writeJSONError(w, http.StatusBadRequest, "Permissão informada é inválida.")
			return
		}
	}

	var activeAdmins int
	if err = tx.QueryRowContext(r.Context(), `
SELECT count(DISTINCT u.id)
FROM users u
WHERE u.active=true
  AND EXISTS(SELECT 1 FROM profile_permissions pp JOIN permissions p ON p.id=pp.permission_id WHERE pp.profile_id=u.profile_id AND p.resource='users' AND p.action='write')
  AND EXISTS(SELECT 1 FROM profile_permissions pp JOIN permissions p ON p.id=pp.permission_id WHERE pp.profile_id=u.profile_id AND p.resource='profiles' AND p.action='write')`).Scan(&activeAdmins); err != nil {
		writeJSONError(w, http.StatusInternalServerError, "internal error")
		return
	}
	if activeAdmins == 0 {
		writeJSONError(w, http.StatusConflict, "não é permitido remover o último administrador ativo")
		return
	}

	var newVersion int64
	if err = tx.QueryRowContext(r.Context(), `UPDATE profiles SET version=version+1 WHERE id=$1 AND version=$2 RETURNING version`, id, req.Version).Scan(&newVersion); errors.Is(err, sql.ErrNoRows) {
		writeConcurrentEditError(w)
		return
	} else if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "internal error")
		return
	}

	if _, err = tx.ExecContext(r.Context(), `UPDATE sessions SET revoked_at=now() WHERE user_id IN (SELECT id FROM users WHERE profile_id=$1) AND revoked_at IS NULL`, id); err != nil {
		writeJSONError(w, http.StatusInternalServerError, "internal error")
		return
	}
	if err = tx.Commit(); err != nil {
		writeJSONError(w, http.StatusInternalServerError, "internal error")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": "ok", "reauth_required": id == u.ProfileID, "version": newVersion})
}

func decodeAdminJSON(w http.ResponseWriter, r *http.Request, dst any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	defer r.Body.Close()
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		writeJSONError(w, 400, "Dados informados são inválidos.")
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
