package main

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/bcrypt"
)

const (
	sessionCookieName = "estoque_session"
	csrfHeaderName    = "X-CSRF-Token"
	bcryptCost        = 11
)

var (
	errAuthRequired = errors.New("auth required")
	errForbidden    = errors.New("forbidden")
)

type authUser struct {
	ID, ProfileID      int64
	Login, Profile     string
	MustChangePassword bool
}
type ctxKey string

const authCtxKey ctxKey = "authUser"

type loginRequest struct {
	Login    string `json:"login"`
	Password string `json:"password"`
}

type createUserRequest struct {
	Login    string `json:"login"`
	Password string `json:"password"`
	Profile  string `json:"profile"`
	Active   *bool  `json:"active"`
}

type loginThrottle struct {
	mu sync.Mutex
	m  map[string][]time.Time
}

var authThrottle = &loginThrottle{m: map[string][]time.Time{}}

func (a *app) registerAuthHandlers(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/login", a.handleLogin)
	mux.HandleFunc("POST /api/logout", a.requireSession(a.handleLogout))
	mux.HandleFunc("GET /api/me", a.requireSession(a.handleMe))
	mux.HandleFunc("POST /api/users", a.requireAuth("users", "write", a.handleCreateUser))
}

func (a *app) bootstrapDirector(ctx context.Context) error {
	if osGetenv("BOOTSTRAP_ADMIN_ENABLED") != "true" {
		return nil
	}
	login, pass := strings.TrimSpace(osGetenv("BOOTSTRAP_ADMIN_USER")), osGetenv("BOOTSTRAP_ADMIN_PASSWORD")
	if login == "" || pass == "" {
		return fmt.Errorf("BOOTSTRAP_ADMIN_USER and BOOTSTRAP_ADMIN_PASSWORD are required when BOOTSTRAP_ADMIN_ENABLED=true")
	}
	if login != "diretor" {
		return fmt.Errorf("BOOTSTRAP_ADMIN_USER must be diretor")
	}
	tx, err := a.db.BeginTx(ctx, &sql.TxOptions{})
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var profileID int64
	if err := tx.QueryRowContext(ctx, `SELECT id FROM profiles WHERE name='Diretor'`).Scan(&profileID); err != nil {
		return err
	}
	var permissionsTotal int
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM permissions`).Scan(&permissionsTotal); err != nil {
		return err
	}
	if permissionsTotal == 0 {
		return fmt.Errorf("Diretor bootstrap requires existing permissions")
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO profile_permissions (profile_id, permission_id) SELECT $1,id FROM permissions ON CONFLICT DO NOTHING`, profileID); err != nil {
		return err
	}
	h, err := bcrypt.GenerateFromPassword([]byte(pass), bcryptCost)
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO users (login,password_hash,profile_id,active,must_change_password) VALUES ($1,$2,$3,true,true) ON CONFLICT (login) DO UPDATE SET password_hash=EXCLUDED.password_hash, profile_id=EXCLUDED.profile_id, active=true, must_change_password=true`, login, string(h), profileID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE sessions SET revoked_at=now() WHERE user_id=(SELECT id FROM users WHERE login=$1) AND revoked_at IS NULL`, login); err != nil {
		return err
	}
	return tx.Commit()
}

