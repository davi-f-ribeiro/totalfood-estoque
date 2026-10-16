-- Rastreio público separado do código privado de confirmação de entrega.
-- PostgreSQL 16+: gen_random_uuid() é criptograficamente seguro e nativo.
ALTER TABLE delivery_orders ADD COLUMN public_tracking_token text;

-- Pedidos criados antes desta migration também recebem link distinto do código de entrega.
UPDATE delivery_orders
SET public_tracking_token = replace(gen_random_uuid()::text, '-', '') || replace(gen_random_uuid()::text, '-', '');

ALTER TABLE delivery_orders
    ALTER COLUMN public_tracking_token SET NOT NULL,
    ADD CONSTRAINT delivery_public_tracking_token_length CHECK (length(public_tracking_token) = 64);

CREATE UNIQUE INDEX idx_delivery_orders_public_tracking_token
    ON delivery_orders (public_tracking_token);
