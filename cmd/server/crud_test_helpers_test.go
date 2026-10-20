package main

import (
	"fmt"
	"sync/atomic"
	"testing"
)

var testOperationSeq atomic.Int64

// testOperationID gera um identificador de envio único para criações de cadastro.
func testOperationID() string {
	return fmt.Sprintf("test-op-%d", testOperationSeq.Add(1))
}

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


func createTestBrand(t *testing.T, a *app, name string) int64 {
	t.Helper()
	var id int64
	if err := a.db.QueryRow(`INSERT INTO brands(name) VALUES ($1) RETURNING id`, name).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}
