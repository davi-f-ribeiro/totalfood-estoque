# Requisições de materiais — Totalfood Estoque

## Objetivo
Uma unidade (ex.: Cozinha Central) solicita materiais por **item pai**, para uma **data de produção** e um **serviço** (café da manhã, almoço ou jantar). A operação é independente da solicitação de compra do Planner.

## Fluxo
1. Cozinha abre `/material-requests`, informa unidade, entrega desejada, data de produção e serviço; adiciona itens/quantidades nas unidades-base (KG, L, UN).
2. Estoquista acessa a mesma tela, escolhe apresentação (SKU/marca) e depósito para **separar** quantidade parcial ou total.
3. A separação cria uma **reserva**, abatida do saldo **livre**, mas não do estoque físico. Saídas e transferências normais respeitam essas reservas, sem criar movimentações fictícias.
4. Ao **confirmar entrega**, o backend consome a reserva e gera, na mesma transação, uma saída real com a data de produção original, custo médio e alocação FEFO existente. A entrega referencia o `movement_id`.
5. Cozinha confirma o **recebimento** de cada entrega. Pode haver várias entregas por requisição; somente após atendimento integral e todos os recebimentos confirmados o pedido é marcado `RECEBIDA`.
6. Se ainda não houve entrega, o solicitante pode cancelar, liberando todas as reservas. O estoquista também pode liberar reservas não entregues.

## Perfis
- Cozinha (criado pela migration 038): ler/criar suas requisições, confirmar recebimentos.
- Estoquista: ler requisições, separar, liberar e entregar.
- Gerente e Diretor: todas as ações.

`GET /api/material-requests` só mostra os pedidos próprios para quem não tem a permissão de atendimento. Cada operação exige sessão/CSRF e é auditável.

## Segurança, disponibilidade e reenvio
- `operation_id` idempotente com hash para criação, separação e entrega.
- Recebimento, cancelamento e liberação são naturalmente repetíveis pelo estado persistido.
- A interface conserva operações incertas no IndexedDB e em `sessionStorage` para reenvio explícito, sem envios automáticos em loop.
- O estoque livre considera reservas ativas do mesmo **item+marca+depósito**, mesmo quando há SKUs de apresentações diferentes.
- A quantidade é em unidade-base (até três casas decimais), independentemente do tamanho da embalagem.
- Eventos de requisição são inseridos em `material_request_events`, sem alteração da movimentação original ou do horário real.

## Endpoints
- `GET/POST /api/material-requests` — listar/criar.
- `GET /api/material-requests/{id}` — detalhes, reservas, entregas e eventos.
- `GET /api/material-requests/items?q=` — busca por item pai.
- `GET /api/material-requests/options/{item_id}` — SKU, marca, depósito, saldo físico/livre.
- `POST /api/material-requests/{id}/reserve` — separar.
- `POST /api/material-requests/{id}/deliver` — saída física transacional.
- `POST /api/material-requests/{id}/reservations/{reservation_id}/release` — liberar saldo reservado.
- `POST /api/material-requests/{id}/deliveries/{delivery_id}/receive` — conferir entrega.
- `POST /api/material-requests/{id}/cancel` — cancelar antes da primeira entrega.

## Próximas integrações
- Gerar rascunhos de requisição a partir das fichas/cardápios aprovados do Planner.
- Confrontar consumo previsto × realizado por **serviço**. A data de produção já acompanha cada saída; o serviço permanece vinculado ao registro da requisição.
- Indicadores de prazo de atendimento e divergências por unidade.
