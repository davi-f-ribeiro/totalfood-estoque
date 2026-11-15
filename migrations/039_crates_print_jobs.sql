-- Caixas de hortifruti e fila de etiquetas, complementando os lotes e movimentações existentes.
CREATE TABLE stock_crates (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    stock_lot_id bigint NOT NULL REFERENCES stock_lots(id),
    origin_lot_id bigint NOT NULL REFERENCES stock_lots(id),
    entry_movement_id bigint NOT NULL REFERENCES movements(id),
    sku_id bigint NOT NULL REFERENCES product_skus(id),
    warehouse_id bigint NOT NULL REFERENCES warehouses(id),
    box_position integer NOT NULL CHECK (box_position > 0),
    box_total integer NOT NULL CHECK (box_total >= box_position),
    initial_weight numeric(14,3) NOT NULL CHECK (initial_weight > 0),
    current_weight numeric(14,3) NOT NULL CHECK (current_weight >= 0),
    revision integer NOT NULL DEFAULT 1 CHECK (revision > 0),
    status text NOT NULL DEFAULT 'ACTIVE' CHECK (status IN ('ACTIVE','EMPTY')),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE(entry_movement_id,box_position),
    CHECK ((status='EMPTY') = (current_weight=0))
);
CREATE INDEX idx_stock_crates_lot ON stock_crates(stock_lot_id);
CREATE INDEX idx_stock_crates_warehouse_status ON stock_crates(warehouse_id,status);

CREATE TABLE stock_crate_events (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    crate_id bigint NOT NULL REFERENCES stock_crates(id),
    operation_id text NOT NULL UNIQUE,
    event_type text NOT NULL CHECK (event_type IN ('RECEIVED','REWEIGH','TRANSFERRED')),
    previous_weight numeric(14,3),
    next_weight numeric(14,3) NOT NULL CHECK (next_weight >= 0),
    movement_id bigint REFERENCES movements(id),
    previous_warehouse_id bigint REFERENCES warehouses(id),
    next_warehouse_id bigint REFERENCES warehouses(id),
    actor_id bigint REFERENCES users(id),
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX idx_stock_crate_events_crate ON stock_crate_events(crate_id,id);

CREATE TABLE print_devices (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    name text NOT NULL UNIQUE CHECK (btrim(name)<>''),
    token_hash text NOT NULL UNIQUE,
    active boolean NOT NULL DEFAULT true,
    created_at timestamptz NOT NULL DEFAULT now(),
    last_seen_at timestamptz
);

CREATE TABLE print_jobs (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    crate_id bigint NOT NULL REFERENCES stock_crates(id),
    revision integer NOT NULL CHECK (revision > 0),
    device_id bigint REFERENCES print_devices(id),
    zpl text NOT NULL CHECK (length(zpl) BETWEEN 1 AND 65536),
    status text NOT NULL DEFAULT 'PENDING'
        CHECK (status IN ('PENDING','CLAIMED','SENT','UNCERTAIN','FAILED','CANCELLED')),
    claim_token_hash text,
    claimed_at timestamptz,
    claim_expires_at timestamptz,
    sent_at timestamptz,
    last_error text,
    created_by bigint REFERENCES users(id),
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE(crate_id,revision)
);
CREATE INDEX idx_print_jobs_claim ON print_jobs(id) WHERE status='PENDING';
CREATE INDEX idx_print_jobs_claim_expiration ON print_jobs(claim_expires_at) WHERE status='CLAIMED';

-- Uma movimentação genérica não pode consumir silenciosamente um lote rastreado
-- por caixas: a baixa deve indicar qual caixa teve seu peso reduzido.
