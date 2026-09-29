-- DDL that depends on the movement_lines backfill must run in a new
-- transaction. PostgreSQL can reject ALTER TABLE after the UPDATE above
-- when constraint-trigger events are still pending in the same transaction.
ALTER TABLE movement_lines
    ALTER COLUMN item_id SET NOT NULL;

CREATE INDEX IF NOT EXISTS idx_movement_lines_item_id ON movement_lines(item_id);

CREATE OR REPLACE FUNCTION snapshot_movement_line_item()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    IF NEW.item_id IS NULL THEN
        SELECT item_id INTO NEW.item_id FROM product_skus WHERE id=NEW.sku_id;
    END IF;
    IF NEW.item_id IS NULL THEN
        RAISE EXCEPTION 'movement line item snapshot missing for sku %', NEW.sku_id;
    END IF;
    RETURN NEW;
END;
$$;

DROP TRIGGER IF EXISTS movement_lines_item_snapshot ON movement_lines;
CREATE TRIGGER movement_lines_item_snapshot
BEFORE INSERT ON movement_lines
FOR EACH ROW EXECUTE FUNCTION snapshot_movement_line_item();
