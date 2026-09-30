CREATE TABLE inventory_validation_recounts (
    validation_id bigint NOT NULL,
    sku_id bigint NOT NULL,
    quantity numeric(14,3) NOT NULL,
    recounted_by_user_id bigint NOT NULL REFERENCES users(id),
    recounted_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (validation_id, sku_id),
    FOREIGN KEY (validation_id, sku_id)
        REFERENCES inventory_validation_lines(validation_id, sku_id),
    CHECK (quantity >= 0)
);

CREATE INDEX idx_inventory_validation_recounts_user_id
    ON inventory_validation_recounts(recounted_by_user_id);
