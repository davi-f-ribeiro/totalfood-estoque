# Plano de correção — situação

Origem: revisão de arquitetura e qualidade de 14 e 15/11/2026 (`main` em `1b96502`, depois `5302df5`). Este documento acompanha a situação de cada item; atualize-o no mesmo PR que resolver um item.

Conferido com o código em 16/11/2026 (`main` em `bca575d`, depois do #40): não há PR novo depois do #40, e os itens abertos 1.8, 1.9, 2.4, 2.5 e porta 9100 foram conferidos no código e continuam abertos (os acentos da etiqueta não foram conferidos). Na mesma data entrou a seção **Layout**, com a versão 2 do contrato de layout (`guia-de-estilo-frontend.md`).

Legenda: **feito** · **parcial** · **aberto** · **decisão** (depende do responsável pelo produto).

## O que falta, em resumo

- **Servidor:** 1.8 (separar handler e SQL em pacotes) e 1.9 (`app.js` com 127 KB em 418 linhas).
- **Impressora e ESP32:** 2.4 (filtro por depósito na fila), 2.5 (consultar `~HS` antes de confirmar o envio), porta 9100 sem autenticação e acentos na etiqueta (`^CI28`).
- **Telas:** decisão sobre o menu do celular por perfil e as abas de Caixas.
- **Layout:** contrato reescrito e aplicado no sistema e no firmware. Falta conferir com dados reais as telas de L5 e L13, gravar o firmware 1.7.0 (L14) e rodar o Playwright (L15).

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

## Layout (contrato de layout, versão 2)

Origem: revisão visual de 16/11/2026, com mockup da tela de Estoque (computador e celular). Objetivo: tirar da interface o que parece modelo pronto (painel em volta de tudo, cartão dentro de cartão, avisos em caixa pastel, faixas laterais, cinzas e sombras de kit de interface) e deixar a identidade vir da logo. As regras estão em `guia-de-estilo-frontend.md`, seções 2 a 5 e 8. Valem para o sistema e para as páginas do ESP32. Tudo que está como feito ou parcial nesta seção entrou no #41.

| Item | Situação | Onde |
|---|---|---|
| L1 Contrato de layout reescrito (cores, forma, tipografia, componentes, navegação, ESP32) | feito | `guia-de-estilo-frontend.md`, versão 2 |
| L2 Variáveis de `app.css`: paleta da logo (`#80BB04`, preto, branco, cinzas neutros), um raio de 4 px, sem sombra | feito: os nomes antigos (`-soft`, `-border`, `--accent`, `--info`, `--focus-ring`, raios e sombras) ficaram como apelidos; apagar de vez entra junto com o 1.9 | `app.css`, bloco `:root` |
| L3 Fonte Barlow (400 e 600) servida pelo sistema; pesos 500, 700 e 800 trocados | feito: WOFF2 e licença em `static/fonts`; a CSP (`default-src 'self'`) não precisou mudar | `static/fonts`, todos os CSS |
| L4 Página sem painel: `.panel` sem borda, fundo e raio | feito | `app.css`, bloco "Contrato de layout v2" |
| L5 Cartão dentro de cartão vira linha: `sku-row`, `sku-editor`, `permission-group`, `inventory-line`, `inventory-create-card`, `inventory-admin-card`, `inventory-review-grid`, `catalog-parent-card` | parcial: feito no CSS e conferido em Catálogo, Produtos e Administração; Contagem só foi vista vazia | `app.css` |
| L6 Estoque: tabela sem moldura, cabeçalho com linha preta, linha aberta em cinza; no celular, linhas | feito: o detalhe continua em uma linha por marca, que carrega as embalagens só quando abre (não virou subtabela) | `app.css`, `app.js` |
| L7 Situação "Faltam X" no lugar de "Abaixo do mínimo" / "Normal": `/api/stock` devolve `shortage`; Início deixou de calcular a falta no navegador | feito, com teste | `stock_view.go`, `ui.go`, `app.js`, `home.js` |
| L8 Avisos em texto: `.operation-feedback` e `.product-feedback` sem fundo e sem borda; `.impact-warning` com contorno preto | feito | `app.css` |
| L9 Faixas laterais, bolinhas e pílulas de situação (`.inventory-review-line`, `.list-action.selected`, `.low`, `.badge`, `.dispatch-chip`, `.driver-state`, `.driver-sync`, `.material-tag`) | feito | `app.css` e CSS dos módulos |
| L10 Botões e links: texto preto sobre o verde; ação de texto preta e sublinhada; secundário com contorno `--border-strong`; alternância ligada em preto | feito | `app.css` |
| L11 Navegação: nome dos grupos sem caixa alta; item atual com fundo verde e texto preto no menu lateral e na barra inferior; cabeçalho sem slogan | feito: o rótulo da barra inferior continua em 12 px (exceção registrada no guia, seção 3) | `app.css` |
| L12 Entrada no sistema sem cartão | feito | `app.css` |
| L13 CSS dos módulos (`batch`, `movement`, `crates`, `dispatch`, `driver`, `planner`, `recipes`, `material_requests`, `logistics`, `home`) | parcial: molduras, pílulas, cores fixas e sombras saíram de todos; com dados só foram conferidos Início, Entrada, Saída e Caixas. Falta abrir com dados reais Requisições, Expedição, Minha rota, Fichas técnicas, Cardápio e Locais e frota | CSS dos módulos |
| L14 ESP32: `web_pages.h` com as mesmas variáveis; `.panel` sem moldura; situação em texto; abas sublinhadas; `.chip` neutro; `theme-color` branco; firmware 1.7.0 | parcial: feito no código e conferido no navegador; falta o `firmware-ci` compilar e gravar no aparelho | `firmware/etiqueta_zd220` |
| L15 Testes de contrato atualizados junto (`ui_test.go`, `prehot_corrections_test.go`) | parcial: `go test` e `firmware/tests` passam; os roteiros do Playwright (`scripts/*.cjs`) não foram rodados nesta mudança | — |

Próximos passos: abrir com dados reais as telas listadas em L13 e a Contagem (L5), gravar o firmware 1.7.0 no ESP (L14) e rodar os roteiros do Playwright (L15).

## Outros achados

| Item | Situação |
|---|---|
| Healthcheck do Compose usa `wget`, que a imagem final não tem | feito: `/app/estoque healthcheck` |
| Hortifruti aparecia no Estoque com a marca interna `__LEGACY_UNASSIGNED__`, e não havia onde ver as caixas ativas | feito: item sem marca aparece como "Sem marca" (Estoque, Produtos, Catálogo, Requisições e relatório); lista "Caixas ativas" na tela Caixas e no detalhe do Estoque (`GET /api/crates`); teste `TestActiveCratesListAndUnbrandedLabel` |
| Zerar o peso de uma caixa mandava uma etiqueta nova (de 0 kg) para a fila | feito: caixa vazia não gera etiqueta e cancela a pendente; teste `TestCrateWeighedToZeroQueuesNoLabel` |
| Abrir o sistema em outra aba derrubava o envio da primeira com 403 (o token CSRF era sorteado a cada página carregada), e a tela mostrava "Você não tem permissão" para qualquer 403 | feito: token fixo por sessão (`csrfForSession`), a tela mostra o motivo que o servidor devolve; teste `TestCSRFTokenSurvivesOpeningAnotherPage` |
| `resetProductionAccess` tinha teste, mas nada na aplicação a chamava | feito: removida (5S) |
| Requisições: a cozinha só conseguia confirmar a entrega inteira (auditoria de 16/11/2026) | feito: recebimento parcial com motivo, situação "Recebida com divergência" e confirmação do restante pela cozinha solicitante ou pela gerência, sem movimentar estoque (migration 041; `docs/requisicoes-materiais.md`); teste `TestMaterialPartialReceiptDivergenceAndReconciliation` |
