package main

import (
    "context"
    "os"
    "path/filepath"
    "testing"
)

func TestInventoryFoundationDoesNotCreateMovements(t *testing.T) {
    if os.Getenv("TEST_DATABASE_URL") == "" {
        t.Skip("TEST_DATABASE_URL is required for PostgreSQL integration check")
    }

    db := openTestDatabase(t)
    ctx := context.Background()
    if err := runMigrations(ctx, db, filepath.Join("..", "..", "migrations")); err != nil {
        t.Fatalf("migrate: %v", err)
    }

    var movementsBefore, movementLinesBefore int64
    if err := db.QueryRowContext(ctx, "SELECT count(*) FROM movements").Scan(&movementsBefore); err != nil {
        t.Fatalf("count movements before: %v", err)
    }
    if err := db.QueryRowContext(ctx, "SELECT count(*) FROM movement_lines").Scan(&movementLinesBefore); err != nil {
        t.Fatalf("count movement_lines before: %v", err)
    }

    var warehouseID, profileID, userID int64
    if err := db.QueryRowContext(ctx, "SELECT id FROM warehouses ORDER BY id LIMIT 1").Scan(&warehouseID); err != nil {
        t.Fatalf("find warehouse: %v", err)
    }
    if err := db.QueryRowContext(ctx, "SELECT id FROM profiles WHERE name = 'Diretor'").Scan(&profileID); err != nil {
        t.Fatalf("find profile: %v", err)
    }
    if err := db.QueryRowContext(ctx,
        `INSERT INTO users (login, password_hash, profile_id)
         VALUES ($1, $2, $3)
         RETURNING id`,
        "inventory-foundation-"+t.Name(), "test-hash", profileID,
    ).Scan(&userID); err != nil {
        t.Fatalf("create responsible user: %v", err)
    }

    var validationID int64
    if err := db.QueryRowContext(ctx,
        `INSERT INTO inventory_validations (type, warehouse_id, status, responsible_user_id, started_at)
         VALUES ('TOTAL', $1, 'OPEN', $2, now())
         RETURNING id`,
        warehouseID, userID,
    ).Scan(&validationID); err != nil {
        t.Fatalf("insert inventory validation: %v", err)
    }

    if _, err := db.ExecContext(ctx, "DELETE FROM inventory_validations WHERE id = $1", validationID); err != nil {
        t.Fatalf("cleanup inventory validation: %v", err)
    }

    var movementsAfter, movementLinesAfter int64
    if err := db.QueryRowContext(ctx, "SELECT count(*) FROM movements").Scan(&movementsAfter); err != nil {
        t.Fatalf("count movements after: %v", err)
    }
    if err := db.QueryRowContext(ctx, "SELECT count(*) FROM movement_lines").Scan(&movementLinesAfter); err != nil {
        t.Fatalf("count movement_lines after: %v", err)
    }

    if movementsBefore != movementsAfter {
        t.Fatalf("inventory foundation changed movements: before=%d after=%d", movementsBefore, movementsAfter)
    }
    if movementLinesBefore != movementLinesAfter {
        t.Fatalf("inventory foundation changed movement_lines: before=%d after=%d", movementLinesBefore, movementLinesAfter)
    }
}
