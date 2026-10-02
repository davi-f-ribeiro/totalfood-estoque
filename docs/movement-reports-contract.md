# Relatórios de movimentação — contrato Gate 01

Baseline: `main@bafaa7745b2ed3172c574e17a9710749fe9967f7`.

Este documento fixa o contrato antes da implementação. Não cria endpoint, tabela, snapshot nem segundo motor de estoque.

## Fonte de verdade

- `movements` + `movement_lines` permanecem como única fonte de verdade do estoque.
- Saldo é sempre derivado de `IN - OUT`; não haverá coluna ou tabela de saldo materializado.
- Saldo histórico usa a mesma semântica do saldo atual, limitada por `movements.occurred_at < limite_exclusivo`.
- Custo/preço médio histórico reutiliza a fórmula canônica atual: valor líquido acumulado dividido pela quantidade líquida acumulada no mesmo corte temporal.
- Quando o saldo for zero, preço médio é zero, preservando a semântica atual de `getStockSnapshot`.

## Períodos

Todos os intervalos de movimentação são semiabertos: `[início, fim)`.

- Hoje: início do dia local até início do próximo dia.
- Semana até hoje (WTD): domingo 00:00 local até o início do dia seguinte ao atual. Domingo é o primeiro dia da semana para este produto.
- Mês até hoje (MTD): primeiro dia do mês 00:00 local até o início do dia seguinte ao atual.
- Personalizado: datas informadas pelo usuário convertidas para o mesmo modelo `[início, próximo-dia-do-fim)`.

O backend recebe limites temporais inequívocos; não fabrica 23:59:59.999.

## Relatórios

### Entradas
Somente `movements.movement_type = 'ENTRADA'`. Transferências recebidas não são entradas comerciais.

Campos mínimos: data/hora, produto, SKU, depósito, quantidade, unidade, preço/custo unitário, documento, fornecedor e responsável quando disponíveis.

### Saídas
Somente `movements.movement_type = 'SAIDA'`. Transferências enviadas não são saídas comerciais.

Campos mínimos: data/hora, produto, SKU, depósito, quantidade, unidade, preço/custo médio registrado na saída, documento e responsável quando disponíveis.

### Saldo
Saldo por SKU e depósito na data de corte, com quantidade, unidade e preço médio histórico. A opção geral pode agregar depósitos sem alterar o ledger.

## Inventário

A conclusão de inventário atualmente não grava ajuste em `movement_lines`. Portanto o relatório representa saldo contábil do ledger, não substitui a contagem física. Alterar a política de ajuste de inventário está fora deste escopo.

## Reutilização obrigatória

1. Não criar tabela de relatório/saldo/snapshot.
2. Não duplicar cálculo de estoque no frontend.
3. Não criar consulta distinta para CSV: prévia e exportação devem consumir a mesma estrutura de dados.
4. Reutilizar autenticação, permissões, depósitos, catálogo e relações existentes.
5. Reutilizar a fórmula de quantidade/valor/preço médio de `getStockSnapshot`; eventual extração de helper só é permitida se reduzir duplicação sem mudar a semântica.
6. Índice novo somente após evidência de `EXPLAIN`/teste de performance no Gate 07.

## Gates

1. Contrato e baseline isolado.
2. Prova automatizada da reconstituição temporal do saldo e preço médio.
3. Consulta canônica com data de corte.
4. Leitura de entradas/saídas por período e depósito.
5. Tela Relatórios.
6. Exportação reutilizando o mesmo resultado.
7. Auditoria funcional, permissões e performance.
8. CI completo, diff final e promoção somente após aprovação explícita.
