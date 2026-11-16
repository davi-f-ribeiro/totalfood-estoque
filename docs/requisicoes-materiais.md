# Requisições de materiais — Totalfood Estoque

## Objetivo
Uma unidade (ex.: Cozinha Central) solicita materiais por **item pai**, para uma **data de produção** e um **serviço** (café da manhã, almoço ou jantar). A operação é independente da solicitação de compra do Planner.

## Fluxo
1. Cozinha abre `/material-requests`, informa unidade, entrega desejada, data de produção e serviço; adiciona itens/quantidades nas unidades-base (KG, L, UN).
2. Estoquista acessa a mesma tela, escolhe apresentação (SKU/marca) e depósito para **separar** quantidade parcial ou total.
3. A separação cria uma **reserva**, abatida do saldo **livre**, mas não do estoque físico. Saídas e transferências normais respeitam essas reservas, sem criar movimentações fictícias.
4. Ao **confirmar entrega**, o backend consome a reserva e gera, na mesma transação, uma saída real com a data de produção original, custo médio e alocação FEFO existente. A entrega referencia o `movement_id`.
5. Cozinha confirma o **recebimento** de cada entrega, informando a quantidade que chegou. Pode haver várias entregas por requisição; somente após atendimento integral e todos os recebimentos confirmados o pedido é marcado `RECEBIDA`.
   - **Recebimento parcial (migration 041):** se a cozinha recebeu menos que o entregue (por exemplo 180 de 200 kg), informa a quantidade e o motivo. A diferença fica como **divergência** na entrega e o pedido aparece como `RECEBIDA_COM_DIVERGENCIA`.
   - **Recebimento do restante:** quando o estoque completa o que faltou, a cozinha solicitante (ou Gerente/Diretor) confirma o recebimento do restante na mesma entrega, de uma vez ou em partes. Com a divergência zerada, o pedido vira `RECEBIDA`. O Estoquista, que entrega, não confirma.
   - Conferir e conciliar **não movimentam estoque**: a saída é a da entrega. Devolução ao estoque e baixa como perda não fazem parte deste fluxo.
   - **Itens sem peso padrão (migration 042):** em hortifruti e proteína a conta exata não fecha.
     - O estoque entrega o **peso real**. Item em KG pode ser separado e entregue até **20% acima do pedido** (`matWeightTolerance`); item em outra unidade continua limitado ao pedido. A situação "Entregue" é decidida por linha: entregue maior ou igual ao pedido.
     - Depois de conferir as entregas, a cozinha solicitante (ou Gerente/Diretor) pode **aceitar e encerrar** a requisição mesmo com diferença, para o pedido ou para o entregue. O pedido vira `RECEBIDA`, o que ainda estava reservado volta a ficar livre e o histórico registra `ENCERRADA` com pedido, entregue e recebido. Não movimenta estoque.
     - Quando a requisição vira `RECEBIDA`, qualquer sobra de reserva é liberada.
   - **Item controlado por caixa:** a saída comum continua recusando estoque de caixa (a proteção de rastreabilidade não mudou). Na entrega o estoquista marca as **caixas que vão sair** (`crate_ids`):
     - várias caixas saem inteiras, e a quantidade tem de ser a soma dos **pesos atuais** (se uma caixa foi repesada, vale o peso novo; soma diferente é recusada);
     - uma caixa só pode sair em parte: o restante fica nela, com etiqueta nova;
     - tudo vira **um movimento de saída**, com uma linha por caixa alocada no lote da própria caixa, na mesma transação que consome a reserva e registra a entrega;
     - as caixas de cada entrega ficam em `material_request_delivery_crates` e aparecem na entrega e no histórico;
     - sem caixa marcada, a resposta é 409 com orientação, não erro genérico. A leitura de QR Code na tela de Requisições ainda não existe: a escolha é pela lista.
   - Com peso real, o pedido pode ficar coberto ("Entregue") e ainda haver saldo separado: a entrega continua aceita enquanto a reserva tiver saldo.
6. Se ainda não houve entrega, o solicitante pode cancelar, liberando todas as reservas. O estoquista também pode liberar reservas não entregues.

## Perfis
- Cozinha (criado pela migration 038): ler/criar suas requisições, confirmar recebimentos.
- Estoquista: ler requisições, separar, liberar e entregar.
- Gerente e Diretor: todas as ações.

`GET /api/material-requests` só mostra os pedidos próprios para quem não tem a permissão de atendimento. Cada operação exige sessão/CSRF e é auditável.

## Segurança, disponibilidade e reenvio
- `operation_id` idempotente com hash para criação, separação e entrega.
- Recebimento, cancelamento e liberação são naturalmente repetíveis pelo estado persistido. Repetir a conferência com a mesma quantidade não muda nada; com outra quantidade é recusada.
- A conciliação usa `operation_id` com hash, e a entrega é travada na transação: dois envios ao mesmo tempo não confirmam a mesma quantidade duas vezes, e a soma recebida nunca passa da entregue.
- Cada conferência e cada conciliação vira uma linha em `material_request_receipts` (quem, quando, quanto, observação) e um evento `DIVERGENCIA` ou `CONCILIADA` no histórico.
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
- `POST /api/material-requests/{id}/deliver` — saída física transacional. `crate_ids` (lista) para item controlado por caixa.
- `POST /api/material-requests/{id}/reservations/{reservation_id}/release` — liberar saldo reservado.
- `POST /api/material-requests/{id}/deliveries/{delivery_id}/receive` — conferir entrega. Corpo opcional: `received_quantity` (sem ela, recebimento integral) e `reason` (obrigatório quando a quantidade é menor que a entregue).
- `POST /api/material-requests/{id}/deliveries/{delivery_id}/reconcile` — confirmar o recebimento do que faltava (`operation_id`, `quantity`, `notes`). Mesma regra da conferência: `material_receipts`, solicitante ou gerência.
- `POST /api/material-requests/{id}/close` — aceitar o que foi recebido e encerrar (`notes` opcional). `material_receipts`, solicitante ou gerência; exige todas as entregas conferidas.
- `POST /api/material-requests/{id}/cancel` — cancelar antes da primeira entrega.

## Próximas integrações
- Gerar rascunhos de requisição a partir das fichas/cardápios aprovados do Planner.
- Confrontar consumo previsto × realizado por **serviço**. A data de produção já acompanha cada saída; o serviço permanece vinculado ao registro da requisição.
- Indicadores de prazo de atendimento e divergências por unidade.
