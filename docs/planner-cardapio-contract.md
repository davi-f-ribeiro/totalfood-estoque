# Planner de cardápio — contrato Gate 01

Este documento fixa o contrato antes da implementação e complementa o `global-engineering-contract.md` e o `ficha-tecnica-cmv-contract.md`. O planner é a base da futura **ordem de produção**; a ordem de produção não faz parte desta fase.

Referência: cardápios reais da semana 12/10 a 17/10 (Data Center Pecém, Quentinhas, Self Service Obras e Self Service Obras BS Ville) e a folha de produção "Almoço Data Center — 12/10 — estimado para 900 pessoas".

## Decisões do produto

1. **Custo nunca vazio.** O planner usa o preço médio do estoque. Sem preço médio, usa o **último preço de compra** (última entrada do item). Sem nenhuma entrada, o plano **não é calculado**: o sistema lista os itens a cadastrar. Não existe resultado parcial.
2. **Período livre.** O plano tem data inicial e final escolhidas por quem planeja.
3. **Refeições:** café, almoço, lanche e jantar. Cada grupo usa só as refeições que contratou (Quentinhas e Self Service: só almoço; Data Center: café, almoço e jantar).
4. **Grupos de clientes.** O cardápio é feito por **grupo** (ex.: Self Service Obras reúne várias obras). Cada grupo tem o **seu preço** e as **suas regras**. Clientes pertencem a um grupo.
5. **Solver de mix inteligente.** Busca o CMV alvo trocando pratos e ingredientes equivalentes. O que o operador **travar** não muda: com uma proteína travada, o solver ajusta o resto (outra proteína em outro dia, feijão mais barato etc.).
6. **Compensação no período.** Um dia pode passar do CMV alvo se o período compensar. O alvo vale para o período; cada dia tem um limite de desvio configurável.
7. **Ganho de escala.** O solver pode preferir a **mesma preparação para vários grupos no mesmo dia** (ex.: frango assado na segunda para Data Center, Quentinhas e Self Service), para produzir num lote só. A ordem de produção soma automaticamente as preparações iguais.
8. **Regras por grupo** (restrições), além das travas manuais. Exemplo real: no Data Center, no dia de feijoada, a refeição precisa ter um feijão que não seja o preto.
9. **Solicitação de compra** = necessidade do plano por item menos o saldo atual (todos os depósitos). Sem estoque mínimo nesta fase.
10. Permissão nova `planning`: Diretor lê e escreve, Gerente só lê. Estoquista e Vendas não veem. Quem não tem `costs` não vê valores no plano.

## O que os cardápios reais mostram

- **A refeição tem uma estrutura fixa por grupo.** Quentinhas: 2 proteínas, farofa ou pirão, arroz, feijão e macarrão. Self Service: o mesmo, mais salada. Data Center almoço: 2 proteínas, ovo frito, arroz, feijão, macarrão, farofa ou pirão, guarnição, salada, suco e fruta ou doce. Data Center café: café, leite, fruta, pão, cuscuz com proteína.
- Essa estrutura vira o **modelo de refeição** do grupo: uma lista de **funções** (proteína, base, feijão, massa, farofa, guarnição, salada, bebida, sobremesa etc.) com a quantidade de cada uma. O solver preenche as funções; o operador pode travar qualquer posição.
- **A produção é planejada por pessoas, não por pratos.** A folha do Data Center diz "900 pessoas" e converte em quilos (198 kg de arroz, 193,5 kg de feijão) e em porções. Opções alternativas têm adesão menor (ovo frito: 200 unidades para 900 pessoas).
- Por isso cada posição do plano tem: **pessoas previstas**, **adesão** (percentual que consome aquela opção; 100% por padrão) e a ficha escolhida. Porções = pessoas × adesão.

## Fonte de verdade

- `movement_lines` continua sendo a única fonte de custo e de saldo. O plano **não grava custo**; recalcula a cada leitura.
- Exceção deliberada: ao **aprovar** um plano, grava-se um *retrato* (quantidades por item e custos usados, com a origem de cada custo). É um documento de decisão, base da ordem de produção. Plano aprovado é imutável; mudar exige novo plano.
- Origem do custo de cada item é sempre devolvida: `estoque` (preço médio) ou `ultima_compra`.
- O backend calcula custo, CMV, solver, regras e necessidade de compra. O frontend só exibe e permite travar.

