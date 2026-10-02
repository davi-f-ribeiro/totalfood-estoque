ALTER TABLE product_skus
    ADD COLUMN content_quantity numeric(14,4) NOT NULL DEFAULT 1
    CHECK (content_quantity > 0);

DROP TABLE stock_locks;

CREATE TABLE stock_locks (
    item_id bigint NOT NULL REFERENCES inventory_items(id),
    warehouse_id bigint NOT NULL REFERENCES warehouses(id),
    PRIMARY KEY (item_id, warehouse_id)
);
