package main

import (
	"context"
	"net/http/httptest"
	"path/filepath"
	"testing"
)

// Erros reais do PostgreSQL classificados pelo código e pelo nome exato.
func TestDBErrorClassification(t *testing.T) {
	db := openTestDatabase(t)
	if err := runMigrations(context.Background(), db, filepath.Join("..", "..", "migrations")); err != nil {
		t.Fatal(err)
	}
	var typeID, wh int64
	if err := db.QueryRow(`INSERT INTO asset_types(name) VALUES ('Caixa térmica teste') RETURNING id`).Scan(&typeID); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT id FROM warehouses WHERE active ORDER BY id LIMIT 1`).Scan(&wh); err != nil {
		t.Fatal(err)
	}
	insert := func(tag string) error {
		_, err := db.Exec(`INSERT INTO assets(asset_type_id,tag,status,current_warehouse_id) VALUES ($1,$2,'ATIVO',$3)`, typeID, tag, wh)
		return err
	}
	if err := insert("PAT-1"); err != nil {
		t.Fatal(err)
	}
	duplicate := insert("PAT-1")
	blank := insert("   ")
	if !violates(duplicate, "assets_tag_key") || violates(duplicate, "assets_tag_check") {
		t.Fatalf("duplicate tag misclassified: %v", duplicate)
	}
	if !violates(blank, "assets_tag_check") || violates(blank, "assets_tag_key") {
		t.Fatalf("blank tag misclassified: %v", blank)
	}
	// Antes, "assets_tag" casava as duas e a plaqueta em branco virava "já cadastrada".
	for _, c := range []struct {
		err  error
		code int
	}{{duplicate, 409}, {blank, 400}} {
		rr := httptest.NewRecorder()
		writeAssetError(rr, c.err, "test")
		if rr.Code != c.code {
			t.Fatalf("writeAssetError(%v) = %d, wanted %d", c.err, rr.Code, c.code)
		}
	}

	_, fk := db.Exec(`INSERT INTO assets(asset_type_id,tag,status) VALUES (-1,'PAT-FK','BAIXADO')`)
	if !isForeignKeyViolation(fk) || isForeignKeyViolation(duplicate) {
		t.Fatalf("foreign key misclassified: %v", fk)
	}
	_, badDate := db.Exec(`SELECT '2026-02-31'::date`)
	if !isInvalidDate(badDate) || isInvalidDate(fk) {
		t.Fatalf("invalid date misclassified: %v", badDate)
	}
	_, raise := db.Exec(`DO $$ BEGIN RAISE EXCEPTION 'asset warehouse inactive or not found'; END $$`)
	if !raised(raise, "asset warehouse inactive or not found") || raised(raise, "asset warehouse") || raised(fk, "asset warehouse inactive or not found") {
		t.Fatalf("raise misclassified: %v", raise)
	}
	if violates(nil, "assets_tag_key") || isForeignKeyViolation(nil) || raised(nil, "x") || isInvalidDate(nil) {
		t.Fatal("nil error must not match")
	}
}
