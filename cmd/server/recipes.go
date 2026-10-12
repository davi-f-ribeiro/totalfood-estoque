package main

import (
	"context"
	"database/sql"
	"errors"
	"log"
	"net/http"
	"regexp"
	"strconv"
	"strings"
)

var (
	portionsPattern = regexp.MustCompile(`^(0|[1-9][0-9]*)(\.[0-9]{1,3})?$`)
	pricePattern    = regexp.MustCompile(`^(0|[1-9][0-9]*)(\.[0-9]{1,2})?$`)
)

type customerRequest struct {
	Name    string `json:"name"`
	Active  *bool  `json:"active"`
	Version int64  `json:"version,omitempty"`
}

type recipeLineRequest struct {
	ItemID           int64  `json:"item_id"`
	Quantity         string `json:"quantity"`
	CorrectionFactor string `json:"correction_factor"`
}

type recipePriceRequest struct {
	CustomerID int64  `json:"customer_id"`
	SalePrice  string `json:"sale_price"`
}

type recipeRequest struct {
	Name     string               `json:"name"`
	Portions string               `json:"portions"`
	Notes    string               `json:"notes"`
	Active   *bool                `json:"active"`
	Lines    []recipeLineRequest  `json:"lines"`
	Prices   []recipePriceRequest `json:"prices"`
	Version  int64                `json:"version,omitempty"`
}

func (a *app) registerRecipeHandlers(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/customers", a.requireAuth("costs", "read", a.handleCustomers))
	mux.HandleFunc("POST /api/customers", a.requireAuth("costs", "write", a.handleCreateCustomer))
	mux.HandleFunc("PATCH /api/customers/{id}", a.requireAuth("costs", "write", a.handleUpdateCustomer))
	mux.HandleFunc("GET /api/recipes", a.requireAuth("costs", "read", a.handleRecipes))
	mux.HandleFunc("GET /api/recipes/{id}", a.requireAuth("costs", "read", a.handleRecipe))
	mux.HandleFunc("POST /api/recipes", a.requireAuth("costs", "write", a.handleCreateRecipe))
	mux.HandleFunc("PATCH /api/recipes/{id}", a.requireAuth("costs", "write", a.handleUpdateRecipe))
}

// recipeCostCTE é a única definição de custo da ficha técnica.
//
// $1 é o id da ficha, ou 0 para todas. O preço médio do item usa a mesma
// fórmula de getStockSnapshotAt e listBalanceReport (valor líquido dividido
// pela quantidade líquida de movement_lines), somando depósitos e marcas.
// Item sem estoque ou sem valor fica com avg_cost NULL ("sem custo"), e a
// ficha vira incompleta: nem o custo da porção nem o CMV são calculados.
const recipeCostCTE = `
WITH item_cost AS (
    SELECT ml.item_id,
           COALESCE(sum(CASE WHEN ml.direction='IN' THEN ml.quantity ELSE -ml.quantity END),0) AS qty,
           COALESCE(sum(CASE WHEN ml.direction='IN' THEN ml.quantity*ml.unit_cost ELSE -ml.quantity*ml.unit_cost END),0) AS value
    FROM movement_lines ml
    WHERE ml.item_id IN (SELECT item_id FROM recipe_lines WHERE $1::bigint=0 OR recipe_id=$1::bigint)
    GROUP BY ml.item_id
), line_cost AS (
    SELECT rl.recipe_id,rl.item_id,rl.quantity,rl.correction_factor,
           CASE WHEN c.qty > 0 AND c.value > 0 THEN c.value / c.qty ELSE NULL END AS avg_cost
    FROM recipe_lines rl
    LEFT JOIN item_cost c ON c.item_id=rl.item_id
    WHERE $1::bigint=0 OR rl.recipe_id=$1::bigint
), recipe_cost AS (
    SELECT lc.recipe_id,
           count(*) AS line_count,
           bool_or(lc.avg_cost IS NULL) AS incomplete,
           CASE WHEN bool_or(lc.avg_cost IS NULL) THEN NULL
                ELSE sum(lc.quantity*lc.correction_factor*lc.avg_cost) / r.portions
           END AS cost_per_portion
    FROM line_cost lc
    JOIN recipes r ON r.id=lc.recipe_id
    GROUP BY lc.recipe_id,r.portions
)
`

func nullText(s sql.NullString) any {
	if s.Valid {
		return s.String
	}
	return nil
}

