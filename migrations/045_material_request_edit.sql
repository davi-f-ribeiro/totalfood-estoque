-- Edição de requisição: o solicitante (ou a gerência) corrige itens, quantidades e
-- dados do pedido sem cancelar nem mexer no banco. A versão protege contra duas
-- edições feitas sobre a mesma tela desatualizada; cada edição vira um evento EDITADA.
ALTER TABLE material_requests
  ADD COLUMN version bigint NOT NULL DEFAULT 1 CHECK(version > 0);

ALTER TABLE material_request_events DROP CONSTRAINT material_request_events_action_check;
ALTER TABLE material_request_events ADD CONSTRAINT material_request_events_action_check CHECK(action IN
  ('CRIADA','EDITADA','RESERVADA','LIBERADA','ENTREGUE','RECEBIDA','DIVERGENCIA','CONCILIADA','ENCERRADA','CANCELADA'));
