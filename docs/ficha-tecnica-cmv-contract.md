# Ficha técnica e CMV — contrato Gate 01

Este documento fixa o contrato antes da implementação e complementa o `global-engineering-contract.md`.

## Decisões do produto

- A ficha guarda só o **custo** do prato. Não há preço de venda na ficha: o preço é **por refeição e por grupo de clientes** (`customer_group_meals`, no planner de cardápio). O CMV por refeição é calculado pelo planner: custo das porções ÷ faturamento da refeição.
- O custo vem do **preço médio do estoque** de cada ingrediente. Não há custo digitado na ficha.
- Ingrediente é o **item** do estoque (ex.: "Leite"), não a marca nem a embalagem.
- A ficha tem **rendimento** (porções por receita) e **fator de correção** por ingrediente.
- Sem sub-receitas e sem importação de vendas nesta fase.
- Custos, fichas e CMV exigem a permissão nova `costs`. Diretor lê e escreve; Gerente só lê. Estoquista e Vendas não veem.

## Fonte de verdade

- `movement_lines` continua sendo a única fonte do custo. Nada de custo materializado na ficha.
- Preço médio do item = valor líquido acumulado dividido pela quantidade líquida acumulada (`IN − OUT`), somando todos os depósitos e marcas. É a mesma fórmula de `getStockSnapshotAt` e de `listBalanceReport`, sem corte temporal (custo atual).
- Transferências gravam entrada e saída ao mesmo custo, então não alteram o preço médio do item.
- Quando a quantidade líquida é zero, o preço médio é zero (semântica atual). Nesse caso o ingrediente aparece como **sem custo**.

## Cálculo

Por linha da ficha:

    quantidade bruta = quantidade × fator de correção
    custo da linha   = quantidade bruta × preço médio do item

Por ficha:

    custo da porção = soma dos custos das linhas ÷ porções

- A quantidade é digitada na **unidade do item** (a mesma de `inventory_items.unit_code`). Não há conversão entre unidades nesta fase.
- Se qualquer ingrediente estiver sem custo, a ficha é marcada como **custo incompleto**: o custo da porção não é calculado (`null`). Mostrar valor parcial induziria a erro. O custo de cada linha continua visível, e a linha sem custo aparece como tal.
- Toda a aritmética é feita em SQL com `numeric`. O frontend não calcula custo nem CMV.

## Modelo de dados

Tabelas novas, todas necessárias porque não há equivalente no schema:

- `customers`: cliente (nome único sem diferenciar maiúsculas, `version`).
- `recipes`: ficha técnica (nome único, porções, observação, `version`).
- `recipe_lines`: ingredientes da ficha (item, quantidade, fator de correção).
- `recipe_customer_prices`: removida pela migração 033. Preço de venda não pertence à ficha.

Nenhum índice novo além das unicidades de nome. Sem evidência de consulta lenta, o contrato global proíbe índice por hipótese.

## API

Todas sob `costs`. Escrita exige `costs/write`.

- `GET/POST /api/customers`, `PATCH /api/customers/{id}`
- `GET/POST /api/recipes`, `GET/PATCH /api/recipes/{id}`

`PATCH` exige `version` e troca ingredientes e preços da ficha de forma atômica. Versão antiga devolve 409 com a mensagem padrão de edição concorrente.

## Gates

1. Contrato (este documento).
2. Migration e permissão `costs`, testadas no mesmo mecanismo do CI.
3. Cálculo canônico de custo do item e da porção, com prova automatizada.
4. API de clientes e fichas técnicas, com permissões e concorrência.
5. Telas: Clientes, Fichas técnicas e CMV, seguindo o guia de estilo.
6. Auditoria de permissões, diff completo e CI no SHA exato. Promoção só com aprovação explícita.
