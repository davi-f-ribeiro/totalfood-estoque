\set ON_ERROR_STOP on

-- Production preflight for migration 021_crud_optimistic_concurrency.sql.
-- Read-only: this script never changes application data or schema.

SELECT current_database() AS database_name, now() AS checked_at;

WITH targets(table_name) AS (
    VALUES ('inventory_items'),('product_skus'),('users'),('profiles'),('assets')
),
state AS (
    SELECT t.table_name,
           EXISTS (
               SELECT 1
               FROM information_schema.columns c
               WHERE c.table_schema='public'
                 AND c.table_name=t.table_name
                 AND c.column_name='version'
           ) AS has_version
    FROM targets t
)
SELECT * FROM state ORDER BY table_name;

DO $$
DECLARE
    present_count integer;
    migration_recorded boolean;
BEGIN
    SELECT count(*)
    INTO present_count
    FROM information_schema.columns
    WHERE table_schema='public'
      AND column_name='version'
      AND table_name IN ('inventory_items','product_skus','users','profiles','assets');

    SELECT EXISTS (
        SELECT 1
        FROM schema_migrations
        WHERE version='021_crud_optimistic_concurrency'
    ) INTO migration_recorded;

    IF present_count NOT IN (0,5) THEN
        RAISE EXCEPTION 'PRECHECK_FAILED: CRUD version schema is partially present (%/5)', present_count;
    END IF;

    IF present_count=5 AND NOT migration_recorded THEN
        RAISE EXCEPTION 'PRECHECK_FAILED: CRUD version schema exists without migration 021 record';
    END IF;
END;
$$;

SELECT version, applied_at
FROM schema_migrations
WHERE version='021_crud_optimistic_concurrency';

SELECT 'PRECHECK_OK' AS crud_021_preflight;
