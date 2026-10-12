# Planner de cardápio — contrato Gate 01

Este documento fixa o contrato antes da implementação e complementa o `global-engineering-contract.md` e o `ficha-tecnica-cmv-contract.md`. O planner é a base da futura **ordem de produção**; a ordem de produção não faz parte desta fase.

## Decisões do produto (já tomadas)

1. **Custo nunca vazio.** O planner usa o preço médio do estoque. Sem preço médio, usa o **último preço de compra** (última entrada do item). Sem nenhuma entrada, o plano **não é calculado**: o sistema lista os itens a cadastrar. Não existe resultado parcial.
2. **Período livre.** O plano tem data inicial e final escolhidas por quem planeja.
3. **Quatro refeições por dia:** café, almoço, lanche e jantar.
4. **Solver de mix inteligente.** Busca o CMV alvo trocando pratos e ingredientes. Itens **travados** não mudam: ao recalcular com uma proteína travada, o solver ajusta o resto (outra proteína no outro dia, feijão mais barato etc.).
5. **Compensação no período.** Um dia ou refeição pode passar do CMV alvo se o período compensar. O alvo vale para o período; cada dia tem um limite de desvio configurável.
6. **Solicitação de compra** = necessidade do plano por item menos o saldo atual (todos os depósitos). Não considera estoque mínimo nesta fase.
7. Permissão nova `planning`: Diretor lê e escreve, Gerente só lê. Estoquista e Vendas não veem. Custos continuam exigindo `costs`: quem não tem `costs` não vê valores no plano.

## Fonte de verdade

- `movement_lines` continua sendo a única fonte de custo e de saldo. O plano **não grava custo**; recalcula a cada leitura.
- Exceção deliberada: ao **aprovar** um plano, grava-se um *retrato* (quantidades por item e custos usados, com a origem de cada custo). É um documento de decisão, como um documento fiscal, e é a base da ordem de produção. Plano aprovado é imutável; mudar exige novo plano.
- Origem do custo de cada item é sempre devolvida: `estoque` (preço médio) ou `ultima_compra`.
- O backend calcula custo, CMV, solver e necessidade de compra. O frontend só exibe.

## Modelo (a detalhar no Gate 02)

- **Grupo de equivalência** de itens (ex.: feijão carioca, feijão preto). O solver só troca ingredientes dentro do grupo.
- **Função do prato** na refeição (proteína, guarnição, base, sobremesa etc.), atributo da ficha técnica. A ficha técnica atual não muda de comportamento.
- **Plano**: nome, cliente, período, CMV alvo do período, desvio diário máximo, status (rascunho, aprovado), `version`.
- **Posição do plano**: dia, refeição, função, porções, ficha escolhida, travada ou não.
- **Candidatas por função**: fichas que o solver pode usar em cada posição.
- **Solicitação de compra** e linhas: plano de origem, item, necessidade, saldo, quantidade a comprar, data em que o item acaba. Emissão idempotente por `operation_id`.

## Cálculo

- Consumo por item = soma das quantidades brutas (quantidade × fator de correção) das fichas, proporcional às porções da posição.
- CMV da posição, do dia, da refeição e do período = custo ÷ receita (porções × preço de venda do cliente).
- Compra = máx(0, necessidade do período − saldo atual). A data em que o item acaba é o primeiro dia em que o consumo acumulado passa do saldo.
- Quantidades na unidade do item, sem conversão (igual à ficha técnica).

## Solver

- Roda no backend, sem dependência externa, **determinístico** (mesma entrada, mesma saída) e com limite de tempo.
- Objetivo: aproximar o CMV do período do alvo respeitando posições travadas, candidatas, grupos de equivalência e o desvio diário máximo.
- Explica o resultado: lista cada troca (de → para, economia), o CMV antes e depois e, quando não alcança o alvo, o motivo e o CMV mais próximo possível. Nunca devolve um plano que quebre uma trava.
- Só sugere: o plano muda quando a pessoa aceita a sugestão.

## Pontos para confirmar antes do Gate 02

- Variedade: impedir a mesma proteína em dias seguidos? Sugestão: regra opcional por plano.
- A função do prato fica na ficha técnica (um valor por ficha). Se o mesmo prato servir em mais de uma função, será necessário outro modelo.
- Retrato na aprovação (custos gravados) como descrito acima.
- Preço de venda: o plano usa o preço do cliente por ficha; ficha sem preço para o cliente bloqueia o cálculo de CMV.

## Gates

1. Este contrato.
2. Migração (grupos, função, plano, posições, compra, permissão `planning`) testada como no CI.
3. Custo com fallback de última compra, com prova em teste.
4. API do plano e avaliação de CMV (posição, dia, refeição, período).
5. Solver com testes: determinismo, travas, compensação, alvo inalcançável.
6. Solicitação de compra com data de ruptura e idempotência.
7. Telas conforme `guia-de-estilo-frontend.md`.
8. Auditoria, CI verde no SHA exato e promoção só com aprovação explícita.
