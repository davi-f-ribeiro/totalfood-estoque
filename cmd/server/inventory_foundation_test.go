package main

import (
    "context"
    "os"
    "testing"

    "github.com/jackc/pgx/v5/pgxpool"
)

func TestInventoryFoundationDoesNotCreateMovements(t *testing.T) {
    databaseURL := os.Getenv("TEST_DATABASE_URL")
    if databaseURL == "" {
        t.Skip("TEST_DATABASE_URL is not set")
    }

    ctx := context.Background()
    pool, err := pgxpool.New(ctx, databaseURL)
    if err != nil {
        t.Fatalf("connect test database: %v", err)
    }
    defer pool.Close()

    var movementsBefore, movementLinesBefore int64
    if err := pool.QueryRow(ctx, "SELECT count(*) FROM movements").Scan(&movementsBefore); err != nil {
        t.Fatalf("count movements before: %v", err)
    }
    if err := pool.QueryRow(ctx, "SELECT count(*) FROM movement_lines").Scan(&movementLinesBefore); err != nil {
        t.Fatalf("count movement_lines before: %v", err)
    }

    var warehouseID, userID int64
    if err := pool.QueryRow(ctx, "SELECT id FROM warehouses ORDER BY id LIMIT 1").Scan(&warehouseID); err != nil {
        t.Fatalf("find warehouse: %v", err)
    }
    if err := pool.QueryRow(ctx, "SELECT id FROM users ORDER BY id LIMIT 1").Scan(&userID); err != nil {
        t.Skipf("no user available in migrated test database: %v", err)
    }

    var validationID int64
    err = pool.QueryRow(ctx,
        `INSERT INTO inventory_validations (type, warehouse_id, status, responsible_user_id, started_at)
         VALUES ('TOTAL', $1, 'OPEN', $2, now())
         RETURNING id`,
        warehouseID, userID,
    ).Scan(&validationID)
    if err != nil {
        t.Fatalf("insert inventory validation: %v", err)
    }

    if _, err := pool.Exec(ctx, "DELETE FROM inventory_validations WHERE id = $1", validationID); err != nil {
        t.Fatalf("cleanup inventory validation: %v", err)
    }

    var movementsAfter, movementLinesAfter int64
    if err := pool.QueryRow(ctx, "SELECT count(*) FROM movements").Scan(&movementsAfter); err != nil {
        t.Fatalf("count movements after: %v", err)
    }
    if err := pool.QueryRow(ctx, "SELECT count(*) FROM movement_lines").Scan(&movementLinesAfter); err != nil {
        t.Fatalf("count movement_lines after: %v", err)
    }

    if movementsBefore != movementsAfter {
        t.Fatalf("inventory foundation changed movements: before=%d after=%d", movementsBefore, movementsAfter)
    }
    if movementLinesBefore != movementLinesAfter {
        t.Fatalf("inventory foundation changed movement_lines: before=%d after=%d", movementLinesBefore, movementLinesAfter)
    }
}
