-- Requisições de itens sem peso padrão (hortifruti, proteínas): a conta exata não fecha.
-- A cozinha pode aceitar o que foi entregue e encerrar a requisição; o que ainda
-- estava reservado volta a ficar livre. O encerramento não movimenta estoque.
ALTER TABLE material_requests
  ADD COLUMN closed_by bigint REFERENCES users(id),
  ADD COLUMN closed_at timestamptz,
  ADD COLUMN close_notes text NOT NULL DEFAULT '' CHECK(length(close_notes) <= 500),
  ADD CONSTRAINT material_requests_closed_check CHECK((closed_at IS NULL) = (closed_by IS NULL));

ALTER TABLE material_request_events DROP CONSTRAINT material_request_events_action_check;
ALTER TABLE material_request_events ADD CONSTRAINT material_request_events_action_check CHECK(action IN
  ('CRIADA','RESERVADA','LIBERADA','ENTREGUE','RECEBIDA','DIVERGENCIA','CONCILIADA','ENCERRADA','CANCELADA'));

-- Item controlado por caixa: quais caixas saíram em cada entrega, e quanto de cada uma.
CREATE TABLE material_request_delivery_crates (
  delivery_id bigint NOT NULL REFERENCES material_request_deliveries(id),
  crate_id bigint NOT NULL REFERENCES stock_crates(id),
  quantity numeric(14,3) NOT NULL CHECK(quantity > 0),
  PRIMARY KEY(delivery_id,crate_id)
);
CREATE INDEX idx_material_delivery_crates_crate ON material_request_delivery_crates(crate_id);
