package main

import (
	"context"
	"database/sql"
	"embed"
	"html/template"
	"io/fs"
	"net/http"
	"strconv"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"
)

//go:embed templates/*.html static/*
var uiFS embed.FS

var uiTemplates = template.Must(template.ParseFS(uiFS, "templates/*.html"))

type uiData struct {
	Title    string
	User     authUser
	CSRF     string
	Page     string
	Error    string
	CanAdmin bool
}

func (a *app) registerUIHandlers(mux *http.ServeMux) {
	staticFS, _ := fs.Sub(uiFS, "static")
	mux.Handle("/static/", http.StripPrefix("/static/", http.FileServer(http.FS(staticFS))))
	mux.HandleFunc("GET /{$}", redirectToStock)
	mux.HandleFunc("GET /app", redirectToStock)
	mux.HandleFunc("GET /login", a.showLogin)
	mux.HandleFunc("POST /login", a.handleLoginForm)
	mux.HandleFunc("GET /change-password", a.showChangePassword)
	mux.HandleFunc("GET /stock", a.uiPage("stock", "Estoque", "stock", "read"))
	mux.HandleFunc("GET /entry", a.uiPage("movement", "Entrada", "movement_entry", "write"))
	mux.HandleFunc("GET /exit", a.uiPage("movement", "Saída", "movement_exit", "write"))
	mux.HandleFunc("GET /transfer", a.uiPage("movement", "Transferência", "movement_transfer", "write"))
	mux.HandleFunc("GET /no-access", a.noAccess)
	mux.HandleFunc("GET /api/warehouses", a.requireAuth("stock", "read", a.handleWarehouses))
	mux.HandleFunc("GET /api/stock", a.requireAuth("stock", "read", a.handleStock))
	mux.HandleFunc("GET /api/stock/detail/{item_id}", a.requireAuth("stock", "read", a.handleStockDetail))
	mux.HandleFunc("GET /api/stock/availability", a.requireAuth("stock", "read", a.handleStockAvailability))
	mux.HandleFunc("GET /api/products/lookup", a.requireAuth("stock", "read", a.handleLookup))
}

func redirectToStock(w http.ResponseWriter, r *http.Request) {
	http.Redirect(w, r, "/stock", http.StatusSeeOther)
}

func (a *app) showLogin(w http.ResponseWriter, r *http.Request) {
	a.render(w, "login.html", uiData{Title: "Estoque"})
}
func (a *app) noAccess(w http.ResponseWriter, r *http.Request) {
	a.render(w, "no_access.html", uiData{Title: "Sem acesso", Page: "no-access"})
}

func (a *app) showChangePassword(w http.ResponseWriter, r *http.Request) {
	u, _, err := a.authenticate(r.Context(), r)
	if err != nil {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	csrf, _ := a.rotateCSRF(r.Context(), r)
	a.render(w, "change_password.html", uiData{Title: "Alterar senha", User: u, CSRF: csrf, Page: "change-password"})
}

func (a *app) handleLoginForm(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		a.render(w, "login.html", uiData{Title: "Estoque", Error: "Credenciais inválidas"})
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	req := loginRequest{Login: r.FormValue("login"), Password: r.FormValue("password")}
	if strings.TrimSpace(req.Login) == "" || req.Password == "" {
		a.render(w, "login.html", uiData{Title: "Estoque", Error: "Credenciais inválidas"})
		return
	}
	id, ok := a.verifyPassword(r.Context(), req.Login, req.Password)
	if !ok {
		a.render(w, "login.html", uiData{Title: "Estoque", Error: "Credenciais inválidas"})
		return
	}
	tok, _ := newToken()
	csrf, _ := newToken()
	exp := now().Add(sessionDuration)
	_, err := a.db.ExecContext(r.Context(), `INSERT INTO sessions (user_id,token_hash,csrf_token_hash,expires_at) VALUES ($1,$2,$3,$4)`, id, hashToken(tok), hashToken(csrf), exp)
	if err != nil {
		a.render(w, "login.html", uiData{Title: "Estoque", Error: "Erro interno"})
		return
	}
	setSessionCookie(w, tok, exp)
	http.Redirect(w, r, "/stock", http.StatusSeeOther)
}

func (a *app) verifyPassword(ctx context.Context, login, pass string) (int64, bool) {
	var id int64
	var hash string
	var active bool
	err := a.db.QueryRowContext(ctx, `SELECT id,password_hash,active FROM users WHERE login=$1`, login).Scan(&id, &hash, &active)
	if err != nil || !active {
		return 0, false
	}
	return id, bcryptCompare(hash, pass)
}

func (a *app) uiPage(tmpl, title, res, act string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		u, _, err := a.authenticate(r.Context(), r)
		if err != nil {
			http.Redirect(w, r, "/login", http.StatusSeeOther)
			return
		}
		if u.MustChangePassword {
			http.Redirect(w, r, "/change-password", http.StatusSeeOther)
			return
		}
		ok, err := a.hasPermission(r.Context(), u.ID, res, act)
		if err != nil || !ok {
			http.Redirect(w, r, "/no-access", http.StatusSeeOther)
			return
		}
		csrf, _ := a.rotateCSRF(r.Context(), r)
		canAdmin, _ := a.hasPermission(r.Context(), u.ID, "users", "write")
		a.render(w, tmpl+".html", uiData{Title: title, User: u, CSRF: csrf, Page: tmpl, CanAdmin: canAdmin})
	}
}