func (a *app) handleLogin(w http.ResponseWriter, r *http.Request) {
	ip, _, _ := net.SplitHostPort(r.RemoteAddr)
	if ip == "" {
		ip = r.RemoteAddr
	}
	if !authThrottle.allow(ip) {
		log.Printf("login throttled ip=%s", ip)
		writeJSONError(w, http.StatusTooManyRequests, "too many attempts")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	defer r.Body.Close()
	var req loginRequest
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil || strings.TrimSpace(req.Login) == "" || req.Password == "" {
		writeJSONError(w, http.StatusBadRequest, "Dados de autenticação informados são inválidos.")
		return
	}
	var id int64
	var hash string
	var active, mustChange bool
	err := a.db.QueryRowContext(r.Context(), `SELECT id,password_hash,active,must_change_password FROM users WHERE login=$1`, req.Login).Scan(&id, &hash, &active, &mustChange)
	if errors.Is(err, sql.ErrNoRows) {
		log.Printf("login failed login=%s reason=not_found", req.Login)
		writeJSONError(w, http.StatusUnauthorized, "invalid credentials")
		return
	}
	if err != nil {
		log.Printf("login lookup failed: %v", err)
		writeJSONError(w, http.StatusInternalServerError, "internal error")
		return
	}
	if !active {
		log.Printf("login failed login=%s reason=inactive", req.Login)
		writeJSONError(w, http.StatusForbidden, "user inactive")
		return
	}
	if bcrypt.CompareHashAndPassword([]byte(hash), []byte(req.Password)) != nil {
		log.Printf("login failed login=%s reason=password", req.Login)
		writeJSONError(w, http.StatusUnauthorized, "invalid credentials")
		return
	}
	tok, err := newToken()
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "internal error")
		return
	}
	csrf, err := newToken()
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "internal error")
		return
	}
	exp := time.Now().Add(12 * time.Hour)
	_, err = a.db.ExecContext(r.Context(), `INSERT INTO sessions (user_id,token_hash,csrf_token_hash,expires_at) VALUES ($1,$2,$3,$4)`, id, hashToken(tok), hashToken(csrf), exp)
	if err != nil {
		log.Printf("session create failed user_id=%d error=%v", id, err)
		writeJSONError(w, http.StatusInternalServerError, "internal error")
		return
	}
	http.SetCookie(w, &http.Cookie{Name: sessionCookieName, Value: tok, Path: "/", Expires: exp, HttpOnly: true, Secure: secureCookie(), SameSite: http.SameSiteStrictMode})
	writeJSON(w, http.StatusOK, map[string]any{"status": "ok", "csrf_token": csrf, "must_change_password": mustChange})
}

func (a *app) handleLogout(w http.ResponseWriter, r *http.Request) {
	c, _ := r.Cookie(sessionCookieName)
	if c != nil {
		_, _ = a.db.ExecContext(r.Context(), `UPDATE sessions SET revoked_at=now() WHERE token_hash=$1 AND revoked_at IS NULL`, hashToken(c.Value))
	}
	http.SetCookie(w, &http.Cookie{Name: sessionCookieName, Value: "", Path: "/", MaxAge: -1, HttpOnly: true, Secure: secureCookie(), SameSite: http.SameSiteStrictMode})
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (a *app) handleMe(w http.ResponseWriter, r *http.Request) {
	u := userFromContext(r.Context())
	writeJSON(w, http.StatusOK, map[string]any{"id": u.ID, "login": u.Login, "profile": u.Profile, "must_change_password": u.MustChangePassword})
}

func (a *app) handleCreateUser(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	defer r.Body.Close()
	var req createUserRequest
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil || strings.TrimSpace(req.Login) == "" || req.Password == "" || strings.TrimSpace(req.Profile) == "" {
		writeJSONError(w, http.StatusBadRequest, "Dados de autenticação informados são inválidos.")
		return
	}
	active := true
	if req.Active != nil {
		active = *req.Active
	}
	var profileID int64
	if err := a.db.QueryRowContext(r.Context(), `SELECT id FROM profiles WHERE name=$1`, req.Profile).Scan(&profileID); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid profile")
		return
	}
	h, err := bcrypt.GenerateFromPassword([]byte(req.Password), bcryptCost)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "internal error")
		return
	}
	var id int64
	err = a.db.QueryRowContext(r.Context(), `INSERT INTO users (login,password_hash,profile_id,active) VALUES ($1,$2,$3,$4) RETURNING id`, req.Login, string(h), profileID, active).Scan(&id)
	if err != nil {
		log.Printf("user create failed error=%v", err)
		writeJSONError(w, http.StatusConflict, "Usuário inicial não pôde ser criado com os dados informados.")
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"id": id, "login": req.Login, "active": active, "profile": req.Profile})
}

func (a *app) requireSession(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		u, csrf, err := a.authenticate(r.Context(), r)
		if err != nil {
			writeJSONError(w, http.StatusUnauthorized, "authentication required")
			return
		}
		if r.Method != http.MethodGet && subtle.ConstantTimeCompare([]byte(hashToken(r.Header.Get(csrfHeaderName))), []byte(csrf)) != 1 {
			writeJSONError(w, http.StatusForbidden, "csrf rejected")
			return
		}
		if u.MustChangePassword && r.URL.Path != "/api/me/password" && r.URL.Path != "/api/logout" {
			writeJSONError(w, http.StatusForbidden, "password change required")
			return
		}
		next(w, r.WithContext(context.WithValue(r.Context(), authCtxKey, u)))
	}
}

