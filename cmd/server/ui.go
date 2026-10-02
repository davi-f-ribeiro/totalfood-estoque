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

//go:embed templates/*.html static/* static/vendor/*.js
var uiFS embed.FS

var uiTemplates = template.Must(template.ParseFS(uiFS, "templates/*.html"))

type uiData struct {
	Title             string
	User              authUser
	CSRF              string
	Page              string
	Error             string
	CanStock          bool
	CanEntry          bool
	CanExit           bool
	CanTransfer       bool
	CanReports        bool
	CanCatalog        bool
	CanAssets         bool
	CanInventory      bool
	CanInventoryWrite bool
	CanAdmin          bool
	CanUserAdmin      bool
	CanProfileAdmin   bool
	CanWarehouseAdmin bool
}

func (a *app) registerUIHandlers(mux *http.ServeMux) {
	staticFS, _ := fs.Sub(uiFS, "static")
	mux.Handle("/static/", http.StripPrefix("/static/", http.FileServer(http.FS(staticFS))))
	mux.HandleFunc("GET /{$}", a.redirectToAuthorizedHome)
	mux.HandleFunc("GET /app", a.redirectToAuthorizedHome)
	mux.HandleFunc("GET /login", a.showLogin)
	mux.HandleFunc("POST /login", a.handleLoginForm)
	mux.HandleFunc("GET /change-password", a.showChangePassword)
	mux.HandleFunc("GET /stock", a.uiPage("stock", "Estoque", "stock", "read"))
	mux.HandleFunc("GET /reports", a.uiPage("reports", "Relatórios", "operations", "read"))
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
	http.Redirect(w, r, "/app", http.StatusSeeOther)
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
		data, err := a.uiDataForUser(r.Context(), u, csrf, tmpl, title)
		if err != nil {
			http.Error(w, "Não foi possível carregar a página.", http.StatusInternalServerError)
			return
		}
		a.render(w, tmpl+".html", data)
	}
}

func (a *app) uiDataForUser(ctx context.Context, u authUser, csrf, page, title string) (uiData, error) {
	rows, err := a.db.QueryContext(ctx, `SELECT p.resource,p.action
FROM profile_permissions pp
JOIN permissions p ON p.id=pp.permission_id
WHERE pp.profile_id=$1`, u.ProfileID)
	if err != nil {
		return uiData{}, err
	}
	defer rows.Close()
	perms := map[string]bool{}
	for rows.Next() {
		var resource, action string
		if err := rows.Scan(&resource, &action); err != nil {
			return uiData{}, err
		}
		perms[resource+"/"+action] = true
	}
	if err := rows.Err(); err != nil {
		return uiData{}, err
	}
	has := func(resource, action string) bool { return perms[resource+"/"+action] }
	canUserAdmin := has("users", "write")
	canProfileAdmin := has("profiles", "write")
	canWarehouseAdmin := has("warehouses", "write")
	return uiData{
		Title: title, User: u, CSRF: csrf, Page: page,
		CanStock: has("stock", "read"),
		CanEntry: has("movement_entry", "write"),
		CanExit: has("movement_exit", "write"),
		CanTransfer: has("movement_transfer", "write"),
		CanReports: has("operations", "read"),
		CanCatalog: has("catalog", "read"),
		CanAssets: has("assets", "read"),
		CanInventory: has("inventory_validation", "read"),
		CanInventoryWrite: has("inventory_validation", "write"),
		CanAdmin: canUserAdmin || canProfileAdmin || canWarehouseAdmin,
		CanUserAdmin: canUserAdmin,
		CanProfileAdmin: canProfileAdmin,
		CanWarehouseAdmin: canWarehouseAdmin,
	}, nil
}

func (a *app) adminPage(w http.ResponseWriter, r *http.Request) {
	u, _, err := a.authenticate(r.Context(), r)
	if err != nil {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	if u.MustChangePassword {
		http.Redirect(w, r, "/change-password", http.StatusSeeOther)
		return
	}
	csrf, _ := a.rotateCSRF(r.Context(), r)
	data, err := a.uiDataForUser(r.Context(), u, csrf, "admin", "Administração")
	if err != nil {
		http.Error(w, "Não foi possível carregar a página.", http.StatusInternalServerError)
		return
	}
	if !data.CanAdmin {
		http.Redirect(w, r, "/no-access", http.StatusSeeOther)
		return
	}
	a.render(w, "admin.html", data)
}

func (a *app) redirectToAuthorizedHome(w http.ResponseWriter, r *http.Request) {
	u, _, err := a.authenticate(r.Context(), r)
	if err != nil {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	if u.MustChangePassword {
		http.Redirect(w, r, "/change-password", http.StatusSeeOther)
		return
	}
	data, err := a.uiDataForUser(r.Context(), u, "", "", "")
	if err != nil {
		http.Error(w, "Não foi possível carregar seus acessos.", http.StatusInternalServerError)
		return
	}
	for _, candidate := range []struct {
		allowed bool
		path    string
	}{
		{data.CanStock, "/stock"},
		{data.CanEntry, "/entry"},
		{data.CanExit, "/exit"},
		{data.CanTransfer, "/transfer"},
		{data.CanReports, "/reports"},
		{data.CanCatalog, "/catalog"},
		{data.CanAssets, "/assets"},
		{data.CanInventory, "/inventory"},
		{data.CanAdmin, "/admin"},
	} {
		if candidate.allowed {
			http.Redirect(w, r, candidate.path, http.StatusSeeOther)
			return
		}
	}
	http.Redirect(w, r, "/no-access", http.StatusSeeOther)
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
	rows, err := a.db.QueryContext(r.Context(), `
SELECT s.id,i.name,b.name,COALESCE(s.barcode,''),i.unit_code,s.content_quantity::text
FROM product_skus s
JOIN inventory_items i ON i.id=s.item_id
JOIN brands b ON b.id=s.brand_id
WHERE s.active=true AND i.active=true AND b.active=true
  AND (s.barcode=$1 OR lower(i.name) LIKE lower($2) OR lower(b.name) LIKE lower($2)
       OR lower(COALESCE(s.name,'')) LIKE lower($2))
ORDER BY i.name,b.name,s.content_quantity,s.id
LIMIT 10`, q, "%"+q+"%")
	if err != nil {
		writeJSONError(w, 500, "internal error")
		return
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var id int64
		var item, brand, barcode, unit, contentQuantity string
		rows.Scan(&id, &item, &brand, &barcode, &unit, &contentQuantity)
		out = append(out, map[string]any{"sku_id": id, "item": item, "brand": brand, "barcode": barcode, "unit": unit, "content_quantity": contentQuantity})
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
		var id, brandID int64
		var brand, name, unit, min, qty string
		var below bool
		if err := rows.Scan(&id,&brandID,&brand,&name,&unit,&min,&qty,&below); err != nil {
			writeJSONError(w, 500, "internal error")
			return
		}
		out = append(out, map[string]any{"item_id": id, "brand_id": brandID, "brand": brand, "name": name, "unit": unit, "minimum": min, "quantity": qty, "below_minimum": below})
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