func normalizeRecipeRequest(req *recipeRequest) {
	req.Name = strings.TrimSpace(req.Name)
	req.Portions = normalizeDecimalInput(req.Portions)
	req.Notes = strings.TrimSpace(req.Notes)
	for i := range req.Lines {
		req.Lines[i].Quantity = normalizeDecimalInput(req.Lines[i].Quantity)
		req.Lines[i].CorrectionFactor = normalizeDecimalInput(req.Lines[i].CorrectionFactor)
		if req.Lines[i].CorrectionFactor == "" {
			req.Lines[i].CorrectionFactor = "1"
		}
	}
	for i := range req.Prices {
		req.Prices[i].SalePrice = normalizeDecimalInput(req.Prices[i].SalePrice)
	}
}

// recipeRequestProblem devolve a mensagem de validação, ou vazio se o pedido é válido.
func recipeRequestProblem(req recipeRequest) string {
	if req.Name == "" {
		return "Digite o nome da ficha técnica."
	}
	if !portionsPattern.MatchString(req.Portions) || !positiveDecimal(req.Portions) {
		return "Digite o rendimento em porções, maior que zero, com até 3 casas decimais."
	}
	if len(req.Lines) == 0 {
		return "Escolha ao menos um ingrediente."
	}
	seenItems := map[int64]bool{}
	for _, l := range req.Lines {
		if l.ItemID <= 0 {
			return "Escolha o ingrediente de cada linha."
		}
		if seenItems[l.ItemID] {
			return "Um ingrediente não pode aparecer duas vezes na ficha."
		}
		seenItems[l.ItemID] = true
		if !positiveDecimal(l.Quantity) {
			return "Digite uma quantidade maior que zero, com até 4 casas decimais."
		}
		factor, err := strconv.ParseFloat(l.CorrectionFactor, 64)
		if !decimalPattern.MatchString(l.CorrectionFactor) || err != nil || factor < 1 {
			return "Digite um fator de correção de 1 ou mais, com até 4 casas decimais."
		}
	}
	seenCustomers := map[int64]bool{}
	for _, p := range req.Prices {
		if p.CustomerID <= 0 {
			return "Escolha o cliente de cada preço."
		}
		if seenCustomers[p.CustomerID] {
			return "Um cliente não pode ter dois preços para o mesmo prato."
		}
		seenCustomers[p.CustomerID] = true
		if !pricePattern.MatchString(p.SalePrice) || !positiveDecimal(p.SalePrice) {
			return "Digite um preço de venda maior que zero, com até 2 casas decimais."
		}
	}
	return ""
}

func writeRecipeError(w http.ResponseWriter, err error, logPrefix string) {
	log.Printf("%s error=%v", logPrefix, err)
	msg := err.Error()
	switch {
	case strings.Contains(msg, "idx_recipes_normalized_name_unique"):
		writeJSONError(w, http.StatusConflict, "Já existe uma ficha técnica com este nome.")
	case strings.Contains(msg, "recipe_lines_item_id_fkey"):
		writeJSONError(w, http.StatusBadRequest, "Ingrediente inválido. Escolha um item do estoque.")
	case strings.Contains(msg, "recipe_customer_prices_customer_id_fkey"):
		writeJSONError(w, http.StatusBadRequest, "Cliente inválido. Escolha um cliente cadastrado.")
	default:
		writeJSONError(w, http.StatusBadRequest, "Não foi possível salvar a ficha técnica.")
	}
}

func writeCustomerError(w http.ResponseWriter, err error, logPrefix string) {
	log.Printf("%s error=%v", logPrefix, err)
	if strings.Contains(err.Error(), "idx_customers_normalized_name_unique") {
		writeJSONError(w, http.StatusConflict, "Já existe um cliente com este nome.")
		return
	}
	writeJSONError(w, http.StatusBadRequest, "Não foi possível salvar o cliente.")
}

