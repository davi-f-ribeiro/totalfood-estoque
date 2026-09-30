ALTER TABLE inventory_validations
    ADD COLUMN completed_by_user_id bigint REFERENCES users(id),
    ADD COLUMN completion_note text,
    ADD COLUMN cancelled_by_user_id bigint REFERENCES users(id),
    ADD COLUMN cancellation_reason text;

ALTER TABLE inventory_validations
    ADD CONSTRAINT inventory_validations_completion_note_length
        CHECK (completion_note IS NULL OR char_length(completion_note) <= 1000),
    ADD CONSTRAINT inventory_validations_cancellation_reason_length
        CHECK (cancellation_reason IS NULL OR char_length(cancellation_reason) BETWEEN 5 AND 1000);

CREATE INDEX idx_inventory_validations_completed_by_user_id
    ON inventory_validations(completed_by_user_id);

CREATE INDEX idx_inventory_validations_cancelled_by_user_id
    ON inventory_validations(cancelled_by_user_id);
