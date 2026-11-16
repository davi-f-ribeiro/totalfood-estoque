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
