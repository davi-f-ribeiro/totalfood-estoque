-- Rastro Connect: credenciais protegidas e vínculo com frota existente.
-- Importações sem capacidade verificada permanecem INATIVAS.
CREATE TABLE rastroconnect_settings (
    id integer PRIMARY KEY DEFAULT 1 CHECK (id = 1),
    company text NOT NULL CHECK (btrim(company) <> ''),
    login text NOT NULL CHECK (btrim(login) <> ''),
    password_cipher bytea NOT NULL,
    last_sync_at timestamptz
);

-- A capacidade é obrigatória para veículos ativos; nunca inventamos um valor
-- ao importar um veículo com dados incompletos do rastreador.
ALTER TABLE vehicles ALTER COLUMN capacity_kg DROP NOT NULL;
ALTER TABLE vehicles ADD CONSTRAINT vehicles_active_capacity_required
    CHECK (NOT active OR capacity_kg IS NOT NULL);

CREATE TABLE rastroconnect_vehicle_telemetry (
    vehicle_id bigint PRIMARY KEY REFERENCES vehicles(id) ON DELETE CASCADE,
    remote_id text NOT NULL UNIQUE CHECK (btrim(remote_id) <> ''),
    equipment_id text,
    latitude numeric(9,6) CHECK (latitude BETWEEN -90 AND 90),
    longitude numeric(9,6) CHECK (longitude BETWEEN -180 AND 180),
    speed_kmh numeric(9,2) CHECK (speed_kmh IS NULL OR speed_kmh >= 0),
    ignition boolean,
    source_time text,
    synced_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT rastro_coords_pair CHECK ((latitude IS NULL) = (longitude IS NULL))
);
