package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"path/filepath"
	"testing"
)

// Caixas só aceita KG. Com 12 produtos em UN antes do único em KG na ordem
// alfabética, filtrar depois do limite de 10 escondia o produto certo; o
// filtro unit=KG precisa ser aplicado no banco.
func TestProductLookupFiltersUnitBeforeLimit(t *testing.T) {
	db := openTestDatabase(t)
	if err := runMigrations(context.Background(), db, filepath.Join("..", "..", "migrations")); err != nil {
		t.Fatal(err)
	}
	a := &app{db: db}
	add := func(name, unit string) {
		t.Helper()
		var item int64
		if err := db.QueryRow(`INSERT INTO inventory_items(name,category_id,unit_code)
 VALUES($1,(SELECT id FROM categories WHERE name='Alimentos'),$2) RETURNING id`, name, unit).Scan(&item); err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(`INSERT INTO product_skus(item_id,brand_id,content_quantity,active) VALUES($1,0,1,true)`, item); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < 12; i++ {
		add(fmt.Sprintf("Zuzubatata palha %02d", i), "UN")
	}
	add("Zuzubatata inglesa zz", "KG")
	lookup := func(query string) (int, []map[string]any) {
		rr := crateTestRequest(t, a.handleLookup, "GET", "/api/products/lookup?"+query, nil, context.Background())
		var out []map[string]any
		if rr.Code == http.StatusOK {
			if err := json.Unmarshal(rr.Body.Bytes(), &out); err != nil {
				t.Fatal(err)
			}
		}
		return rr.Code, out
	}
	if code, rows := lookup("q=Zuzubatata"); code != 200 || len(rows) != 10 {
		t.Fatalf("unfiltered lookup: %d, %d rows", code, len(rows))
	}
	code, rows := lookup("q=Zuzubatata&unit=kg")
	if code != 200 || len(rows) != 1 || rows[0]["item"] != "Zuzubatata inglesa zz" || rows[0]["unit"] != "KG" {
		t.Fatalf("unit=KG lookup: %d %v", code, rows)
	}
	if code, _ := lookup("q=Zuzubatata&unit=K%27G"); code != http.StatusBadRequest {
		t.Fatalf("invalid unit accepted: %d", code)
	}
}
