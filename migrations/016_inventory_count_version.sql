ALTER TABLE inventory_validation_lines
    ADD COLUMN count_version bigint NOT NULL DEFAULT 0;
