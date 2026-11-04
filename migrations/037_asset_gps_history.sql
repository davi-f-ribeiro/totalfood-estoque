-- Append-only GPS observations for assets; independent of warehouse movement history.
CREATE TABLE asset_gps_history (
 id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
 operation_id text NOT NULL UNIQUE,
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
CREATE INDEX asset_gps_history_latest ON asset_gps_history (asset_id, recorded_at DESC, id DESC);
