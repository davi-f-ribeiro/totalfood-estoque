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
10. **Per capita por grupo.** A mesma preparação rende porções de tamanho diferente conforme o contrato. Exemplo: bife à parmegiana com 120 g de proteína pronta por pessoa no Data Center e 200 g na Base Aérea. O per capita muda a quantidade a produzir, o consumo de ingredientes, a necessidade de compra e o CMV de cada grupo.
11. Permissão nova `planning`: Diretor lê e escreve, Gerente só lê. Estoquista e Vendas não veem. Quem não tem `costs` não vê valores no plano.

## O que os cardápios reais mostram

- **A refeição tem uma estrutura fixa por grupo.** Quentinhas: 2 proteínas, farofa ou pirão, arroz, feijão e macarrão. Self Service: o mesmo, mais salada. Data Center almoço: 2 proteínas, ovo frito, arroz, feijão, macarrão, farofa ou pirão, guarnição, salada, suco e fruta ou doce. Data Center café: café, leite, fruta, pão, cuscuz com proteína.
- Cada função do modelo de refeição tem **per capita** (em gramas, mililitros ou unidades, de produto pronto) **por grupo**. O per capita da proteína da Base Aérea é maior que o do Data Center, e isso vale para a mesma ficha.
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
- **Per capita do contrato**: por grupo e função (ex.: Base Aérea, proteína = 200 g; Data Center, proteína = 120 g). Quando o contrato fixa uma quantidade para um tipo de prato, ela é cadastrada no contrato do grupo, não na ficha.
- **Peso da porção pronta** na ficha técnica (g, ml ou unidade): quanto de produto pronto uma porção da ficha entrega. Sem ele, a ficha não entra no planner e a tela diz qual ficha completar.
- **Função** da ficha técnica e **etiquetas** (ex.: `feijoada`, `feijao-preto`, `frango`, `suino`, `peixe`) usadas nas regras. A ficha técnica atual não muda de comportamento.
- **Grupo de equivalência** de itens (ex.: feijão carioca, feijão preto, feijão de corda). O solver só troca ingredientes dentro do grupo.
- **Regra do grupo**, no formato: *quando* a refeição tiver uma ficha com a etiqueta X, *exigir* (ou *proibir*) uma ficha da função Y com (ou sem) a etiqueta Z. Também: não repetir a mesma etiqueta em N dias seguidos.
- **Plano**: nome, período, grupos incluídos, CMV alvo do período, status (rascunho, aprovado), `version`.
- **Posição do plano**: grupo, dia, refeição, função, pessoas, adesão, ficha escolhida, travada ou não.
- **Solicitação de compra** e linhas: plano de origem, item, necessidade, saldo, quantidade a comprar, data em que o item acaba. Emissão idempotente por `operation_id`.

## Cálculo

- Produto pronto da posição = pessoas × adesão × per capita do grupo (g).
- Porções-equivalentes da ficha = produto pronto da posição ÷ peso da porção pronta da ficha. A ficha continua sendo a receita de uma porção-padrão; o planner escala por esse fator.
- Consumo por item = soma das quantidades brutas (quantidade × fator de correção) das fichas, ÷ rendimento da ficha × porções-equivalentes.
- Custo da posição escala na mesma proporção: 200 g custa 200/120 do custo de 120 g da mesma ficha.
- CMV da refeição = custo de todas as posições da refeição ÷ (pessoas × preço por pessoa daquela refeição do grupo). Também por dia, por grupo e no período.
- A mesma ficha em grupos diferentes gera **linhas de produção por grupo** (cada uma com seu per capita e suas pessoas) e **um lote consolidado** (soma dos grupos). A ordem de produção mostra os dois: o lote para produzir e a divisão por grupo para porcionar e expedir.
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

## Decisões fechadas (Gate 01 concluído)

1. **Preço por refeição, por grupo.** Cada grupo tem um preço por pessoa para cada refeição que contratou (café X, almoço Y, jantar Z...). O CMV de uma refeição é o custo da refeição inteira ÷ (pessoas × preço daquela refeição).
2. **Quantidade por pessoa.** A ficha técnica descreve a porção de **uma pessoa**. Produção = pessoas × adesão × quantidade da porção. Exemplo: arroz com porção de 200 g para 900 pessoas = 180 kg. Quando o per capita do contrato do grupo difere da porção da ficha, a quantidade escala pela proporção (200 g contratados sobre 120 g da ficha = 1,67 porção da ficha por pessoa).
3. **Ganho de escala prevalece quando o custo é próximo.** Se a opção compartilhada entre grupos custar quase o mesmo que a mais barata, o solver escolhe a compartilhada (menos gás, menos setup). "Quase o mesmo" é uma **tolerância configurável por plano**, padrão **3%** do custo da refeição. Acima da tolerância, vence o custo menor.
4. **O contrato manda na quantidade.** O per capita vem do contrato do grupo (por função). Não há exceção por ficha que contrarie o contrato: se o contrato prevê uma quantidade específica para um tipo de prato, ela é cadastrada no próprio contrato do grupo. O planner nunca usa a quantidade da ficha para contrariar o contrato.
5. **Per capita é de produto pronto.** O cru vem do fator de correção e do rendimento da ficha. A ficha precisa ter o **peso da porção pronta**; sem ele, não entra no planner.
6. **Retrato na aprovação** (custos e quantidades gravados no plano aprovado), como descrito em "Fonte de verdade".

## Gates

1. Este contrato.
2. Migração (grupos de clientes, modelo de refeição, função, etiquetas, equivalência, regras, plano, posições, compra, permissão `planning`) testada como no CI.
3. Custo com fallback de última compra, com prova em teste.
4. API do plano e avaliação de CMV (refeição, dia, grupo, período) e das regras.
5. Solver com testes: determinismo, travas, regras (caso feijoada), compensação, escala, alvo inalcançável.
6. Solicitação de compra com data de ruptura e idempotência.
7. Telas conforme `guia-de-estilo-frontend.md`.
8. Auditoria, CI verde no SHA exato e promoção só com aprovação explícita.
