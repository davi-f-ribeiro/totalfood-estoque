CREATE UNIQUE INDEX idx_inventory_items_normalized_name_unique
    ON inventory_items ((lower(btrim(name))));