func (a *app) requireAuthenticatedRead(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		u, _, err := a.authenticate(r.Context(), r)
		if err != nil {
			writeJSONError(w, http.StatusUnauthorized, "authentication required")
			return
		}
		if u.MustChangePassword {
			writeJSONError(w, http.StatusForbidden, "password change required")
			return
		}
		next(w, r.WithContext(context.WithValue(r.Context(), authCtxKey, u)))
	}
}

func (a *app) requireAuth(resource, action string, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		u, csrf, err := a.authenticate(r.Context(), r)
		if err != nil {
			writeJSONError(w, http.StatusUnauthorized, "authentication required")
			return
		}
		if r.Method != http.MethodGet && r.URL.Path != "/api/login" && subtle.ConstantTimeCompare([]byte(hashToken(r.Header.Get(csrfHeaderName))), []byte(csrf)) != 1 {
			writeJSONError(w, http.StatusForbidden, "csrf rejected")
			return
		}
		if u.MustChangePassword && r.URL.Path != "/api/me/password" && r.URL.Path != "/api/logout" {
			writeJSONError(w, http.StatusForbidden, "password change required")
			return
		}
		ok, err := a.hasPermission(r.Context(), u.ID, resource, action)
		if err != nil {
			log.Printf("permission check failed user_id=%d error=%v", u.ID, err)
			writeJSONError(w, http.StatusInternalServerError, "internal error")
			return
		}
		if !ok {
			log.Printf("access denied user_id=%d resource=%s action=%s", u.ID, resource, action)
			writeJSONError(w, http.StatusForbidden, "forbidden")
			return
		}
		next(w, r.WithContext(context.WithValue(r.Context(), authCtxKey, u)))
	}
}

func (a *app) authenticate(ctx context.Context, r *http.Request) (authUser, string, error) {
	c, err := r.Cookie(sessionCookieName)
	if err != nil || c.Value == "" {
		return authUser{}, "", errAuthRequired
	}
	var u authUser
	var csrf string
	err = a.db.QueryRowContext(ctx, `SELECT u.id,u.login,u.profile_id,p.name,u.must_change_password,s.csrf_token_hash FROM sessions s JOIN users u ON u.id=s.user_id JOIN profiles p ON p.id=u.profile_id WHERE s.token_hash=$1 AND s.revoked_at IS NULL AND s.expires_at>now() AND u.active=true`, hashToken(c.Value)).Scan(&u.ID, &u.Login, &u.ProfileID, &u.Profile, &u.MustChangePassword, &csrf)
	if err != nil {
		return authUser{}, "", errAuthRequired
	}
	return u, csrf, nil
}

func (a *app) hasPermission(ctx context.Context, userID int64, resource, action string) (bool, error) {
	var ok bool
	err := a.db.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM users u JOIN profile_permissions pp ON pp.profile_id=u.profile_id JOIN permissions p ON p.id=pp.permission_id WHERE u.id=$1 AND u.active=true AND p.resource=$2 AND p.action=$3)`, userID, resource, action).Scan(&ok)
	return ok, err
}
func userFromContext(ctx context.Context) authUser {
	u, _ := ctx.Value(authCtxKey).(authUser)
	return u
}
func newToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
func hashToken(v string) string { h := sha256.Sum256([]byte(v)); return hex.EncodeToString(h[:]) }
func (l *loginThrottle) allow(k string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now()
	xs := l.m[k][:0]
	for _, t := range l.m[k] {
		if now.Sub(t) < 15*time.Minute {
			xs = append(xs, t)
		}
	}
	if len(xs) >= 10 {
		l.m[k] = xs
		return false
	}
	l.m[k] = append(xs, now)
	return true
}
func osGetenv(k string) string { return strings.TrimSpace(os.Getenv(k)) }
func secureCookie() bool       { return os.Getenv("COOKIE_SECURE") != "false" }
func setSessionCookie(w http.ResponseWriter, tok string, exp time.Time) {
	http.SetCookie(w, &http.Cookie{Name: sessionCookieName, Value: tok, Path: "/", Expires: exp, HttpOnly: true, Secure: secureCookie(), SameSite: http.SameSiteStrictMode})
}
