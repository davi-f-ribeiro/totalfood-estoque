package main

import (
	"encoding/json"
	"net/http"
	"strings"

	"golang.org/x/crypto/bcrypt"
)

type changeOwnPasswordRequest struct {
	CurrentPassword string `json:"current_password"`
	NewPassword     string `json:"new_password"`
	ConfirmPassword string `json:"confirm_password"`
}

func (a *app) registerAccountHandlers(mux *http.ServeMux) {
	mux.HandleFunc("PATCH /api/me/password", a.requireAuth("operations", "read", a.handleChangeOwnPassword))
}

func (a *app) handleChangeOwnPassword(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	defer r.Body.Close()
	var req changeOwnPasswordRequest
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil || req.CurrentPassword == "" || strings.TrimSpace(req.NewPassword) == "" || req.ConfirmPassword == "" {
		writeJSONError(w, http.StatusBadRequest, "invalid request")
		return
	}
	if req.NewPassword != req.ConfirmPassword {
		writeJSONError(w, http.StatusBadRequest, "confirmação divergente")
		return
	}
	if len(req.NewPassword) < 8 {
		writeJSONError(w, http.StatusBadRequest, "senha deve ter ao menos 8 caracteres")
		return
	}
	u := userFromContext(r.Context())
	var hash string
	if err := a.db.QueryRowContext(r.Context(), `SELECT password_hash FROM users WHERE id=$1 AND active=true`, u.ID).Scan(&hash); err != nil {
		writeJSONError(w, http.StatusUnauthorized, "authentication required")
		return
	}
	if bcrypt.CompareHashAndPassword([]byte(hash), []byte(req.CurrentPassword)) != nil {
		writeJSONError(w, http.StatusUnauthorized, "senha atual incorreta")
		return
	}
	newHash, err := bcrypt.GenerateFromPassword([]byte(req.NewPassword), bcryptCost)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "internal error")
		return
	}
	if _, err := a.db.ExecContext(r.Context(), `UPDATE users SET password_hash=$1, must_change_password=false WHERE id=$2`, string(newHash), u.ID); err != nil {
		writeJSONError(w, http.StatusInternalServerError, "internal error")
		return
	}
	c, _ := r.Cookie(sessionCookieName)
	if c != nil {
		_, _ = a.db.ExecContext(r.Context(), `UPDATE sessions SET revoked_at=now() WHERE user_id=$1 AND token_hash<>$2 AND revoked_at IS NULL`, u.ID, hashToken(c.Value))
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}
