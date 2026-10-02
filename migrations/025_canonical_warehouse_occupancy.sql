CREATE OR REPLACE FUNCTION enforce_warehouse_deactivation_empty()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    IF OLD.active AND NOT NEW.active THEN
        IF EXISTS (
            SELECT 1
            FROM movement_lines
            WHERE warehouse_id = OLD.id
            GROUP BY item_id
            HAVING SUM(CASE direction WHEN 'IN' THEN quantity ELSE -quantity END) <> 0
        ) OR EXISTS (
            SELECT 1
            FROM assets
            WHERE current_warehouse_id = OLD.id
        ) THEN
            RAISE EXCEPTION 'warehouse not empty';
        END IF;
    END IF;

    RETURN NEW;
END;
$$;
