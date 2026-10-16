# Portabilidade funcional Fleetbase → TotalFood (Go + PostgreSQL)

## Diagnóstico em main@53b31fb (2026-10-15)
Fonte de comparação: Fleetbase/Fleet-Ops e Navigator; implementação local auditada em `cmd/server/`, `migrations/` e testes, sem copiar código AGPL.

| Capacidade | TotalFood verificado | Diferença / contrato de integração |
| --- | --- | --- |
| Cadastros cliente/refeição/BoM/CMV | `planner.go`, `recipes.go`, `031`-`033` | Preservar: não portar Storefront nem catálogo Fleetbase |
| Estoque por SKU/lote e saída | `stock_engine.go`, `movement_lines` | Preservar ledger e regras pai/marca/SKU |
| Locais, veículos, motoristas | `034`, `logistics.go` | Implementado: não criar entidade paralela |
| Entrega de insumos ou refeições | `035`, `dispatch.go` | Implementado, mas entrega não abate estoque automaticamente |
| Rota e paradas, bloqueio de duplicidades | `routes.go` | Implementado: restrições existentes de veículo/motorista/dia |
| Ordem de paradas e ETA | `osrm.go`, `routes.go` | OSRM + heurística e fallback; capacidade de veículo/volume não é atualmente validada contra carga |
| App do motorista | `driver.go`, `driver.js`, `driver-sw.js` | Implementado como web móvel; sem telemetria contínua |
| Comprovante | `proofs.go`, `delivery_proofs` | Implementado: código, foto, assinatura e temperatura opcional |
| Portal do cliente / rastreio | Não havia rota pública; `tracking_token` também era usado como código de entrega | Nesta branch: página pública somente-leitura com token independente, sem expor prova, localização exata ou dados pessoais |
| Integração produção → expedição → estoque | `delivery_order_lines.movement_line_id` opcional, sem ligação operacional automática | Próximo incremento: vínculo de SAÍDA/retorno atômico e idempotente com lote real, nunca baixa por mera criação do pedido |
| Conferência produzido × expedido × recebido | Comparativo de cardápio/saídas em `planner.go` | Falta reconciliar entrega parcial, devolução e comprovante por cliente/lote |
| Capacidade, janelas e temperatura | Capacidade no cadastro; janela no local; temperatura no comprovante | Falta peso/volume e regras sanitárias no cálculo/validação da carga; não estimar kg de porções sem ficha física |
| Gestão de frota | Veículo, motorista, documentação básica | Falta manutenção, disponibilidade, viagens múltiplas/escala e custos operacionais |
| Tracking em tempo real | Eventos com coordenada por ação do motorista | Sem streaming GPS, geofences ou telemetria; requer consentimento, retenção e limites de acesso |
| Financeiro logístico | CMV de ingredientes no Planner | Custos de combustível, motorista, manutenção e frete por rota/refeição ainda não calculados |
| Portal de clientes / notificações | Não identificado nesta cadeia | Entrega read-only primeiro; notificações e autoatendimento depois |
| IAM | Perfis existentes, `requireAuth`, `dispatch`, `delivery` | Reutilizar permissões, sem nova identidade Fleetbase |

## Incremento de rastreio público (esta branch)
- `public_tracking_token` de 64 hexadecimais, distinto do `tracking_token` que segue sendo código de confirmação; backfill dos pedidos antigos com duas UUIDs seguras nativas do PostgreSQL.
- `GET /tracking/{token}` não exige login, mas só exibe data, refeição, horário, status e ETA estimado; não inclui nomes/endereços, histórico, coordenadas, comprovante ou código.
- `Cache-Control: no-store`, `Referrer-Policy: no-referrer`, noindex e CSP para limitar vazamento do link.
- Detalhe autenticado de entrega oferece o link, sem criar um segundo sistema de cadastro.
- Teste valida isolamento dos dois tokens, 404 com código privado, ausência de PII e atualização do status.

## Próximos incrementos sem duplicação
1. **L4.1 – Expedição e ledger**: definir quando ocorre a transferência efetiva, vincular `movement_line_id` ao pedido sob transação; estorno/devolução com movimento compensatório e rastreio de lote. Não registrar SAÍDA automática sem SKU/marca/lote/quantidade comprovados. Testar concorrência, repetição, faltas e ajuste FEFO.
2. **L4.2 – Capacidade**: modelar carga física (kg/volume/caixas) com fonte canônica; validar capacidade antes de liberar rota e sinalizar janelas inviáveis. Não deduzir peso a partir de quantidade de porções.
3. **L4.3 – Recebimento e reconciliação**: quantidades expedidas/recebidas/devolvidas, ocorrência e conferência por cliente + refeição + lote; apuração de desvio sem alterar ledger retroativamente.
4. **L4.4 – Frota e custos**: disponibilidade/ manutenção, custo por km/rota e rateio por refeições; custos do Planner permanecem canônicos.
5. **L4.5 – Evoluções opcionais**: mapa operacional, telemetria com autorização, geofences, notificações, portal de clientes. Priorizar por demanda real.

## Gates permanentes
- Go/PostgreSQL existentes, `go vet`, `go test -count=1 ./...`, `go test -race -count=1 -timeout 30m ./...`, JS e build.
- Branch isolada, review do diff, CI do HEAD exato e merge somente após autorização explícita.
- Sem copiar implementação AGPL da Fleetbase; somente comportamento e contratos implementados de modo independente.
