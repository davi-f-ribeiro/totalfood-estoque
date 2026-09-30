package main

import (
    "context"
    "fmt"
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


func TestInventoryFoundationMigrationAppliesIncrementally(t *testing.T) {
    if os.Getenv("TEST_DATABASE_URL") == "" {
        t.Skip("TEST_DATABASE_URL is required for PostgreSQL integration check")
    }

    db := openTestDatabase(t)
    ctx := context.Background()
    migrationsDir := filepath.Join("..", "..", "migrations")

    baseDir := t.TempDir()
    for i := 1; i <= 13; i++ {
        pattern := filepath.Join(migrationsDir, fmt.Sprintf("%03d_*.sql", i))
        matches, err := filepath.Glob(pattern)
        if err != nil || len(matches) != 1 {
            t.Fatalf("resolve migration %03d: matches=%v err=%v", i, matches, err)
        }
        body, err := os.ReadFile(matches[0])
        if err != nil {
            t.Fatalf("read %s: %v", matches[0], err)
        }
        if err := os.WriteFile(filepath.Join(baseDir, filepath.Base(matches[0])), body, 0o600); err != nil {
            t.Fatalf("copy base migration: %v", err)
        }
    }
    if err := runMigrations(ctx, db, baseDir); err != nil {
        t.Fatalf("apply migrations 001-013: %v", err)
    }

    incrementalDir := t.TempDir()
    migration014 := filepath.Join(migrationsDir, "014_inventory_validation_foundation.sql")
    body, err := os.ReadFile(migration014)
    if err != nil {
        t.Fatalf("read migration 014: %v", err)
    }
    if err := os.WriteFile(filepath.Join(incrementalDir, filepath.Base(migration014)), body, 0o600); err != nil {
        t.Fatalf("copy migration 014: %v", err)
    }
    if err := runMigrations(ctx, db, incrementalDir); err != nil {
        t.Fatalf("apply migration 014 incrementally: %v", err)
    }

    var validationsTable, linesTable bool
    if err := db.QueryRowContext(ctx, `SELECT to_regclass('inventory_validations') IS NOT NULL`).Scan(&validationsTable); err != nil {
        t.Fatalf("check inventory_validations: %v", err)
    }
    if err := db.QueryRowContext(ctx, `SELECT to_regclass('inventory_validation_lines') IS NOT NULL`).Scan(&linesTable); err != nil {
        t.Fatalf("check inventory_validation_lines: %v", err)
    }
    if !validationsTable || !linesTable {
        t.Fatalf("incremental migration did not create inventory tables")
    }
}
