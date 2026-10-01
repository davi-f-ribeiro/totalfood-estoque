\set ON_ERROR_STOP on

-- Production preflight for migration 019_warehouse_administration.sql.
-- Read-only: this script never changes application data or schema.

SELECT current_database() AS database_name, now() AS checked_at;

-- 1) Names that would collide with the tracking unique index.
SELECT lower(btrim(name)) AS normalized_name,
       count(*) AS warehouse_count,
       array_agg(id ORDER BY id) AS warehouse_ids,
       array_agg(name ORDER BY id) AS warehouse_names
FROM warehouses
GROUP BY lower(btrim(name))
HAVING count(*) > 1;

DO $$
BEGIN
    IF EXISTS (
        SELECT 1
        FROM warehouses
        GROUP BY lower(btrim(name))
        HAVING count(*) > 1
    ) THEN
        RAISE EXCEPTION 'PRECHECK_FAILED: normalized duplicate warehouse names exist';
    END IF;
END;
$$;

-- 2) Inactive warehouses that still have real stock.
SELECT w.id AS warehouse_id,
       w.name AS warehouse_name,
       ml.sku_id,
       SUM(CASE ml.direction WHEN 'IN' THEN ml.quantity ELSE -ml.quantity END) AS balance
FROM warehouses w
JOIN movement_lines ml ON ml.warehouse_id = w.id
WHERE w.active = false
GROUP BY w.id, w.name, ml.sku_id
HAVING SUM(CASE ml.direction WHEN 'IN' THEN ml.quantity ELSE -ml.quantity END) <> 0
ORDER BY w.id, ml.sku_id;

DO $$
BEGIN
    IF EXISTS (
        SELECT 1
        FROM warehouses w
        JOIN movement_lines ml ON ml.warehouse_id = w.id
        WHERE w.active = false
        GROUP BY w.id, ml.sku_id
        HAVING SUM(CASE ml.direction WHEN 'IN' THEN ml.quantity ELSE -ml.quantity END) <> 0
    ) THEN
        RAISE EXCEPTION 'PRECHECK_FAILED: inactive warehouse has non-zero stock';
    END IF;
END;
$$;

-- 3) Current patrimonial location pointing to an inactive warehouse.
SELECT w.id AS warehouse_id,
       w.name AS warehouse_name,
       count(*) AS asset_count
FROM warehouses w
JOIN assets a ON a.current_warehouse_id = w.id
WHERE w.active = false
GROUP BY w.id, w.name
ORDER BY w.id;

DO $$
BEGIN
    IF EXISTS (
        SELECT 1
        FROM warehouses w
        JOIN assets a ON a.current_warehouse_id = w.id
        WHERE w.active = false
    ) THEN
        RAISE EXCEPTION 'PRECHECK_FAILED: inactive warehouse has current assets';
    END IF;
END;
$$;

-- 4) Migration must not already be partially/incorrectly recorded.
SELECT version, applied_at
FROM schema_migrations
WHERE version = '019_warehouse_administration';

SELECT 'PRECHECK_OK' AS warehouse_019_preflight;
