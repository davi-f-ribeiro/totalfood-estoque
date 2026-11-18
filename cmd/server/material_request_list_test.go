package main

import (
	"fmt"
	"net/http"
	"net/url"
	"reflect"
	"testing"
)

func TestMaterialRequestListFiltersAndPermissions(t *testing.T) {
	a, sku, _, _ := newStockTestApp(t)
	owner := createTestUser(t, a, "filter-cozinha", "secret", "Cozinha", true)
	other := createTestUser(t, a, "filter-outra", "secret", "Cozinha", true)
	createTestUser(t, a, "filter-estoque", "secret", "Estoquista", true)
	kitchen := doLogin(t, a, "filter-cozinha", "secret")
	warehouse := doLogin(t, a, "filter-estoque", "secret")

	var item int64
	if err := a.db.QueryRow("SELECT item_id FROM product_skus WHERE id=$1", sku).Scan(&item); err != nil {
		t.Fatal(err)
	}
	makeRequest := func(n int, actor int64, unit, service, status string, closed bool) int64 {
		t.Helper()
		var id int64
		err := a.db.QueryRow(`
			INSERT INTO material_requests (operation_id, request_hash, unit_name, delivery_date,
				production_date, service, status, created_by, created_at, closed_by, closed_at)
			VALUES ($1, 'seed', $2, '2026-11-16', '2026-11-18', $3, $4, $5::bigint,
			        TIMESTAMPTZ '2026-11-12 10:00:00+00',
			        CASE WHEN $6::boolean THEN $5::bigint END, CASE WHEN $6 THEN now() END)
			RETURNING id`, fmt.Sprintf("filter-%d", n), unit, service, status, actor, closed).Scan(&id)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := a.db.Exec("INSERT INTO material_request_lines (request_id,item_id,quantity) VALUES ($1,$2,1)", id, item); err != nil {
			t.Fatal(err)
		}
		return id
	}
	first := makeRequest(0, owner, "Cozinha Central", "almoco", "PENDENTE", false)
	closed := makeRequest(1, owner, "Cozinha Central", "cafe", "RECEBIDA", true)
	for i := 2; i < materialPageSize; i++ {
		makeRequest(i, owner, "Cozinha Central", "almoco", "PENDENTE", false)
	}
	last := makeRequest(materialPageSize, other, "Nutrição", "cafe", "CANCELADA", false)

	get := func(params string, session loginData) map[string]any {
		t.Helper()
		rr := authReq(a, http.MethodGet, "/api/material-requests/search"+params, nil, session, "")
		if rr.Code != 200 {
			t.Fatalf("list %q got %d: %s", params, rr.Code, rr.Body.String())
		}
		return materialTestJSON(t, rr.Body.Bytes())
	}
	check := func(res map[string]any, scope string, total, filtered int, units []any, size int, firstID int64) {
		t.Helper()
		records := res["requests"].([]any)
		if res["scope"] != scope || res["total"] != float64(total) ||
			res["filtered"] != float64(filtered) || !reflect.DeepEqual(res["units"], units) ||
			len(records) != size || (size > 0 && records[0].(map[string]any)["id"] != float64(firstID)) {
			t.Fatalf("unexpected page: %+v", res)
		}
	}
	allUnits := []any{"Cozinha Central", "Nutrição"}
	ownUnits := []any{"Cozinha Central"}
	check(get("", warehouse), "all", materialPageSize+1, materialPageSize+1, allUnits, materialPageSize, last)
	check(get("?page=2", warehouse), "all", materialPageSize+1, materialPageSize+1, allUnits, 1, first)
	check(get("?sort=oldest", warehouse), "all", materialPageSize+1, materialPageSize+1, allUnits, materialPageSize, first)
	check(get("", kitchen), "own", materialPageSize, materialPageSize, ownUnits, materialPageSize, last-1)
	check(get("?q="+url.QueryEscape(fmt.Sprintf("#%d", last)), kitchen), "own", materialPageSize, 0, ownUnits, 0, 0)
	check(get("?q=nutricao&unit="+url.QueryEscape("Nutrição")+"&status=CANCELADA&service=cafe&date_field=delivery&from=2026-11-16&to=2026-11-16", warehouse),
		"all", materialPageSize+1, 1, allUnits, 1, last)
	check(get("?q="+url.QueryEscape("cafe da manha")+"&status=ENCERRADA&from=2026-11-18", kitchen),
		"own", materialPageSize, 1, ownUnits, 1, closed)
	check(get("?q=arroz&from=2026-11-18&to=2026-11-18", warehouse),
		"all", materialPageSize+1, materialPageSize+1, allUnits, materialPageSize, last)
	check(get("?q=arroz&date_field=delivery&from=2026-11-18", warehouse),
		"all", materialPageSize+1, 0, allUnits, 0, 0)
	for _, params := range []string{"?page=0", "?sort=whatever", "?from=2026-02-30", "?from=2026-11-19&to=2026-11-18"} {
		rr := authReq(a, http.MethodGet, "/api/material-requests/search"+params, nil, kitchen, "")
		if rr.Code != 400 {
			t.Fatalf("invalid filter %q got %d: %s", params, rr.Code, rr.Body.String())
		}
	}
}
