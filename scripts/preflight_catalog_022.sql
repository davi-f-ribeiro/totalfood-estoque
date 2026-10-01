\set ON_ERROR_STOP on

-- Production preflight for migration 022_catalog_normalized_name_unique.sql.
-- Read-only: this script never changes application data or schema.

SELECT current_database() AS database_name, now() AS checked_at;

-- Any row returned here blocks migration 022. Data must be reviewed explicitly;
-- this preflight never chooses which duplicate to keep.
SELECT
    lower(btrim(name)) AS normalized_name,
    count(*) AS duplicate_count,
    array_agg(id ORDER BY id) AS item_ids,
    array_agg(name ORDER BY id) AS item_names
FROM inventory_items
GROUP BY lower(btrim(name))
HAVING count(*) > 1
ORDER BY normalized_name;

DO $$
DECLARE
    duplicate_groups integer;
    index_exists boolean;
    migration_recorded boolean;
BEGIN
    SELECT count(*)
    INTO duplicate_groups
    FROM (
        SELECT lower(btrim(name))
        FROM inventory_items
        GROUP BY lower(btrim(name))
        HAVING count(*) > 1
    ) d;

    IF duplicate_groups > 0 THEN
        RAISE EXCEPTION 'PRECHECK_FAILED: % normalized inventory item name duplicate group(s) exist', duplicate_groups;
    END IF;

    SELECT EXISTS (
        SELECT 1
        FROM pg_indexes
        WHERE schemaname='public'
          AND tablename='inventory_items'
          AND indexname='idx_inventory_items_normalized_name_unique'
    ) INTO index_exists;

    SELECT EXISTS (
        SELECT 1
        FROM schema_migrations
        WHERE version='022_catalog_normalized_name_unique'
    ) INTO migration_recorded;

    IF index_exists AND NOT migration_recorded THEN
        RAISE EXCEPTION 'PRECHECK_FAILED: normalized item name index exists without migration 022 record';
    END IF;

    IF migration_recorded AND NOT index_exists THEN
        RAISE EXCEPTION 'PRECHECK_FAILED: migration 022 recorded but normalized item name index is missing';
    END IF;
END;
$$;

SELECT version, applied_at
FROM schema_migrations
WHERE version='022_catalog_normalized_name_unique';

SELECT 'PRECHECK_OK' AS catalog_022_preflight;