func (a *app) handleCustomers(w http.ResponseWriter, r *http.Request) {
	rows, err := a.db.QueryContext(r.Context(), `SELECT id,name,active,version FROM customers ORDER BY active DESC,lower(name),id`)
	if err != nil {
		log.Printf("customers list failed error=%v", err)
		writeJSONError(w, http.StatusInternalServerError, "Não foi possível carregar os clientes.")
		return
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var id, version int64
		var name string
		var active bool
		if err := rows.Scan(&id, &name, &active, &version); err != nil {
			log.Printf("customers scan failed error=%v", err)
			writeJSONError(w, http.StatusInternalServerError, "Não foi possível carregar os clientes.")
			return
		}
		out = append(out, map[string]any{"id": id, "name": name, "active": active, "version": version})
	}
	if err := rows.Err(); err != nil {
		log.Printf("customers rows failed error=%v", err)
		writeJSONError(w, http.StatusInternalServerError, "Não foi possível carregar os clientes.")
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (a *app) handleCreateCustomer(w http.ResponseWriter, r *http.Request) {
	var req customerRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	req.Name = strings.TrimSpace(req.Name)
	if req.Name == "" {
		writeJSONError(w, http.StatusBadRequest, "Digite o nome do cliente.")
		return
	}
	active := true
	if req.Active != nil {
		active = *req.Active
	}
	var id int64
	if err := a.db.QueryRowContext(r.Context(), `INSERT INTO customers (name,active) VALUES ($1,$2) RETURNING id`, req.Name, active).Scan(&id); err != nil {
		writeCustomerError(w, err, "customer create failed")
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"id": id, "version": int64(1)})
}

func (a *app) handleUpdateCustomer(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var req customerRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	req.Name = strings.TrimSpace(req.Name)
	if req.Name == "" {
		writeJSONError(w, http.StatusBadRequest, "Digite o nome do cliente.")
		return
	}
	if req.Version <= 0 {
		writeJSONError(w, http.StatusBadRequest, "Atualize a página e tente de novo.")
		return
	}
	active := true
	if req.Active != nil {
		active = *req.Active
	}
	var newVersion int64
	err := a.db.QueryRowContext(r.Context(), `
UPDATE customers SET name=$1,active=$2,version=version+1
WHERE id=$3 AND version=$4
RETURNING version`, req.Name, active, id, req.Version).Scan(&newVersion)
	if errors.Is(err, sql.ErrNoRows) {
		var exists bool
		if qerr := a.db.QueryRowContext(r.Context(), `SELECT EXISTS(SELECT 1 FROM customers WHERE id=$1)`, id).Scan(&exists); qerr != nil {
			writeJSONError(w, http.StatusInternalServerError, "Não foi possível salvar o cliente.")
			return
		}
		if !exists {
			writeJSONError(w, http.StatusNotFound, "Cliente não encontrado.")
			return
		}
		writeConcurrentEditError(w)
		return
	}
	if err != nil {
		writeCustomerError(w, err, "customer update failed")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"id": id, "version": newVersion})
}

