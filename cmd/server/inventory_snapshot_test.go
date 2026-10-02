package main

import (
	"context"
	"encoding/json"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func (e inventoryTestEnv) movementDecimal(t *testing.T, skuID, warehouseID int64, direction, qty string) {
	t.Helper()
	ctx := context.Background()
	tx, err := e.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	typ := "ENTRADA"
	if direction == "OUT" {
		typ = "SAIDA"
	}
	var movementID int64
	if err := tx.QueryRowContext(ctx,
		`INSERT INTO movements (movement_type,user_id) VALUES ($1,$2) RETURNING id`,
		typ, e.userID).Scan(&movementID); err != nil {
		t.Fatalf("movement header: %v", err)
	}
	var movementLineID int64
	if err := tx.QueryRowContext(ctx,
		`INSERT INTO movement_lines (movement_id,sku_id,warehouse_id,direction,quantity,unit_cost)
		 VALUES ($1,$2,$3,$4,$5::numeric,1) RETURNING id`,
		movementID, skuID, warehouseID, direction, qty).Scan(&movementLineID); err != nil {
		t.Fatalf("movement line: %v", err)
	}
	if direction == "IN" {
		if err := createEntryLot(ctx, tx, movementLineID); err != nil {
			t.Fatalf("movement lot: %v", err)
		}
	} else if direction == "OUT" {
		if err := allocateLots(ctx, tx, movementLineID); err != nil {
			t.Fatalf("movement lot allocation: %v", err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("movement commit: %v", err)
	}
}

func inventorySnapshotQuantities(t *testing.T, db *sql.DB, validationID int64) map[int64]string {
	t.Helper()
	rows, err := db.Query(
		`SELECT sku_id, system_quantity_snapshot::text
		   FROM inventory_validation_lines
		  WHERE validation_id=$1
		  ORDER BY sku_id`, validationID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	out := map[int64]string{}
	for rows.Next() {
		var skuID int64
		var qty string
		if err := rows.Scan(&skuID, &qty); err != nil {
			t.Fatal(err)
		}
		out[skuID] = qty
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestInventorySnapshotBasicDecimalAndImmutable(t *testing.T) {
	e := newInventoryTestEnv(t)
	ctx := context.Background()
	wh := e.warehouse(t, "snapshot-basic", true)
	a := e.sku(t, "snapshot-a", true, true)
	b := e.sku(t, "snapshot-b", true, true)

	e.movementDecimal(t, a, wh, "IN", "10.125")
	e.movementDecimal(t, b, wh, "IN", "20.500")

	res, err := e.a.createTotalInventory(ctx, e.userID, wh)
	if err != nil {
		t.Fatalf("create total: %v", err)
	}
	before := inventorySnapshotQuantities(t, e.db, res.ID)
	if before[a] != "10.125" || before[b] != "20.500" {
		t.Fatalf("unexpected initial snapshots: %v", before)
	}

	var snapshotAt sql.NullTime
	if err := e.db.QueryRowContext(ctx,
		`SELECT snapshot_at FROM inventory_validations WHERE id=$1`, res.ID).Scan(&snapshotAt); err != nil {
		t.Fatal(err)
	}
	if !snapshotAt.Valid {
		t.Fatal("new inventory must persist snapshot_at")
	}

	e.movementDecimal(t, a, wh, "OUT", "3.125")
	after := inventorySnapshotQuantities(t, e.db, res.ID)
	if after[a] != before[a] || after[b] != before[b] {
		t.Fatalf("snapshot changed after later movement: before=%v after=%v", before, after)
	}

	current, err := getStockSnapshot(ctx, e.db, a, wh)
	if err != nil {
		t.Fatal(err)
	}
	if current.Quantity != "7.000" {
		t.Fatalf("current balance=%s want=7.000", current.Quantity)
	}
}

func TestBlindPersistsSnapshotWithoutOperationalLeak(t *testing.T) {
	e := newInventoryTestEnv(t)
	ctx := context.Background()
	wh := e.warehouse(t, "blind-snapshot", true)
	for i := 0; i < 4; i++ {
		sku := e.sku(t, fmt.Sprintf("blind-snapshot-%d", i), true, true)
		e.movementDecimal(t, sku, wh, "IN", fmt.Sprintf("%d.250", i+1))
	}

	res, err := e.a.createBlindInventory(ctx, e.userID)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.SKUIDs) != 2 {
		t.Fatalf("sample=%v", res.SKUIDs)
	}
	for _, skuID := range res.SKUIDs {
		var qty sql.NullString
		if err := e.db.QueryRowContext(ctx,
			`SELECT system_quantity_snapshot::text
			   FROM inventory_validation_lines
			  WHERE validation_id=$1 AND sku_id=$2`,
			res.ID, skuID).Scan(&qty); err != nil {
			t.Fatal(err)
		}
		if !qty.Valid || qty.String == "" {
			t.Fatalf("BLIND line %d has no snapshot", skuID)
		}
	}

	loaded, err := e.a.getInventoryValidation(ctx, res.ID)
	if err != nil {
		t.Fatal(err)
	}
	if fmt.Sprint(loaded.SKUIDs) != fmt.Sprint(res.SKUIDs) {
		t.Fatalf("selection changed on read: created=%v loaded=%v", res.SKUIDs, loaded.SKUIDs)
	}
	payload, err := json.Marshal(loaded)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(payload), "system_quantity_snapshot") ||
		strings.Contains(string(payload), "snapshot_at") {
		t.Fatalf("BLIND operational payload leaked snapshot data: %s", payload)
	}
}

func openInventorySecondConnection(t *testing.T, primary *sql.DB) *sql.DB {
	t.Helper()
	var schema string
	if err := primary.QueryRow(`SELECT current_schema()`).Scan(&schema); err != nil {
		t.Fatalf("current schema: %v", err)
	}
	if !strings.HasPrefix(schema, "test_") {
		t.Fatalf("refusing unexpected schema %q", schema)
	}
	for _, r := range strings.TrimPrefix(schema, "test_") {
		if r < '0' || r > '9' {
			t.Fatalf("unsafe generated schema %q", schema)
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	db, err := openDatabase(ctx, config{
		DatabaseURL: os.Getenv("TEST_DATABASE_URL"),
		MaxOpenConns: 1,
		MaxIdleConns: 1,
		ConnMaxLife:  30 * time.Minute,
		ConnMaxIdle:  5 * time.Minute,
	})
	if err != nil {
		t.Fatalf("open second db: %v", err)
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	if _, err := db.ExecContext(ctx, "SET search_path TO "+schema); err != nil {
		db.Close()
		t.Fatalf("set second search_path: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func commitConcurrentExit(t *testing.T, db *sql.DB, userID, warehouseID int64, lines map[int64]string) {
	t.Helper()
	ctx := context.Background()
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	var movementID int64
	if err := tx.QueryRowContext(ctx,
		`INSERT INTO movements (movement_type,user_id) VALUES ('SAIDA',$1) RETURNING id`,
		userID).Scan(&movementID); err != nil {
		t.Fatal(err)
	}
	for skuID, qty := range lines {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO movement_lines (movement_id,sku_id,warehouse_id,direction,quantity,unit_cost)
			 VALUES ($1,$2,$3,'OUT',$4::numeric,1)`,
			movementID, skuID, warehouseID, qty); err != nil {
			t.Fatal(err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
}

func TestInventoryRepeatableReadSnapshotBeforeConcurrentMovement(t *testing.T) {
	e := newInventoryTestEnv(t)
	ctx := context.Background()
	wh := e.warehouse(t, "mvcc-before", true)
	a := e.sku(t, "mvcc-a", true, true)
	b := e.sku(t, "mvcc-b", true, true)
	e.movementDecimal(t, a, wh, "IN", "10.000")
	e.movementDecimal(t, b, wh, "IN", "20.000")

	second := openInventorySecondConnection(t, e.db)

	tx, snapshotAt, err := e.a.beginInventorySnapshotTx(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if err := lockInventorySelection(ctx, tx); err != nil {
		t.Fatal(err)
	}

	done := make(chan error, 1)
	go func() {
		tx2, err := second.BeginTx(context.Background(), nil)
		if err != nil {
			done <- err
			return
		}
		defer tx2.Rollback()
		var movementID int64
		if err := tx2.QueryRow(
			`INSERT INTO movements (movement_type,user_id) VALUES ('SAIDA',$1) RETURNING id`,
			e.userID).Scan(&movementID); err != nil {
			done <- err
			return
		}
		if _, err := tx2.Exec(
			`INSERT INTO movement_lines (movement_id,sku_id,warehouse_id,direction,quantity,unit_cost)
			 VALUES ($1,$2,$3,'OUT',3.000,1),($1,$4,$3,'OUT',5.000,1)`,
			movementID, a, wh, b); err != nil {
			done <- err
			return
		}
		done <- tx2.Commit()
	}()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("concurrent movement failed: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("concurrent movement was blocked by inventory snapshot")
	}

	rows, err := inventoryEligibility(ctx, tx, wh)
	if err != nil {
		t.Fatal(err)
	}
	lines := make([]inventorySnapshotLine, 0, 2)
	for _, row := range rows {
		if row.Eligible && (row.SKUID == a || row.SKUID == b) {
			lines = append(lines, inventorySnapshotLine{SKUID: row.SKUID, Quantity: row.Balance})
		}
	}
	if len(lines) != 2 {
		t.Fatalf("snapshot view lost eligible SKUs: %+v", rows)
	}
	res, err := persistInventorySelection(ctx, tx, "TOTAL", e.userID, wh, snapshotAt, lines, "", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}

	snapshots := inventorySnapshotQuantities(t, e.db, res.ID)
	if snapshots[a] != "10.000" || snapshots[b] != "20.000" {
		t.Fatalf("hybrid or post-movement snapshot: %v", snapshots)
	}
	currentA, err := getStockSnapshot(ctx, e.db, a, wh)
	if err != nil {
		t.Fatal(err)
	}
	currentB, err := getStockSnapshot(ctx, e.db, b, wh)
	if err != nil {
		t.Fatal(err)
	}
	if currentA.Quantity != "7.000" || currentB.Quantity != "15.000" {
		t.Fatalf("operational balances not updated: A=%s B=%s", currentA.Quantity, currentB.Quantity)
	}
}

func TestInventorySnapshotAfterCommittedMovementUsesNewBalance(t *testing.T) {
	e := newInventoryTestEnv(t)
	ctx := context.Background()
	wh := e.warehouse(t, "mvcc-after", true)
	sku := e.sku(t, "mvcc-after-sku", true, true)
	e.movementDecimal(t, sku, wh, "IN", "10.000")
	e.movementDecimal(t, sku, wh, "OUT", "3.000")

	res, err := e.a.createTotalInventory(ctx, e.userID, wh)
	if err != nil {
		t.Fatal(err)
	}
	snapshots := inventorySnapshotQuantities(t, e.db, res.ID)
	if snapshots[sku] != "7.000" {
		t.Fatalf("snapshot=%s want=7.000", snapshots[sku])
	}
}

func TestSnapshotMigration015CleanAndIncremental(t *testing.T) {
	if os.Getenv("TEST_DATABASE_URL") == "" {
		t.Skip("TEST_DATABASE_URL is required")
	}
	db := openTestDatabase(t)
	ctx := context.Background()
	migrationsDir := filepath.Join("..", "..", "migrations")

	baseDir := t.TempDir()
	for i := 1; i <= 14; i++ {
		pattern := filepath.Join(migrationsDir, fmt.Sprintf("%03d_*.sql", i))
		matches, err := filepath.Glob(pattern)
		if err != nil || len(matches) != 1 {
			t.Fatalf("resolve migration %03d: matches=%v err=%v", i, matches, err)
		}
		body, err := os.ReadFile(matches[0])
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(baseDir, filepath.Base(matches[0])), body, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := runMigrations(ctx, db, baseDir); err != nil {
		t.Fatalf("apply 001-014: %v", err)
	}

	incrementalDir := t.TempDir()
	body, err := os.ReadFile(filepath.Join(migrationsDir, "015_inventory_snapshot_timestamp.sql"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		filepath.Join(incrementalDir, "015_inventory_snapshot_timestamp.sql"),
		body, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := runMigrations(ctx, db, incrementalDir); err != nil {
		t.Fatalf("apply 015 incrementally: %v", err)
	}

	var snapshotAtExists bool
	if err := db.QueryRowContext(ctx, `
SELECT EXISTS (
    SELECT 1
      FROM information_schema.columns
     WHERE table_schema=current_schema()
       AND table_name='inventory_validations'
       AND column_name='snapshot_at'
)`).Scan(&snapshotAtExists); err != nil {
		t.Fatal(err)
	}
	if !snapshotAtExists {
		t.Fatal("migration 015 did not create snapshot_at")
	}
}


func TestInventorySnapshotDoesNotBlockOperationalExit(t *testing.T) {
	e := newInventoryTestEnv(t)
	ctx := context.Background()
	wh := e.warehouse(t, "nonblocking-engine", true)
	sku := e.sku(t, "nonblocking-engine-sku", true, true)
	e.movementDecimal(t, sku, wh, "IN", "10.000")

	second := openInventorySecondConnection(t, e.db)
	tx, _, err := e.a.beginInventorySnapshotTx(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if err := lockInventorySelection(ctx, tx); err != nil {
		t.Fatal(err)
	}

	done := make(chan error, 1)
	go func() {
		app2 := &app{db: second}
		_, err := app2.createExit(context.Background(), movementRequest{
			OperationID: "inventory-nonblocking-exit-" + t.Name(),
			SKUID:       sku,
			WarehouseID: wh,
			Quantity:    "3.000",
			UserID:      e.userID,
		}, "inventory-nonblocking-hash")
		done <- err
	}()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("operational createExit failed while snapshot was open: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("operational createExit was blocked by inventory snapshot")
	}

	rows, err := inventoryEligibility(ctx, tx, wh)
	if err != nil {
		t.Fatal(err)
	}
	var snapshotQty string
	for _, row := range rows {
		if row.SKUID == sku {
			snapshotQty = row.Balance
		}
	}
	if snapshotQty != "10.000" {
		t.Fatalf("repeatable-read view changed after operational exit: %s", snapshotQty)
	}
}
