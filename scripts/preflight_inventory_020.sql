\set ON_ERROR_STOP on

-- Production preflight for migration 020_inventory_creation_idempotency.sql.
-- Read-only: this script never changes application data or schema.

SELECT current_database() AS database_name, now() AS checked_at;

SELECT
    EXISTS (
        SELECT 1 FROM information_schema.columns
        WHERE table_schema='public'
          AND table_name='inventory_validations'
          AND column_name='operation_id'
    ) AS operation_id_column_exists,
    EXISTS (
        SELECT 1 FROM information_schema.columns
        WHERE table_schema='public'
          AND table_name='inventory_validations'
          AND column_name='request_hash'
    ) AS request_hash_column_exists;

DO $$
DECLARE
    has_operation boolean;
    has_hash boolean;
BEGIN
    SELECT EXISTS (
        SELECT 1 FROM information_schema.columns
        WHERE table_schema='public'
          AND table_name='inventory_validations'
          AND column_name='operation_id'
    ) INTO has_operation;

    SELECT EXISTS (
        SELECT 1 FROM information_schema.columns
        WHERE table_schema='public'
          AND table_name='inventory_validations'
          AND column_name='request_hash'
    ) INTO has_hash;

    IF has_operation <> has_hash THEN
        RAISE EXCEPTION 'PRECHECK_FAILED: inventory idempotency schema is partially present';
    END IF;
END;
$$;

SELECT version, applied_at
FROM schema_migrations
WHERE version = '020_inventory_creation_idempotency';

SELECT 'PRECHECK_OK' AS inventory_020_preflight;
