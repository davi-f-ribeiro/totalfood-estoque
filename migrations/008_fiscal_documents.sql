CREATE TABLE fiscal_documents (
  id bigserial PRIMARY KEY,
  access_key text NOT NULL UNIQUE,
  operation_id text NOT NULL UNIQUE REFERENCES movements(operation_id),
  movement_id bigint NOT NULL REFERENCES movements(id),
  created_at timestamptz NOT NULL DEFAULT now()
);
