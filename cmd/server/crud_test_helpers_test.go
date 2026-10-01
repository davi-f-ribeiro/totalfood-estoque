package main

import "testing"

func crudVersion(t *testing.T, a *app, table string, id int64) int64 {
	t.Helper()
	var query string
	switch table {
	case "inventory_items":
		query = "SELECT version FROM inventory_items WHERE id=$1"
	case "product_skus":
		query = "SELECT version FROM product_skus WHERE id=$1"
	case "users":
		query = "SELECT version FROM users WHERE id=$1"
	case "profiles":
		query = "SELECT version FROM profiles WHERE id=$1"
	case "assets":
		query = "SELECT version FROM assets WHERE id=$1"
	default:
		t.Fatalf("unsupported CRUD version table %q", table)
	}
	var version int64
	if err := a.db.QueryRow(query, id).Scan(&version); err != nil {
		t.Fatalf("version lookup %s/%d: %v", table, id, err)
	}
	return version
}