func (a *app) handleRecipes(w http.ResponseWriter, r *http.Request) {
	rows, err := a.db.QueryContext(r.Context(), recipeCostCTE+`
SELECT r.id,r.name,r.portions::text,r.active,r.version,
       COALESCE(rc.line_count,0),COALESCE(rc.incomplete,false),
       rc.cost_per_portion::numeric(14,4)::text
FROM recipes r
LEFT JOIN recipe_cost rc ON rc.recipe_id=r.id
ORDER BY r.active DESC,lower(r.name),r.id`, int64(0))
	if err != nil {
		log.Printf("recipes list failed error=%v", err)
		writeJSONError(w, http.StatusInternalServerError, "Não foi possível carregar as fichas técnicas.")
		return
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var id, version, lineCount int64
		var name, portions string
		var active, incomplete bool
		var cost sql.NullString
		if err := rows.Scan(&id, &name, &portions, &active, &version, &lineCount, &incomplete, &cost); err != nil {
			log.Printf("recipes scan failed error=%v", err)
			writeJSONError(w, http.StatusInternalServerError, "Não foi possível carregar as fichas técnicas.")
			return
		}
		out = append(out, map[string]any{
			"id": id, "name": name, "portions": portions, "active": active, "version": version,
			"line_count": lineCount, "incomplete": incomplete, "cost_per_portion": nullText(cost),
		})
	}
	if err := rows.Err(); err != nil {
		log.Printf("recipes rows failed error=%v", err)
		writeJSONError(w, http.StatusInternalServerError, "Não foi possível carregar as fichas técnicas.")
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (a *app) handleRecipe(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	ctx := r.Context()
	var name, portions, notes string
	var active, incomplete bool
	var version int64
	var cost sql.NullString
	err := a.db.QueryRowContext(ctx, recipeCostCTE+`
SELECT r.name,r.portions::text,COALESCE(r.notes,''),r.active,r.version,
       COALESCE(rc.incomplete,false),rc.cost_per_portion::numeric(14,4)::text
FROM recipes r
LEFT JOIN recipe_cost rc ON rc.recipe_id=r.id
WHERE r.id=$1::bigint`, id).Scan(&name, &portions, &notes, &active, &version, &incomplete, &cost)
	if errors.Is(err, sql.ErrNoRows) {
		writeJSONError(w, http.StatusNotFound, "Ficha técnica não encontrada.")
		return
	}
	if err != nil {
		log.Printf("recipe detail failed id=%d error=%v", id, err)
		writeJSONError(w, http.StatusInternalServerError, "Não foi possível carregar a ficha técnica.")
		return
	}

	lineRows, err := a.db.QueryContext(ctx, recipeCostCTE+`
SELECT lc.item_id,i.name,i.unit_code,lc.quantity::text,lc.correction_factor::text,
       (lc.quantity*lc.correction_factor)::numeric(14,4)::text,
       lc.avg_cost::numeric(14,4)::text,
       (lc.quantity*lc.correction_factor*lc.avg_cost)::numeric(14,4)::text
FROM line_cost lc
JOIN inventory_items i ON i.id=lc.item_id
WHERE lc.recipe_id=$1::bigint
ORDER BY lower(i.name),i.id`, id)
	if err != nil {
		log.Printf("recipe lines failed id=%d error=%v", id, err)
		writeJSONError(w, http.StatusInternalServerError, "Não foi possível carregar a ficha técnica.")
		return
	}
	defer lineRows.Close()
	lines := []map[string]any{}
	for lineRows.Next() {
		var itemID int64
		var item, unit, quantity, factor, gross string
		var avgCost, lineCost sql.NullString
		if err := lineRows.Scan(&itemID, &item, &unit, &quantity, &factor, &gross, &avgCost, &lineCost); err != nil {
			log.Printf("recipe lines scan failed id=%d error=%v", id, err)
			writeJSONError(w, http.StatusInternalServerError, "Não foi possível carregar a ficha técnica.")
			return
		}
		lines = append(lines, map[string]any{
			"item_id": itemID, "item": item, "unit": unit, "quantity": quantity,
			"correction_factor": factor, "gross_quantity": gross,
			"avg_cost": nullText(avgCost), "line_cost": nullText(lineCost),
		})
	}
	if err := lineRows.Err(); err != nil {
		log.Printf("recipe lines rows failed id=%d error=%v", id, err)
		writeJSONError(w, http.StatusInternalServerError, "Não foi possível carregar a ficha técnica.")
		return
	}

	priceRows, err := a.db.QueryContext(ctx, recipeCostCTE+`
SELECT p.customer_id,c.name,p.sale_price::text,
       CASE WHEN rc.cost_per_portion IS NULL THEN NULL
            ELSE (rc.cost_per_portion / p.sale_price * 100)::numeric(8,2)::text
       END
FROM recipe_customer_prices p
JOIN customers c ON c.id=p.customer_id
LEFT JOIN recipe_cost rc ON rc.recipe_id=p.recipe_id
WHERE p.recipe_id=$1::bigint
ORDER BY lower(c.name),c.id`, id)
	if err != nil {
		log.Printf("recipe prices failed id=%d error=%v", id, err)
		writeJSONError(w, http.StatusInternalServerError, "Não foi possível carregar a ficha técnica.")
		return
	}
	defer priceRows.Close()
	prices := []map[string]any{}
	for priceRows.Next() {
		var customerID int64
		var customer, salePrice string
		var cmv sql.NullString
		if err := priceRows.Scan(&customerID, &customer, &salePrice, &cmv); err != nil {
			log.Printf("recipe prices scan failed id=%d error=%v", id, err)
			writeJSONError(w, http.StatusInternalServerError, "Não foi possível carregar a ficha técnica.")
			return
		}
		prices = append(prices, map[string]any{
			"customer_id": customerID, "customer": customer, "sale_price": salePrice, "cmv_percent": nullText(cmv),
		})
	}
	if err := priceRows.Err(); err != nil {
		log.Printf("recipe prices rows failed id=%d error=%v", id, err)
		writeJSONError(w, http.StatusInternalServerError, "Não foi possível carregar a ficha técnica.")
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"id": id, "name": name, "portions": portions, "notes": notes, "active": active, "version": version,
		"incomplete": incomplete, "cost_per_portion": nullText(cost), "lines": lines, "prices": prices,
	})
}

func replaceRecipeContent(ctx context.Context, tx *sql.Tx, id int64, req recipeRequest) error {
	if _, err := tx.ExecContext(ctx, `DELETE FROM recipe_lines WHERE recipe_id=$1`, id); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM recipe_customer_prices WHERE recipe_id=$1`, id); err != nil {
		return err
	}
	for _, l := range req.Lines {
		if _, err := tx.ExecContext(ctx, `INSERT INTO recipe_lines (recipe_id,item_id,quantity,correction_factor) VALUES ($1,$2,$3,$4)`, id, l.ItemID, l.Quantity, l.CorrectionFactor); err != nil {
			return err
		}
	}
	for _, p := range req.Prices {
		if _, err := tx.ExecContext(ctx, `INSERT INTO recipe_customer_prices (recipe_id,customer_id,sale_price) VALUES ($1,$2,$3)`, id, p.CustomerID, p.SalePrice); err != nil {
			return err
		}
	}
	return nil
}

func (a *app) handleCreateRecipe(w http.ResponseWriter, r *http.Request) {
	var req recipeRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	normalizeRecipeRequest(&req)
	if msg := recipeRequestProblem(req); msg != "" {
		writeJSONError(w, http.StatusBadRequest, msg)
		return
	}
	active := true
	if req.Active != nil {
		active = *req.Active
	}
	ctx := r.Context()
	tx, err := a.db.BeginTx(ctx, nil)
	if err != nil {
		log.Printf("recipe create begin failed error=%v", err)
		writeJSONError(w, http.StatusInternalServerError, "Não foi possível salvar a ficha técnica.")
		return
	}
	defer func() { _ = tx.Rollback() }()
	var id int64
	if err := tx.QueryRowContext(ctx, `INSERT INTO recipes (name,portions,notes,active) VALUES ($1,$2,NULLIF($3,''),$4) RETURNING id`, req.Name, req.Portions, req.Notes, active).Scan(&id); err != nil {
		writeRecipeError(w, err, "recipe create failed")
		return
	}
	if err := replaceRecipeContent(ctx, tx, id, req); err != nil {
		writeRecipeError(w, err, "recipe create content failed")
		return
	}
	if err := tx.Commit(); err != nil {
		log.Printf("recipe create commit failed error=%v", err)
		writeJSONError(w, http.StatusInternalServerError, "Não foi possível salvar a ficha técnica.")
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"id": id, "version": int64(1)})
}

func (a *app) handleUpdateRecipe(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var req recipeRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	normalizeRecipeRequest(&req)
	if msg := recipeRequestProblem(req); msg != "" {
		writeJSONError(w, http.StatusBadRequest, msg)
		return
	}
	if req.Version <= 0 {
		writeJSONError(w, http.StatusBadRequest, "Atualize a página e tente de novo.")
		return
	}
	active := true
	if req.Active != nil {
		active = *req.Active
	}
	ctx := r.Context()
	tx, err := a.db.BeginTx(ctx, nil)
	if err != nil {
		log.Printf("recipe update begin failed id=%d error=%v", id, err)
		writeJSONError(w, http.StatusInternalServerError, "Não foi possível salvar a ficha técnica.")
		return
	}
	defer func() { _ = tx.Rollback() }()
	var newVersion int64
	err = tx.QueryRowContext(ctx, `
UPDATE recipes SET name=$1,portions=$2,notes=NULLIF($3,''),active=$4,version=version+1
WHERE id=$5 AND version=$6
RETURNING version`, req.Name, req.Portions, req.Notes, active, id, req.Version).Scan(&newVersion)
	if errors.Is(err, sql.ErrNoRows) {
		var exists bool
		if qerr := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM recipes WHERE id=$1)`, id).Scan(&exists); qerr != nil {
			writeJSONError(w, http.StatusInternalServerError, "Não foi possível salvar a ficha técnica.")
			return
		}
		if !exists {
			writeJSONError(w, http.StatusNotFound, "Ficha técnica não encontrada.")
			return
		}
		writeConcurrentEditError(w)
		return
	}
	if err != nil {
		writeRecipeError(w, err, "recipe update failed")
		return
	}
	if err := replaceRecipeContent(ctx, tx, id, req); err != nil {
		writeRecipeError(w, err, "recipe update content failed")
		return
	}
	if err := tx.Commit(); err != nil {
		log.Printf("recipe update commit failed id=%d error=%v", id, err)
		writeJSONError(w, http.StatusInternalServerError, "Não foi possível salvar a ficha técnica.")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"id": id, "version": newVersion})
}
