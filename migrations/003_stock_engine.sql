ALTER TABLE movements
    ADD COLUMN operation_id text,
    ADD COLUMN request_hash text,
    ADD COLUMN responsible text,
    ADD CONSTRAINT movements_operation_id_unique UNIQUE (operation_id),
    ADD CONSTRAINT movements_operation_id_not_blank CHECK (operation_id IS NULL OR btrim(operation_id) <> ''),
    ADD CONSTRAINT movements_request_hash_not_blank CHECK (request_hash IS NULL OR btrim(request_hash) <> ''),
    ADD CONSTRAINT movements_responsible_not_blank CHECK (responsible IS NULL OR btrim(responsible) <> '');

CREATE TABLE stock_locks (
    sku_id bigint NOT NULL REFERENCES product_skus(id),
    warehouse_id bigint NOT NULL REFERENCES warehouses(id),
    PRIMARY KEY (sku_id, warehouse_id)
);