func (a *app) rotateCSRF(ctx context.Context, r *http.Request) (string, error) {
	c, _ := r.Cookie(sessionCookieName)
	if c == nil {
		return "", errAuthRequired
	}
	csrf, err := newToken()
	if err != nil {
		return "", err
	}
	_, err = a.db.ExecContext(ctx, `UPDATE sessions SET csrf_token_hash=$1 WHERE token_hash=$2 AND revoked_at IS NULL AND expires_at>now()`, hashToken(csrf), hashToken(c.Value))
	return csrf, err
}
func (a *app) render(w http.ResponseWriter, name string, data uiData) {
	setSecurityHeaders(w)
	_ = uiTemplates.ExecuteTemplate(w, name, data)
}

func (a *app) handleWarehouses(w http.ResponseWriter, r *http.Request) {
	rows, err := a.db.QueryContext(r.Context(), `SELECT id,name FROM warehouses WHERE active=true ORDER BY name`)
	if err != nil {
		writeJSONError(w, 500, "internal error")
		return
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var id int64
		var n string
		rows.Scan(&id, &n)
		out = append(out, map[string]any{"id": id, "name": n})
	}
	writeJSON(w, 200, out)
}
func (a *app) handleLookup(w http.ResponseWriter, r *http.Request) {
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	if len(q) < 2 {
		writeJSON(w, 200, []any{})
		return
	}
	rows, err := a.db.QueryContext(r.Context(), `SELECT s.id, i.name, COALESCE(NULLIF(s.name,''),NULLIF(s.brand,''),''), COALESCE(s.barcode,''), i.unit_code FROM product_skus s JOIN inventory_items i ON i.id=s.item_id WHERE s.active=true AND i.active=true AND (s.barcode=$1 OR lower(i.name) LIKE lower($2) OR lower(COALESCE(s.name,'')) LIKE lower($2) OR lower(COALESCE(s.brand,'')) LIKE lower($2)) ORDER BY i.name,COALESCE(s.name,s.brand) LIMIT 10`, q, "%"+q+"%")
	if err != nil {
		writeJSONError(w, 500, "internal error")
		return
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var id int64
		var item, brand, barcode, unit string
		rows.Scan(&id, &item, &brand, &barcode, &unit)
		out = append(out, map[string]any{"sku_id": id, "item": item, "brand": brand, "barcode": barcode, "unit": unit})
	}
	writeJSON(w, 200, out)
}
func (a *app) handleStock(w http.ResponseWriter, r *http.Request) {
	wh, _ := strconv.ParseInt(r.URL.Query().Get("warehouse_id"), 10, 64)
	showZero := r.URL.Query().Get("show_zero") == "1"
	var rows *sql.Rows
	var err error
	if wh <= 0 {
		rows, err = a.db.QueryContext(r.Context(), stockRowsSQL(true, showZero))
	} else {
		rows, err = a.db.QueryContext(r.Context(), stockRowsSQL(false, showZero), wh)
	}
	if err != nil {
		writeJSONError(w, 500, "internal error")
		return
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var id int64
		var name, unit, min, qty string
		var below bool
		rows.Scan(&id, &name, &unit, &min, &qty, &below)
		out = append(out, map[string]any{"item_id": id, "name": name, "unit": unit, "minimum": min, "quantity": qty, "below_minimum": below})
	}
	writeJSON(w, 200, out)
}

func bcryptCompare(hash, pass string) bool {
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(pass)) == nil
}
func now() time.Time { return time.Now() }

const sessionDuration = 12 * time.Hour

func setSecurityHeaders(w http.ResponseWriter) {
	w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self'; object-src 'none'; base-uri 'self'; frame-ancestors 'none'")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Referrer-Policy", "same-origin")
}

const stockSQL = `SELECT i.id,i.name,i.unit_code,COALESCE(i.minimum_stock::text,''),COALESCE(sum(CASE WHEN ml.direction='IN' THEN ml.quantity ELSE -ml.quantity END),0)::text, CASE WHEN i.minimum_stock IS NULL THEN false ELSE COALESCE(sum(CASE WHEN ml.direction='IN' THEN ml.quantity ELSE -ml.quantity END),0) < i.minimum_stock END FROM inventory_items i JOIN product_skus s ON s.item_id=i.id LEFT JOIN movement_lines ml ON ml.sku_id=s.id AND ml.warehouse_id=$1 GROUP BY i.id ORDER BY i.name`
