# Plano de correção — situação

Origem: revisão de arquitetura e qualidade de 14 e 15/11/2026 (`main` em `1b96502`, depois `5302df5`). Este documento acompanha a situação de cada item; atualize-o no mesmo PR que resolver um item.

Legenda: **feito** · **parcial** · **aberto** · **decisão** (depende do responsável pelo produto).

## Servidor e repositório

| Item | Situação | Onde |
|---|---|---|
| 1.1 Bootstrap do Diretor com variáveis diferentes no Compose e no código | feito | #30 |
| 1.2 Limite de login contando acertos e agrupando todos atrás do proxy | feito | #30 (`CLIENT_IP_HEADER`, `TRUSTED_PROXY_HOPS`) |
| 1.3 Erros do banco identificados pelo texto da mensagem (`strings.Contains(err.Error(), ...)`) | feito: código SQLSTATE e nome exato da constraint (`db_errors.go`); "assets_tag" casava também a validação `assets_tag_check` | #40 |
| 1.4 `WriteTimeout` de 10 s para todas as rotas | feito: prazo próprio para relatórios, planejamento e fotos de entrega; a foto ampliava só a leitura, e a resposta podia se perder | #40 |
| 1.5 Dois arquivos de migration com número 033 | feito: número repetido agora reprova o teste; o 033 fica como exceção registrada | 5S |
| 1.6 README, `check.sh` e documentação de deploy desatualizados | feito: deploy documentado só para Render + Neon (Cloud Run descartado) | 5S |
| 1.7 CI verificando só parte dos arquivos JavaScript | feito: `node --check` em todos os JS de `static/` | 5S |
| 1.8 Todo o servidor em `package main`, com handler e SQL juntos | aberto | — |
| 1.9 `app.js` com 127 KB em poucas linhas | aberto | — |

## Impressora Zebra e ESP32

| Item | Situação | Onde |
|---|---|---|
| 2.2 ESP travado quando o WMS recusava a confirmação | feito | #29 (firmware 1.6.2) |
| Cadeia de certificado do Render recusada pelo ESP | feito | #29 (firmware 1.6.3) |
| Fila travada por trabalho sem caixa (503 em produção) | feito | #33 (migration 040) |
| Etiqueta de teste sem movimentar estoque | feito | #33 |
| 2.3 Reserva: prazo de 5 min e confirmação tardia | feito | #27 |
| 2.3 Reserva vencida só expira quando alguma impressora busca a fila | feito: expira também ao listar a fila, as impressoras e ao enviar teste | #39 |
| 2.4 Qualquer impressora pega qualquer etiqueta de caixa (sem filtro por depósito) | aberto | — |
| 2.5 Etiqueta marcada como enviada sem consultar papel, tampa e pausa da Zebra (`~HS`) | aberto | — |
| 2.6 Fila do ESP exige login e recusa a senha padrão | feito | #27 |
| 2.6 Operador precisava da senha de administrador do ESP para imprimir | feito: fila sem senha numa aba própria (rede interna fechada, decisão de 15/11) | #37 (firmware 1.6.4) |
| 2.6 Porta 9100 aberta sem autenticação | aberto | — |
| 2.7 Falha de banco na autenticação da impressora respondia 401 | feito | #29 |
| 2.7 `last_seen_at` só atualiza quando há trabalho | feito: atualiza a cada busca e aparece em Administração | #39 |
| 2.7 Comentário órfão na migration 039 | feito: aponta para `allocateLots`, onde a regra está | #39 |
| 2.7 `^CI28` com texto só ASCII (acentos removidos na etiqueta) | aberto: muda a impressão, validar com a Zebra | — |
| 2.8 Testes: token revogado, reserva antiga após reenvio, outra impressora, teste endereçado | feito | #29, #33 |
| 2.8 Testes: falha seguida de reenvio; cancelamento ao gerar nova revisão | feito | #39 |

## Telas

| Item | Situação | Onde |
|---|---|---|
| Caixas: busca sumia sem dizer o motivo | feito | #31 |
| Caixas: aviso fora da tela no celular, dica de KG apagada, espaçamento | feito | #34 |
| Configuração de impressoras misturada à operação de Caixas | feito | #32 |
| Menu inferior: ícones faltando e rótulos sobrepostos com 8 itens | feito | #31 |
| Requisições: todas as mensagens no topo da página, inclusive as do detalhe; mensagem genérica "Operação registrada." (contrato global, seção 3) | feito | #36 |
| Caixas: a busca filtra KG no navegador depois do limite de 10 resultados do servidor | feito: `unit=KG` filtra no banco | #40 |
| Menu do celular com 4 atalhos por perfil; abas na tela de Caixas | decisão | — |
| Saída no celular um produto por vez (pedido de 15/11: lote como no desktop) | feito: Saída em lote no celular, com câmera | #38 |

## Outros achados

| Item | Situação |
|---|---|
| Healthcheck do Compose usa `wget`, que a imagem final não tem | feito: `/app/estoque healthcheck` |
| `resetProductionAccess` tinha teste, mas nada na aplicação a chamava | feito: removida (5S) |
