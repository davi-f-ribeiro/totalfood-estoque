package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

const materialPageSize = 25

type materialListFilter struct {
	query, number, unit, status, service string
	dateField, dateFrom, dateTo          string
	order                                string
	page                                 int
}

func readMaterialListFilter(r *http.Request) (materialListFilter, error) {
	q := r.URL.Query()
	filter := materialListFilter{
		query: strings.TrimSpace(q.Get("q")), unit: strings.TrimSpace(q.Get("unit")),
		status: q.Get("status"), service: q.Get("service"), dateField: q.Get("date_field"),
		dateFrom: q.Get("from"), dateTo: q.Get("to"), page: 1,
	}
	if len([]rune(filter.query)) > 120 || len([]rune(filter.unit)) > 120 {
		return filter, matBad("Use até 120 caracteres para buscar ou filtrar por unidade.")
	}
	if id, err := strconv.ParseInt(strings.TrimPrefix(filter.query, "#"), 10, 64); err == nil && id > 0 {
		filter.number = strconv.FormatInt(id, 10)
	}
	switch filter.status {
	case "", "PENDENTE", "EM_SEPARACAO", "ENTREGA_PARCIAL", "ENTREGUE",
		"RECEBIDA_COM_DIVERGENCIA", "RECEBIDA", "CANCELADA", "ENCERRADA":
	default:
		return filter, matBad("Selecione uma situação válida.")
	}
	switch filter.service {
	case "", "cafe", "almoco", "lanche", "jantar":
	default:
		return filter, matBad("Selecione um serviço válido.")
	}
	switch filter.dateField {
	case "", "production":
		filter.dateField = "production"
	case "delivery":
	default:
		return filter, matBad("Selecione qual data deseja filtrar.")
	}
	for _, date := range []string{filter.dateFrom, filter.dateTo} {
		if date == "" {
			continue
		}
		if parsed, err := time.Parse(time.DateOnly, date); err != nil || parsed.Year() < 1 {
			return filter, matBad("Informe uma data válida.")
		}
	}
	if filter.dateFrom != "" && filter.dateTo != "" && filter.dateFrom > filter.dateTo {
		return filter, matBad("A data inicial deve ser anterior ou igual à data final.")
	}
	switch q.Get("sort") {
	case "", "newest":
		filter.order = "created_at DESC, id DESC"
	case "oldest":
		filter.order = "created_at ASC, id ASC"
	case "production":
		filter.order = "production_date ASC, id ASC"
	case "number":
		filter.order = "id DESC"
	default:
		return filter, matBad("Selecione uma ordenação válida.")
	}
	if raw := q.Get("page"); raw != "" {
		page, err := strconv.Atoi(raw)
		if err != nil || page < 1 || page > 1_000_000 {
			return filter, matBad("Informe um número de página válido.")
		}
		filter.page = page
	}
	return filter, nil
}

func (a *app) handleMaterialListPage(w http.ResponseWriter, r *http.Request) {
	filter, err := readMaterialListFilter(r)
	if err != nil {
		a.matRespond(w, err)
		return
	}
	ctx := r.Context()
	actor := userFromContext(ctx).ID
	all, err := a.hasPermission(ctx, actor, "material_fulfillment", "write")
	if err != nil {
		a.matRespond(w, err)
		return
	}

	// Escopo, opções, contagem e página saem do mesmo snapshot. A ordem vem
	// exclusivamente das opções fixas em readMaterialListFilter.
	const accents = "'áàâãäéèêëíìîïóòôõöúùûüç','aaaaaeeeeiiiiooooouuuuc'"
	const query = `
WITH scoped AS (
	SELECT id, unit_name, delivery_date, production_date, service, status, created_at, created_by,
	       CASE WHEN closed_at IS NOT NULL THEN 'ENCERRADA' ELSE status END AS list_status
	FROM material_requests
	WHERE ($1::boolean OR created_by = $2)
), search AS (
	SELECT translate(lower($3::text), ` + accents + `) AS term
), filtered AS (
	SELECT s.*
	FROM scoped s CROSS JOIN search
	CROSS JOIN LATERAL (
		SELECT CASE WHEN $8 = 'delivery' THEN s.delivery_date ELSE s.production_date END AS date
	) dates
	WHERE ($5 = '' OR s.unit_name = $5)
	  AND ($6 = '' OR s.list_status = $6)
	  AND ($7 = '' OR s.service = $7)
	  AND ($9 = '' OR dates.date >= NULLIF($9, '')::date)
	  AND ($10 = '' OR dates.date <= NULLIF($10, '')::date)
	  AND (search.term = ''
	       OR ($4 <> '' AND s.id::text = $4)
	       OR ($4 = '' AND (
	          strpos(translate(lower(concat_ws(' ', s.unit_name, s.service, CASE s.service
	              WHEN 'cafe' THEN 'Café da manhã' WHEN 'almoco' THEN 'Almoço'
	              WHEN 'lanche' THEN 'Lanche' WHEN 'jantar' THEN 'Jantar' END)), ` + accents + `), search.term) > 0
	          OR EXISTS (SELECT 1 FROM material_request_lines l JOIN inventory_items i ON i.id = l.item_id
	                     WHERE l.request_id = s.id
	                       AND strpos(translate(lower(i.name), ` + accents + `), search.term) > 0)
	       )))
), page AS (
	SELECT * FROM filtered ORDER BY %s LIMIT $11 OFFSET $12
)
SELECT jsonb_build_object(
	'scope', CASE WHEN $1 THEN 'all' ELSE 'own' END,
	'page_size', $11::integer,
	'total', (SELECT count(*) FROM scoped),
	'filtered', (SELECT count(*) FROM filtered),
	'units', ARRAY(SELECT DISTINCT unit_name FROM scoped ORDER BY unit_name),
	'requests', COALESCE((
		SELECT jsonb_agg(jsonb_build_object(
			'id', id, 'unit_name', unit_name, 'delivery_date', delivery_date,
			'production_date', production_date, 'service', service, 'status', list_status,
			'items', (SELECT count(*) FROM material_request_lines WHERE request_id = page.id)
		) ORDER BY %s)
		FROM page
	), '[]'::jsonb)
)`
	var payload json.RawMessage
	err = a.db.QueryRowContext(ctx, fmt.Sprintf(query, filter.order, filter.order),
		all, actor, filter.query, filter.number, filter.unit, filter.status, filter.service,
		filter.dateField, filter.dateFrom, filter.dateTo, materialPageSize, (filter.page-1)*materialPageSize,
	).Scan(&payload)
	if err != nil {
		a.matRespond(w, err)
		return
	}
	writeJSON(w, 200, payload)
}
