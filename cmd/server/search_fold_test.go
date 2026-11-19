package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"path/filepath"
	"testing"
)

// Busca por nome ignora acentos e maiúsculas: "acu" precisa sugerir "Açúcar"
// e "cafe" precisa sugerir "Café", nos dois sentidos.
func TestSearchIgnoresAccentsAndCase(t *testing.T) {
	db := openTestDatabase(t)
	if err := runMigrations(context.Background(), db, filepath.Join("..", "..", "migrations")); err != nil {
		t.Fatal(err)
	}
	a := &app{db: db}
	for _, name := range []string{"Zuzuaçúcar cristal", "ZUZUCAFÉ em pó", "Zuzupao frances"} {
		var item int64
		if err := db.QueryRow(`INSERT INTO inventory_items(name,category_id,unit_code)
 VALUES($1,(SELECT id FROM categories WHERE name='Alimentos'),'UN') RETURNING id`, name).Scan(&item); err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(`INSERT INTO product_skus(item_id,brand_id,content_quantity,active) VALUES($1,0,1,true)`, item); err != nil {
			t.Fatal(err)
		}
	}
	names := func(h http.HandlerFunc, path, q, field string) []string {
		t.Helper()
		rr := crateTestRequest(t, h, "GET", path+"?q="+url.QueryEscape(q), nil, context.Background())
		if rr.Code != http.StatusOK {
			t.Fatalf("%s q=%q: %d %s", path, q, rr.Code, rr.Body.String())
		}
		var out []map[string]any
		if err := json.Unmarshal(rr.Body.Bytes(), &out); err != nil {
			t.Fatal(err)
		}
		var got []string
		for _, r := range out {
			got = append(got, r[field].(string))
		}
		return got
	}
	cases := []struct{ q, want string }{
		{"zuzuacu", "Zuzuaçúcar cristal"},
		{"ZUZUAÇÚ", "Zuzuaçúcar cristal"},
		{"zuzucafe", "ZUZUCAFÉ em pó"},
		{"zuzupão", "Zuzupao frances"},
	}
	for _, c := range cases {
		for _, h := range []struct {
			fn          http.HandlerFunc
			path, field string
		}{
			{a.handleLookup, "/api/products/lookup", "item"},
			{a.handleMaterialItems, "/api/material-requests/items", "name"},
		} {
			got := names(h.fn, h.path, c.q, h.field)
			if len(got) != 1 || got[0] != c.want {
				t.Fatalf("%s q=%q: got %v, want [%s]", h.path, c.q, got, c.want)
			}
		}
	}
}
