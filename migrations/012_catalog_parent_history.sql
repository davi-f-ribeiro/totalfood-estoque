ALTER TABLE movement_lines
    ADD COLUMN item_id bigint REFERENCES inventory_items(id);

UPDATE movement_lines ml
SET item_id = s.item_id
FROM product_skus s
WHERE s.id = ml.sku_id AND ml.item_id IS NULL;

ALTER TABLE movement_lines
    ALTER COLUMN item_id SET NOT NULL;

CREATE INDEX idx_movement_lines_item_id ON movement_lines(item_id);

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

CREATE TRIGGER movement_lines_item_snapshot
BEFORE INSERT ON movement_lines
FOR EACH ROW EXECUTE FUNCTION snapshot_movement_line_item();
