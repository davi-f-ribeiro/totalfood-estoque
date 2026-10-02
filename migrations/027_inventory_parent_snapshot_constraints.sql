ALTER TABLE inventory_validation_lines
    ALTER COLUMN item_id SET NOT NULL;

CREATE INDEX idx_inventory_validation_lines_item_id
    ON inventory_validation_lines(item_id);
