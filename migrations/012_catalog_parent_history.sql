ALTER TABLE movement_lines
    ADD COLUMN item_id bigint REFERENCES inventory_items(id);

UPDATE movement_lines ml
SET item_id = s.item_id
FROM product_skus s
WHERE s.id = ml.sku_id AND ml.item_id IS NULL;
