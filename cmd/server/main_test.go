package main

import (
	"context"
	"database/sql"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestHealthReturnsSuccessWithDatabaseAvailable(t *testing.T) {
	db := openTestDatabase(t)
	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	rec := httptest.NewRecorder()

	(&app{db: db}).health(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected %d, got %d: %q", http.StatusOK, rec.Code, rec.Body.String())
	}
	if rec.Body.String() != "ok\n" {
		t.Fatalf("unexpected response body: %q", rec.Body.String())
	}
}

func TestHealthDoesNotReturnSuccessWithDatabaseUnavailable(t *testing.T) {
	db := openTestDatabase(t)
	if err := db.Close(); err != nil {
		t.Fatalf("close db: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	rec := httptest.NewRecorder()

	(&app{db: db}).health(rec, req)

	if rec.Code == http.StatusOK {
		t.Fatalf("expected non-success status when DB is unavailable")
	}
}

func TestMigrationsExecute(t *testing.T) {
	db := openTestDatabase(t)
	dir := t.TempDir()
	migration := filepath.Join(dir, "001_check.sql")
	if err := os.WriteFile(migration, []byte("CREATE TABLE IF NOT EXISTS foundation_check (id integer PRIMARY KEY);"), 0o600); err != nil {
		t.Fatalf("write migration: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := runMigrations(ctx, db, dir); err != nil {
		t.Fatalf("run migrations: %v", err)
	}

	var exists bool
	if err := db.QueryRowContext(ctx, "SELECT EXISTS (SELECT 1 FROM schema_migrations WHERE version = $1)", "001_check").Scan(&exists); err != nil {
		t.Fatalf("query schema_migrations: %v", err)
	}
	if !exists {
		t.Fatalf("migration version was not recorded")
	}
}

func TestCoreSchemaSupportsOperationalModel(t *testing.T) {
	db := openTestDatabase(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := runMigrations(ctx, db, filepath.Join("..", "..", "migrations")); err != nil {
		t.Fatalf("run core migrations on empty schema: %v", err)
	}

	assertCount(t, db, "SELECT count(*) FROM warehouses", 4)
	assertCount(t, db, "SELECT count(*) FROM units", 8)
	assertCount(t, db, "SELECT count(*) FROM categories", 5)

	var categoryID, itemID, skuAID, skuBID, warehouseID, assetTypeID int64
	if err := db.QueryRowContext(ctx, "SELECT id FROM categories WHERE name = $1", "Alimentos").Scan(&categoryID); err != nil {
		t.Fatalf("select category: %v", err)
	}
	if err := db.QueryRowContext(ctx, "SELECT id FROM warehouses WHERE name = $1", "VR").Scan(&warehouseID); err != nil {
		t.Fatalf("select warehouse: %v", err)
	}
	if err := db.QueryRowContext(ctx, "INSERT INTO inventory_items (name, category_id, unit_code, minimum_stock) VALUES ($1, $2, $3, $4) RETURNING id", "Arroz Parboilizado", categoryID, "KG", 10).Scan(&itemID); err != nil {
		t.Fatalf("insert generic item: %v", err)
	}
	if err := db.QueryRowContext(ctx, "INSERT INTO product_skus (item_id, brand, barcode) VALUES ($1, $2, $3) RETURNING id", itemID, "Panelaço", "7890000000011").Scan(&skuAID); err != nil {
		t.Fatalf("insert first sku: %v", err)
	}
	if err := db.QueryRowContext(ctx, "INSERT INTO product_skus (item_id, brand, barcode) VALUES ($1, $2, $3) RETURNING id", itemID, "Namorado", "7890000000028").Scan(&skuBID); err != nil {
		t.Fatalf("insert second sku for same generic item: %v", err)
	}
	if skuAID == skuBID {
		t.Fatalf("expected different SKUs for same generic item")
	}

	if _, err := db.ExecContext(ctx, "INSERT INTO product_skus (item_id, brand, barcode) VALUES ($1, $2, $3)", itemID, "Duplicado", "7890000000011"); err == nil {
		t.Fatalf("expected duplicate barcode to be rejected")
	}

	insertValidEntry(t, ctx, db, skuAID, warehouseID)

	if err := insertInvalidQuantity(ctx, db, skuAID, warehouseID); err == nil {
		t.Fatalf("expected invalid quantity to be rejected")
	}
	if _, err := db.ExecContext(ctx, "INSERT INTO product_skus (item_id, brand) VALUES ($1, $2)", int64(999999), "FK inválida"); err == nil {
		t.Fatalf("expected invalid FK to be rejected")
	}

	if err := db.QueryRowContext(ctx, "INSERT INTO asset_types (name, unit_code) VALUES ($1, $2) RETURNING id", "Cadeira de plástico preta", "UN").Scan(&assetTypeID); err != nil {
		t.Fatalf("insert asset type: %v", err)
	}
	if _, err := db.ExecContext(ctx, "INSERT INTO assets (asset_type_id, tag, status, current_warehouse_id, acquisition_unit_cost) VALUES ($1, $2, $3, $4, $5), ($1, $6, $3, $4, $5)", assetTypeID, "PAT-000001", "ATIVO", warehouseID, 25.50, "PAT-000002"); err != nil {
		t.Fatalf("insert two assets for same type: %v", err)
	}
	assertCount(t, db, "SELECT count(*) FROM assets WHERE asset_type_id = $1", 2, assetTypeID)

	insertValidTransfer(t, ctx, db, skuAID)
}

func TestServerStarts(t *testing.T) {
	db := openTestDatabase(t)
	srv := newHTTPServer("127.0.0.1:0", &app{db: db})
	if srv.ReadHeaderTimeout == 0 || srv.ReadTimeout == 0 || srv.WriteTimeout == 0 || srv.IdleTimeout == 0 || srv.MaxHeaderBytes == 0 {
		t.Fatalf("server limits/timeouts are not configured")
	}

	ln, err := net.Listen("tcp", srv.Addr)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	errCh := make(chan error, 1)
	go func() {
		if err := srv.Serve(ln); err != nil && err != http.ErrServerClosed {
			errCh <- err
			return
		}
		errCh <- nil
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		t.Fatalf("shutdown: %v", err)
	}
	if err := <-errCh; err != nil {
		t.Fatalf("serve: %v", err)
	}
}

func insertValidEntry(t *testing.T, ctx context.Context, db *sql.DB, skuID, warehouseID int64) {
	t.Helper()
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("begin entry: %v", err)
	}
	defer tx.Rollback()
	var movementID int64
	if err := tx.QueryRowContext(ctx, "INSERT INTO movements (movement_type, document_number, supplier_name) VALUES ($1, $2, $3) RETURNING id", "ENTRADA", "NF-1", "Fornecedor").Scan(&movementID); err != nil {
		t.Fatalf("insert movement: %v", err)
	}
	if _, err := tx.ExecContext(ctx, "INSERT INTO movement_lines (movement_id, sku_id, warehouse_id, direction, quantity, unit_cost, lot_code) VALUES ($1, $2, $3, $4, $5, $6, $7)", movementID, skuID, warehouseID, "IN", 5, 12.34, "LOTE-1"); err != nil {
		t.Fatalf("insert movement line: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit valid entry: %v", err)
	}
}

func insertInvalidQuantity(ctx context.Context, db *sql.DB, skuID, warehouseID int64) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var movementID int64
	if err := tx.QueryRowContext(ctx, "INSERT INTO movements (movement_type) VALUES ($1) RETURNING id", "ENTRADA").Scan(&movementID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, "INSERT INTO movement_lines (movement_id, sku_id, warehouse_id, direction, quantity) VALUES ($1, $2, $3, $4, $5)", movementID, skuID, warehouseID, "IN", 0); err != nil {
		return err
	}
	return tx.Commit()
}

func insertValidTransfer(t *testing.T, ctx context.Context, db *sql.DB, skuID int64) {
	t.Helper()
	var fromWarehouseID, toWarehouseID int64
	if err := db.QueryRowContext(ctx, "SELECT id FROM warehouses WHERE name = $1", "VR").Scan(&fromWarehouseID); err != nil {
		t.Fatalf("select source warehouse: %v", err)
	}
	if err := db.QueryRowContext(ctx, "SELECT id FROM warehouses WHERE name = $1", "Totalfood Estoque Seco").Scan(&toWarehouseID); err != nil {
		t.Fatalf("select target warehouse: %v", err)
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("begin transfer: %v", err)
	}
	defer tx.Rollback()
	var movementID int64
	if err := tx.QueryRowContext(ctx, "INSERT INTO movements (movement_type) VALUES ($1) RETURNING id", "TRANSFERENCIA").Scan(&movementID); err != nil {
		t.Fatalf("insert transfer movement: %v", err)
	}
	if _, err := tx.ExecContext(ctx, "INSERT INTO movement_lines (movement_id, sku_id, warehouse_id, direction, quantity) VALUES ($1, $2, $3, $4, $5), ($1, $2, $6, $7, $5)", movementID, skuID, fromWarehouseID, "OUT", 2, toWarehouseID, "IN"); err != nil {
		t.Fatalf("insert transfer lines: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit valid transfer: %v", err)
	}
}

func assertCount(t *testing.T, db *sql.DB, query string, want int, args ...any) {
	t.Helper()
	var got int
	if err := db.QueryRow(query, args...).Scan(&got); err != nil {
		t.Fatalf("count query failed: %v", err)
	}
	if got != want {
		t.Fatalf("expected count %d, got %d for %s", want, got, query)
	}
}

func openTestDatabase(t *testing.T) *sql.DB {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL is required for PostgreSQL integration check")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	db, err := openDatabase(ctx, config{DatabaseURL: dsn, MaxOpenConns: 10, MaxIdleConns: 5, ConnMaxLife: 30 * time.Minute, ConnMaxIdle: 5 * time.Minute})
	if err != nil {
		t.Fatalf("open test database: %v", err)
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)

	schemaName := fmt.Sprintf("test_%d", time.Now().UnixNano())
	if _, err := db.ExecContext(ctx, "CREATE SCHEMA "+schemaName); err != nil {
		db.Close()
		t.Fatalf("create test schema: %v", err)
	}
	if _, err := db.ExecContext(ctx, "SET search_path TO "+schemaName); err != nil {
		db.Close()
		t.Fatalf("set search_path: %v", err)
	}
	t.Cleanup(func() {
		_, _ = db.Exec("DROP SCHEMA IF EXISTS " + schemaName + " CASCADE")
		_ = db.Close()
	})
	if strings.Contains(schemaName, ";") {
		t.Fatalf("invalid generated schema name")
	}
	return db
}