## Modelo (a detalhar no Gate 02)

- **Grupo de clientes**: nome, refeições contratadas, **preço por pessoa por refeição**, CMV alvo padrão, desvio diário máximo padrão.
- **Modelo de refeição** do grupo: funções e quantidade de cada uma por refeição.
- **Função** da ficha técnica e **etiquetas** (ex.: `feijoada`, `feijao-preto`, `frango`, `suino`, `peixe`) usadas nas regras. A ficha técnica atual não muda de comportamento.
- **Grupo de equivalência** de itens (ex.: feijão carioca, feijão preto, feijão de corda). O solver só troca ingredientes dentro do grupo.
- **Regra do grupo**, no formato: *quando* a refeição tiver uma ficha com a etiqueta X, *exigir* (ou *proibir*) uma ficha da função Y com (ou sem) a etiqueta Z. Também: não repetir a mesma etiqueta em N dias seguidos.
- **Plano**: nome, período, grupos incluídos, CMV alvo do período, status (rascunho, aprovado), `version`.
- **Posição do plano**: grupo, dia, refeição, função, pessoas, adesão, ficha escolhida, travada ou não.
- **Solicitação de compra** e linhas: plano de origem, item, necessidade, saldo, quantidade a comprar, data em que o item acaba. Emissão idempotente por `operation_id`.

## Cálculo

- Consumo por item = soma das quantidades brutas (quantidade × fator de correção) das fichas, proporcional às porções da posição (pessoas × adesão ÷ rendimento da ficha).
- CMV da refeição = custo de todas as posições da refeição ÷ (pessoas × preço por pessoa daquela refeição do grupo). Também por dia, por grupo e no período.
- Compra = máx(0, necessidade do período − saldo atual). A data em que o item acaba é o primeiro dia em que o consumo acumulado passa do saldo.
- Quantidades na unidade do item, sem conversão (igual à ficha técnica).

## Solver

- Roda no backend, sem dependência externa, **determinístico** (mesma entrada, mesma saída) e com limite de tempo.
- Respeita sempre: travas, regras do grupo, modelo de refeição, candidatas por função e grupos de equivalência. Nunca devolve plano que quebre uma trava ou regra.
- Objetivo, em ordem: (1) CMV de cada grupo no período o mais perto possível do alvo, dentro do desvio diário; (2) mais preparações compartilhadas entre grupos no mesmo dia (ganho de escala); (3) mais variedade.
- Explica o resultado: cada troca (de → para, economia), o CMV antes e depois por grupo e, quando não alcança o alvo, a regra ou trava que impede e o CMV mais próximo possível.
- Só sugere: o plano muda quando o operador aceita.

## Tela (resumo, detalhes no Gate 07)

- Grade por grupo: linhas = funções do modelo de refeição, colunas = dias (igual ao cardápio impresso hoje).
- Cada célula mostra a ficha, as porções e um cadeado para travar. O CMV aparece por dia e no período.
- "Recalcular com o solver" mostra as trocas sugeridas para aceitar ou recusar.
- Regras quebradas aparecem na célula, com o motivo e o que fazer.

## Pontos para confirmar antes do Gate 02

1. **Preço por pessoa por refeição** de cada grupo (ex.: almoço Data Center = R$ X por pessoa). É o que os contratos cobram? O CMV por prato da ficha técnica continua existindo, mas o planner usa este preço.
2. **Rendimento em quilos**: a folha de produção pede quilos (198 kg de arroz). A ficha hoje rende em porções. Para gerar quilos é preciso o peso da porção na ficha.
3. **Ganho de escala**: entra só como preferência do solver (desempate), ou deve ter peso definido pelo operador?
4. **Retrato na aprovação** (custos gravados) como descrito acima.

## Gates

1. Este contrato.
2. Migração (grupos de clientes, modelo de refeição, função, etiquetas, equivalência, regras, plano, posições, compra, permissão `planning`) testada como no CI.
3. Custo com fallback de última compra, com prova em teste.
4. API do plano e avaliação de CMV (refeição, dia, grupo, período) e das regras.
5. Solver com testes: determinismo, travas, regras (caso feijoada), compensação, escala, alvo inalcançável.
6. Solicitação de compra com data de ruptura e idempotência.
7. Telas conforme `guia-de-estilo-frontend.md`.
8. Auditoria, CI verde no SHA exato e promoção só com aprovação explícita.
