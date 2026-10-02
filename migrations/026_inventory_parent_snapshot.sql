ALTER TABLE inventory_validation_lines
    ADD COLUMN item_id bigint REFERENCES inventory_items(id);

UPDATE inventory_validation_lines l
SET item_id = s.item_id
FROM product_skus s
WHERE s.id = l.sku_id
  AND l.item_id IS NULL;
