ALTER TABLE inventory_validations
    ADD COLUMN operation_id text,
    ADD COLUMN request_hash text;

ALTER TABLE inventory_validations
    ADD CONSTRAINT inventory_validations_operation_pair
    CHECK (
        (operation_id IS NULL AND request_hash IS NULL)
        OR
        (
            operation_id IS NOT NULL
            AND btrim(operation_id) <> ''
            AND request_hash IS NOT NULL
            AND btrim(request_hash) <> ''
        )
    );

CREATE UNIQUE INDEX inventory_validations_operation_id_unique
    ON inventory_validations(operation_id)
    WHERE operation_id IS NOT NULL;
