-- Append-only GPS observations for assets; independent of warehouse movement history.
CREATE TABLE asset_gps_history (
 id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
 operation_id text NOT NULL UNIQUE,
 request_hash text NOT NULL CHECK (btrim(request_hash) <> ''),
 asset_id bigint NOT NULL REFERENCES assets(id),
 latitude double precision NOT NULL CHECK (latitude BETWEEN -90 AND 90),
 longitude double precision NOT NULL CHECK (longitude BETWEEN -180 AND 180),
 accuracy_m double precision NOT NULL CHECK (accuracy_m >= 0 AND accuracy_m <= 100000),
 captured_at timestamptz NOT NULL,
 recorded_at timestamptz NOT NULL DEFAULT now(),
 user_id bigint NOT NULL REFERENCES users(id),
 source text NOT NULL CHECK (source IN ('CREATE','MOVE','STATUS','MANUAL')),
 CHECK (btrim(operation_id) <> '')
);
CREATE INDEX asset_gps_history_latest ON asset_gps_history (asset_id, captured_at DESC, id DESC);

-- Application credentials may append observations but cannot rewrite audit evidence.
CREATE FUNCTION prevent_asset_gps_history_edit() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  RAISE EXCEPTION 'Histórico GPS imutável';
END $$;
CREATE TRIGGER asset_gps_history_immutable BEFORE UPDATE OR DELETE ON asset_gps_history
FOR EACH ROW EXECUTE FUNCTION prevent_asset_gps_history_edit();

-- Asset creation now shares the existing operation-id audit contract.
ALTER TABLE asset_events DROP CONSTRAINT asset_events_event_type_check;
ALTER TABLE asset_events ADD CONSTRAINT asset_events_event_type_check CHECK (event_type IN ('MOVE','STATUS','CREATE'));
