# Relatório de teste — TotalFood Estoque

## 1. Resumo

Execução de 16/11/2026 (Fortaleza), branch `main`, commit `40cdd82e2efd5a8b7e5a7abc0217c4c28e72a75a`. Teste no Docker local em `127.0.0.1:5656`. Seção 5.10 excluída. A rota `/fiscal` entrou apenas na matriz de navegação da seção 5.11, sem leitura de QR, confirmação ou requisição fiscal externa. Os módulos da segunda etapa ficaram fora dos roteiros. A suíte existente foi executada como fornecida.

**147 casos no escopo · 126 passaram · 21 falharam · 0 não executados integralmente.** `PERM-01` e `PERM-02` são os dois controles da seção 5.11. Os casos NF-01 a NF-03 ficam fora desses números.

As movimentações, as duas etapas de recebimento parcial, a entrega com cinco caixas, a contagem e o ajuste de produto comum funcionaram na base real. Os principais achados são os perfis de origem sem acesso ao estoque, a tela restaurada pelo histórico após sair, o erro 500 em ajuste de caixa e a ausência de ajustes no relatório exigido pelo plano.

Os testes usaram Playwright 1.56.1 com Chrome local em 390×844 e 1366×900. Casos de tela e chamadas à API de verdade estão diferenciados nos passos. A câmera foi simulada no navegador, com busca e persistência reais. A impressora foi simulada via curl. Segue o ambiente registrado.

```text
Docker 29.1.3
Docker Compose version 2.40.3+ds1-0ubuntu1
repo-app sha256:b356fe506da05d4217e426497627686f1b41bc4181a50c67e2d595101207aa02
sha256:6b75136145ddabf3d9a4b69660402082de7c56b734cfe50555b0674774dd742a ["postgres@sha256:a77aa70e641402a21411b92b721c58d10f23b50ae5fdd2333efd22d166e1e5f7"]
postgres (PostgreSQL) 17.12
go version go1.26.8 linux/amd64
v22.23.3
Google Chrome 156.0.8078.79 
1.56.1
en_US.utf8|en_US.utf8|c
```

### Preparação

O banco do Compose foi iniciado com volume novo. /health respondeu 200 após inicialização. O primeiro curl foi cedo demais e recebeu conexão encerrada. A tabela schema_migrations registrou os 43 arquivos, e a recriação do app com bootstrap desativado preservou versões e horários. O bootstrap foi desligado depois da criação das contas antes das operações de estoque. O reset de senha a cada partida com bootstrap ligado está no código, mas não atrapalhou depois de desligado.

O Diretor entrou pela tela do Playwright e cadastrou quatro contas pela Administração. A configuração inicial dos perfis foi capturada antes da intervenção. Para prosseguir na instância de teste, foram acrescentadas permissões de leitura de estoque/catálogo, entrada, saída e transferência ao Estoquista e de estoque, catálogo, relatórios, contagem e ajuste ao Gerente. A Cozinha conservou as permissões iniciais.

Na preparação por API autenticada, 8 gravações bem-sucedidas levaram aproximadamente 385 ms de ponta a ponta no ambiente local, além da tentativa de SKU sem marca recusada. Foram feitas uma ativação de depósito e entradas posteriores. O tempo mede a automação e não a operação manual. Para continuar os fluxos de caixa após CAT-04, foi usada marca fictícia. A busca de mínimo nulo usou o item legado Água Mineral. REL-02 acrescentou uma fixture de SKU legado diretamente no banco local com brand_id=0, usando o mesmo padrão de TestActiveCratesListAndUnbrandedLabel. O recebimento e as consultas foram feitos pela aplicação. Evidências em [fixture.json](teste-evidencias/fixture.json), [setup.json](teste-evidencias/setup.json), [profiles-before.txt](teste-evidencias/profiles-before.txt) e [profile-grants.json](teste-evidencias/profile-grants.json).

## 2. Suíte automática

| Comando | Resultado | Tempo de parede | Evidência |
|---|---|---:|---|
| `go vet ./...` | passou | 0s | [vet.log](teste-evidencias/vet.log) |
| `go test -count=1 ./... (com PostgreSQL, modo verboso)` | passou | 398s | [go-db-verbose.log](teste-evidencias/go-db-verbose.log) |
| `go test -race -count=1 -timeout 30m ./... (com PostgreSQL, modo verboso)` | passou | 1052s | [race-db.log](teste-evidencias/race-db.log) |
| `go build ./...` | passou | 0s | [build-time.log](teste-evidencias/build-time.log) |
| `node --check (todos os *.js em cmd/server/static)` | passou | 1s | [js-time.log](teste-evidencias/js-time.log) |
| `python3 -m unittest discover -s firmware/tests -p test_*.py -v` | passou | 0s | [firmware-time.log](teste-evidencias/firmware-time.log) |
| `docker build -t totalfood-estoque:teste .` | passou | 0s | [docker-build-time.log](teste-evidencias/docker-build-time.log) |
| `npm install playwright@1.56.1` | passou | 2s | [npm-time.log](teste-evidencias/npm-time.log) |
| `node scripts/crates-playwright-e2e.cjs` | passou | 4s | [crates-pw-time.log](teste-evidencias/crates-pw-time.log) |
| `node scripts/exit-batch-mobile-playwright-e2e.cjs` | passou | 9s | [exit-pw-time.log](teste-evidencias/exit-pw-time.log) |
| `node scripts/material-requests-playwright-e2e.cjs` | passou | 3s | [requests-pw-time.log](teste-evidencias/requests-pw-time.log) |
| `go test -count=3 ./cmd/server (com PostgreSQL, timeout padrão de 10 min)` | interrompido pelo timeout padrão | 600s | [repeats-db-early.log](teste-evidencias/repeats-db-early.log) |
| `go test -count=3 -timeout 35m ./cmd/server (com PostgreSQL, modo verboso)` | falhou | 1215s | [repeats-db-35m.log](teste-evidencias/repeats-db-35m.log) |
| `TestWarehouseNameIsUniqueForTracking` com banco | passou | 1,765 s em Go | [warehouse-unique.log](teste-evidencias/warehouse-unique.log) |

Os três roteiros JavaScript da seção 4 avaliam a interface com API simulada. Os casos da seção 5 usam outra execução, com login real. Tempos de parede estão arredondados ao segundo. 0 s representa duração abaixo da resolução da medição.

- Go com banco — 342 resultados PASS, 0 FAIL e 0 SKIP no log final. [go-db-verbose.log](teste-evidencias/go-db-verbose.log)
- Go race com banco — 342 resultados PASS, 0 FAIL e 0 SKIP no log final. [race-db.log](teste-evidencias/race-db.log)
- Três repetições com banco — 1023 resultados PASS, 3 FAIL e 0 SKIP no log final. [repeats-db-35m.log](teste-evidencias/repeats-db-35m.log)

- Instabilidade entre as três rodadas — nenhuma detectada.
- A primeira execução com limite padrão atingiu 10 min. A execução confirmatória usou limite de 35 min, preservando os testes, o banco isolado e o commit.
- `TestWarehouseMigration019IncrementalAndIdempotent` falhou na suíte de repetição em banco reutilizado com `permission=1 index=2 triggers=10`. O teste consulta pg_indexes e pg_trigger sem restringir o schema. A execução interrompida aos 10 min deixou um schema residual, que aparece na inspeção junto ao schema da execução ativa. Em outro banco vazio, o mesmo teste passou nas três chamadas. Evidência em [warehouse-m019-scopes.txt](teste-evidencias/warehouse-m019-scopes.txt), [warehouse-m019-fresh.log](teste-evidencias/warehouse-m019-fresh.log) e [repeats-db-35m.log](teste-evidencias/repeats-db-35m.log). O TRV-09 verificou idempotência das migrações no banco da aplicação.
- O teste do nome de depósito passou no PostgreSQL 17 Alpine desta rodada. Também houve recusa HTTP 409 de nome repetido com caixa e espaços alterados na instância do app. O banco de teste usou en_US.utf8. A divergência de locale informada pelo plano não se reproduziu neste ambiente.

## 3. Casos

| ID | Resultado | Observação | Evidência |
|---|---|---|---|
| ACE-01 | passou | Usuário ou senha incorretos. Senha inicial levou à troca obrigatória. | [real-access.json](teste-evidencias/real-access.json) |
| ACE-02 | passou | Troca exigida ao abrir /stock. Após salvar, /home disponível. | [real-access.json](teste-evidencias/real-access.json) |
| ACE-03 | passou | Sem sessão, /stock redirecionou a /login. | [real-access.json](teste-evidencias/real-access.json) |
| ACE-04 | passou | Cozinha foi redirecionada a /no-access, sem menu administrativo ou dados da tela. HTTP 200 pertence à tela de destino. | [capturas/ACE-04-real-roles-layout.png](teste-evidencias/capturas/ACE-04-real-roles-layout.png), [real-roles-layout.json](teste-evidencias/real-roles-layout.json) |
| ACE-05 | passou | Produto salvo após abrir outra aba (HTTP 201). | [real-session-proof.json](teste-evidencias/real-session-proof.json) |
| ACE-06 | falhou | Servidor devolveu HTTP 401 “Entre para continuar.” Produtos fechou o formulário e exibiu “Cadastro sem confirmação” com “Enviar de novo”. | [capturas/ACE-06-real-session-proof.png](teste-evidencias/capturas/ACE-06-real-session-proof.png), [real-session-proof.json](teste-evidencias/real-session-proof.json) |
| ACE-07 | falhou | Voltar abriu /home com conteúdo e navegação em cache. /api/me retornou 401 e recarregar redirecionou a /login. | [capturas/ACE-07-historico-antes-reload.png](teste-evidencias/capturas/ACE-07-historico-antes-reload.png), [real-session-proof.json](teste-evidencias/real-session-proof.json) |
| ACE-08 | passou | 403 {"error":"Sua sessão expirou. Atualize a página."} | [real-roles-layout.json](teste-evidencias/real-roles-layout.json) |
| ACE-09 | passou | Troca HTTP 200; senha anterior recusada e nova aceita. | [real-mov-access-retry.json](teste-evidencias/real-mov-access-retry.json) |
| ADM-01 | passou | Usuário ou senha incorretos. | [real-admin.json](teste-evidencias/real-admin.json) |
| ADM-02 | falhou | Senha redefinida aceita. O novo login entrou diretamente em /home, sem obrigar a troca. | [capturas/ADM-02-real-admin.png](teste-evidencias/capturas/ADM-02-real-admin.png), [real-admin.json](teste-evidencias/real-admin.json) |
| ADM-03 | passou | Perfil Leitor Lab 869591072 abre Produtos e API recusa gravação com 403. | [real-admin.json](teste-evidencias/real-admin.json) |
| ADM-04 | passou | A remoção da permissão revogou a sessão. A próxima consulta foi recusada com HTTP 401 “Entre para continuar.” | [capturas/ADM-04-real-admin.png](teste-evidencias/capturas/ADM-04-real-admin.png), [real-admin.json](teste-evidencias/real-admin.json) |
| ADM-05 | passou | {"error":"Já existe um depósito com este nome."} | [real-mov-catalog.json](teste-evidencias/real-mov-catalog.json) |
| ADM-06 | passou | 409 {"error":"Não é possível desativar: o depósito ainda tem estoque ou patrimônio."} | [real-mov-retry.json](teste-evidencias/real-mov-retry.json) |
| ADM-07 | passou | HTTP 201 no primeiro cadastro. O token apareceu uma vez e desapareceu ao recarregar. O nome já usado retornou HTTP 409 na rodada seguinte. | [capturas/ADM-07-real-crates.png](teste-evidencias/capturas/ADM-07-real-crates.png), [real-crates.json](teste-evidencias/real-crates.json) |
| ADM-08 | falhou | A tela mostrou identificadores internos delivery e dispatch na lista de permissões. | [capturas/ADM-08-real-final-ui.png](teste-evidencias/capturas/ADM-08-real-final-ui.png), [real-final-ui.json](teste-evidencias/real-final-ui.json) |
| CAT-01 | passou | Três códigos encontrados na API e item KG visível em Produtos; cadastros por API autenticada. | [real-mov-catalog.json](teste-evidencias/real-mov-catalog.json) |
| CAT-02 | passou | 409 {"error":"Este código de barras já está cadastrado."} | [real-mov-catalog.json](teste-evidencias/real-mov-catalog.json) |
| CAT-03 | passou | SKU saiu da busca, movimento de entrada permaneceu e item/marca retornaram 200. | [real-inventory-catalog-extra.json](teste-evidencias/real-inventory-catalog-extra.json) |
| CAT-04 | falhou | Criar SKU de Melão com brand_id=0 via API retorna HTTP 400 e exige marca. O restante do teste de caixas usou um SKU com marca fictícia. | [capturas/CAT-04-real-mov-catalog.png](teste-evidencias/capturas/CAT-04-real-mov-catalog.png), [real-mov-catalog.json](teste-evidencias/real-mov-catalog.json) |
| CAT-05 | passou | Itens e marcas paginaram (30 → 60, 30 → 60); formulários à base do viewport. | [real-inventory-catalog-extra.json](teste-evidencias/real-inventory-catalog-extra.json) |
| CAT-06 | falhou | Botão “+ Novo produto” apareceu para usuário somente com leitura. O backend recusou a escrita com HTTP 403. | [capturas/CAT-06-real-admin.png](teste-evidencias/capturas/CAT-06-real-admin.png), [real-admin.json](teste-evidencias/real-admin.json) |
| MOV-01 | passou | Duas linhas no formulário e feedback visível. Posição no VR com preço médio por item de 4,20/kg e 2,50/un. | [real-integrity-final.json](teste-evidencias/real-integrity-final.json) |
| MOV-02 | passou | 404 {"error":"Item 2: produto não encontrado.","line_index":1}; estoque preservado. | [real-mov-retry.json](teste-evidencias/real-mov-retry.json) |
| MOV-03 | passou | 409 {"error":"Item 1: estoque insuficiente. Só há 420.000 UN.","line_index":0}; saldo 420 | [real-mov-retry.json](teste-evidencias/real-mov-retry.json) |
| MOV-04 | passou | Simulador de câmera: 1 → 2; controles 2 → 3 → 2; confirmar visível e sem overflow. | [real-mov-access-retry.json](teste-evidencias/real-mov-access-retry.json) |
| MOV-05 | passou | Entrada e Transferência em três passos, um produto e revisão visível. | [real-mov-access-retry.json](teste-evidencias/real-mov-access-retry.json) |
| MOV-06 | passou | 420,0 → 400,20 | [real-mov-retry.json](teste-evidencias/real-mov-retry.json) |
| MOV-07 | passou | Repetido: [2,3]. Corpo diferente: 409. | [real-mov-retry.json](teste-evidencias/real-mov-retry.json) |
| MOV-08 | passou | Envio que não chegou ao servidor: conferência encontrou ausência, reenvio com mesmo ID e uma baixa. Resposta perdida após gravação coberta no roteiro anterior. | [real-final-ui.json](teste-evidencias/real-final-ui.json) |
| MOV-09 | passou | 409 {"error":"Este estoque tem caixas identificadas. Registre o consumo pela caixa."} | [real-crates.json](teste-evidencias/real-crates.json) |
| MOV-10 | passou | HTTP 200 / 409; restante 5 | [real-mov-retry.json](teste-evidencias/real-mov-retry.json) |
| EST-01 | passou | Quatro cabeçalhos e coluna numérica alinhada à direita. | [real-stock-retry.json](teste-evidencias/real-stock-retry.json) |
| EST-02 | passou | 2 faltas em vermelho. Água Mineral mostra sem mínimo; Item arquivamento 870365660 editado sem texto de situação. | [real-validation-final.json](teste-evidencias/real-validation-final.json) |
| EST-03 | passou | 2 faltas conferidas contra o servidor. | [real-stock-retry.json](teste-evidencias/real-stock-retry.json) |
| EST-04 | passou | Marca Lab 869414320 — 342,7 KG Arroz Lab 869414320 - Marca Lab 869414320 - 1 KG 1 un = 1 KG · EAN 7098694143201 Dar saída Dar entrada | [real-stock-retry.json](teste-evidencias/real-stock-retry.json) |
| EST-05 | passou | CX-000001, VR, recebida em 16/11/2026 19,3 KG | [real-stock-retry.json](teste-evidencias/real-stock-retry.json) |
| EST-06 | falhou | Ao trocar VR por Câmara Fria, os controles mantiveram busca e filtro, mas voltaram a aparecer Melão e Pote. | [capturas/EST-06-real-stock-retry.png](teste-evidencias/capturas/EST-06-real-stock-retry.png), [real-stock-retry.json](teste-evidencias/real-stock-retry.json) |
| EST-07 | falhou | Atalhos e quantidade de falta presentes. A coluna “Falta” mostra apenas o número, em divergência com “Faltam X” em Estoque. | [capturas/EST-07-real-stock-retry.png](teste-evidencias/capturas/EST-07-real-stock-retry.png), [real-stock-retry.json](teste-evidencias/real-stock-retry.json) |
| EST-08 | passou | Largura 358 px, cantos retos, sem sombra e sem rolagem horizontal. | [real-stock-retry.json](teste-evidencias/real-stock-retry.json) |
| CX-01 | passou | CX-000001 19.300 \| CX-000002 18.900 \| CX-000003 22.000 e três etiquetas PENDING. | [real-crates.json](teste-evidencias/real-crates.json) |
| CX-02 | passou | 3 caixas, 60,2 kg | [real-crates.json](teste-evidencias/real-crates.json) |
| CX-03 | passou | Pendente da revisão 1 substituído; peso 17.300; revisão 2. | [real-crates.json](teste-evidencias/real-crates.json) |
| CX-04 | passou | 409 {"error":"O aumento de peso exige uma nova entrada ou uma correção auditada."} | [real-crates.json](teste-evidencias/real-crates.json) |
| CX-05 | falhou | Banco: CANCELLED\|Caixa vazia. A fila apresenta Substituída e não exibe o motivo Caixa vazia. | [capturas/CX-05-real-print-retry.png](teste-evidencias/capturas/CX-05-real-print-retry.png), [real-print-retry.json](teste-evidencias/real-print-retry.json) |
| CX-06 | passou | CX-000003 agora no depósito 2 com 22.000 kg. | [real-crates.json](teste-evidencias/real-crates.json) |
| CX-07 | passou | CX-000001 · Melão Japonês Lab 869414320 · Peso atual 19,3 kg · NF LAB-CX-01 | [real-crates.json](teste-evidencias/real-crates.json) |
| CX-08 | passou | HTTP 200, ZPL no corpo, X-Print-Claim e X-Print-Job-ID presentes. | [real-crates.json](teste-evidencias/real-crates.json) |
| CX-09 | passou | Trabalho 1 {"id":1,"status":"SENT"}. | [real-crates.json](teste-evidencias/real-crates.json) |
| CX-10 | passou | Trabalho 5 expirado em UNCERTAIN e reenviado para PENDING. Reenvio por botão confirmado HTTP 200. | [real-print-retry.json](teste-evidencias/real-print-retry.json) |
| CX-11 | falhou | FAILED e UNCERTAIN confirmados. A UI descarta os motivos que estão em print_jobs.last_error. | [capturas/CX-11-real-print-retry.png](teste-evidencias/capturas/CX-11-real-print-retry.png), [real-print-retry.json](teste-evidencias/real-print-retry.json) |
| CX-12 | passou | 401 ambos, sem conteúdo da etiqueta. | [real-crates.json](teste-evidencias/real-crates.json) |
| CX-13 | passou | ^CI28 \| ^FO16,94^A0N,28,23^FB448,2,3,L,0^FDMELAO JAPONES LAB 869414320^FS (renderização física da Zebra fora desta etapa). | [real-crates.json](teste-evidencias/real-crates.json) |
| CX-14 | passou | Pareamento e listagem sem vínculo de depósito; confirma a limitação 2.4. | [real-crates.json](teste-evidencias/real-crates.json) |
| REQ-01 | passou | #14 com dois materiais, HTTP 201 e PENDENTE. | [real-requests-ui.json](teste-evidencias/real-requests-ui.json) |
| REQ-02 | passou | Separação confirmou HTTP 201 na tela. No fluxo básico, o pedido ficou EM_SEPARACAO, o estoque físico não mudou e uma saída acima do livre recebeu HTTP 409. | [capturas/REQ-02-real-requests-ui.png](teste-evidencias/capturas/REQ-02-real-requests-ui.png), [real-requests-ui.json](teste-evidencias/real-requests-ui.json) |
| REQ-03 | passou | Reserva sem quantidade restante; pedido voltou a PENDENTE. | [real-requests.json](teste-evidencias/real-requests.json) |
| REQ-04 | passou | Entrega 1; movimento 15; saída de 6 kg. | [real-requests.json](teste-evidencias/real-requests.json) |
| REQ-05 | passou | Arroz Lab 869414320, 6 KG; Marca Lab 869414320, Arroz Lab 869414320 - Marca Lab 869414320 - 1 KG, VR | [real-requests-ui.json](teste-evidencias/real-requests-ui.json) |
| REQ-06 | passou | Entrega conferida em 6 kg. | [real-requests.json](teste-evidencias/real-requests.json) |
| REQ-07 | passou | #1 RECEBIDA. | [real-requests.json](teste-evidencias/real-requests.json) |
| REQ-08 | passou | Novo: CANCELADA. Com entrega: 409 {"error":"Já houve entrega. Confirme o recebimento em vez de cancelar."} | [real-requests.json](teste-evidencias/real-requests.json) |
| REQ-09 | passou | 403 {"error":"Somente o solicitante ou gerente pode confirmar recebimento."} | [real-requests.json](teste-evidencias/real-requests.json) |
| REQ-10 | passou | 403 {"error":"Você não tem permissão para fazer isso."} | [real-requests.json](teste-evidencias/real-requests.json) |
| REQ-11 | passou | 400 {"error":"Informe o motivo da diferença entre o entregue e o recebido."} | [real-requests.json](teste-evidencias/real-requests.json) |
| REQ-12 | passou | 400 {"error":"A quantidade recebida não pode ser maior que a entregue (200.000)."} | [real-requests.json](teste-evidencias/real-requests.json) |
| REQ-13 | passou | 200 kg entregues e 180 kg conferidos com motivo. API mostrou RECEBIDA_COM_DIVERGENCIA e 20 kg pendentes. O mesmo componente exibiu a falta em texto no fluxo móvel de 102,9 kg. | [real-requests.json](teste-evidencias/real-requests.json) |
| REQ-14 | passou | Replay sem alteração; corpo diferente 409 {"error":"Esta entrega já foi conferida com outra quantidade. Atualize a página."} | [real-requests.json](teste-evidencias/real-requests.json) |
| REQ-15 | passou | RECEBIDA; 3 registros de recebimento. | [real-requests.json](teste-evidencias/real-requests.json) |
| REQ-16 | passou | 409 {"error":"Só faltam 20.000 para receber nesta entrega. Atualize a página."} | [real-requests.json](teste-evidencias/real-requests.json) |
| REQ-17 | passou | A posição de estoque permaneceu idêntica e a entrega conservou seu movimento durante a conferência e as duas conciliações. | [real-integrity-final.json](teste-evidencias/real-integrity-final.json) |
| REQ-18 | passou | Zero aceito, 2 unidades em divergência. | [real-requests.json](teste-evidencias/real-requests.json) |
| REQ-19 | passou | 110 kg aceitos; mais 20: 409 {"error":"Quantidade acima do saldo ainda solicitado. Itens em KG aceitam até 20% acima do pedido."} | [real-requests.json](teste-evidencias/real-requests.json) |
| REQ-20 | passou | 108,7 kg aceitos e pedido ENTREGUE. | [real-requests.json](teste-evidencias/real-requests.json) |
| REQ-21 | passou | Não permitiu 110 un para pedido de 106 un. A recusa de UN menciona 20% para KG, desnecessário nesse contexto. | [real-requests.json](teste-evidencias/real-requests.json) |
| REQ-22 | passou | ENTREGA_PARCIAL e botão visível para quem solicitou. | [real-requests-ui.json](teste-evidencias/real-requests-ui.json) |
| REQ-23 | passou | 409 {"error":"Ainda não há entrega para aceitar. Para desistir do pedido, use Cancelar."} \| 409 {"error":"Confira o recebimento de cada entrega antes de encerrar."} | [real-requests.json](teste-evidencias/real-requests.json) |
| REQ-24 | passou | 403 {"error":"Você não tem permissão para fazer isso."} | [real-requests.json](teste-evidencias/real-requests.json) |
| REQ-25 | passou | Pedido RECEBIDA com ENCERRADA no histórico, reserva liberada. | [real-requests-ui.json](teste-evidencias/real-requests-ui.json) |
| REQ-26 | passou | 409 {"error":"Esta requisição não aceita novas entregas."} | [real-requests.json](teste-evidencias/real-requests.json) |
| REQ-27 | passou | Este produto é controlado por caixas. Marque as caixas que vão sair. | [real-requests-ui.json](teste-evidencias/real-requests-ui.json) |
| REQ-28 | passou | Quantidade 102,9, somente leitura. | [real-requests-ui.json](teste-evidencias/real-requests-ui.json) |
| REQ-29 | passou | Uma entrega, movimento 45, cinco linhas OUT, soma 102,9 kg e cinco caixas zeradas. | [real-integrity-final.json](teste-evidencias/real-integrity-final.json) |
| REQ-30 | passou | 15 kg: HTTP 409; 14 kg: entrega 10. | [real-requests.json](teste-evidencias/real-requests.json) |
| REQ-31 | passou | CX-000014 ativa com 5 kg e nova etiqueta. | [real-requests.json](teste-evidencias/real-requests.json) |
| REQ-32 | passou | 409 {"error":"Com várias caixas, cada uma sai inteira: a quantidade tem de ser 102.900 kg, a soma dos pesos atuais. Para tirar só parte, selecione uma caixa."} | [real-requests.json](teste-evidencias/real-requests.json) |
| REQ-33 | passou | 409 {"error":"A caixa CX-000008 não é do produto e do depósito separados, ou já está vazia. Atualize a página."} | [real-requests.json](teste-evidencias/real-requests.json) |
| REQ-34 | passou | HTTP 201 / 409; uma saída e caixa vazia. | [real-requests.json](teste-evidencias/real-requests.json) |
| REQ-35 | passou | Estado integral preservado em REQ-27 e REQ-32. | [real-requests.json](teste-evidencias/real-requests.json) |
| REQ-36 | passou | Entrega e recebimento parcial a 390 px, sem rolagem lateral; ficaram 1,9 kg pendentes. | [real-requests-ui.json](teste-evidencias/real-requests-ui.json) |
| INV-01 | passou | #6 snapshot em 2026-11-16T23:00:08.362357Z, 3 SKUs. | [real-inventory-retry.json](teste-evidencias/real-inventory-retry.json) |
| INV-02 | passou | 409 {"error":"Este depósito não tem produtos para contar."} | [real-inventory-retry.json](teste-evidencias/real-inventory-retry.json) |
| INV-03 | passou | Não revelou saldo na tela nem nas respostas de criação, contagem e metadados. | [real-final-ui.json](teste-evidencias/real-final-ui.json) |
| INV-04 | passou | Quantidade igual, divergente e zero aceitos; última linha muda para REVIEW. | [real-inventory-retry.json](teste-evidencias/real-inventory-retry.json) |
| INV-05 | passou | 400 {"error":"Quantidade contada inválida."} \| 400 {"error":"Quantidade contada inválida."} \| 400 {"error":"Quantidade contada inválida."} | [real-inventory-retry.json](teste-evidencias/real-inventory-retry.json) |
| INV-06 | passou | {"known":true,"in_inventory":true,"sku_id":26} | [real-inventory-retry.json](teste-evidencias/real-inventory-retry.json) |
| INV-07 | passou | Primeira contagem preservada ao retornar. | [real-inventory-retry.json](teste-evidencias/real-inventory-retry.json) |
| INV-08 | passou | 409 {"error":"Outra pessoa alterou esta contagem. Atualize a página antes de salvar."} | [real-replay.json](teste-evidencias/real-replay.json) |
| INV-09 | passou | Revisão mostra divergência por texto; API registra estados distintos. | [real-inventory-retry.json](teste-evidencias/real-inventory-retry.json) |
| INV-10 | passou | {"error":"Este item já foi recontado."} \| {"error":"Só itens com divergência podem ser recontados."} | [real-inventory-retry.json](teste-evidencias/real-inventory-retry.json) |
| INV-11 | passou | Após recontar cada divergência, a conclusão sem observação retornou 400. Com observação, ficou COMPLETED. | [real-inventory-retry.json](teste-evidencias/real-inventory-retry.json) |
| INV-12 | passou | Estoque integralmente preservado. | [real-inventory-retry.json](teste-evidencias/real-inventory-retry.json) |
| INV-13 | passou | Movimento 40 e histórico. Sobra de 2 un a 3,20 (preço médio). | [real-validation-final.json](teste-evidencias/real-validation-final.json) |
| INV-14 | passou | 409 {"error":"Esta divergência já foi ajustada."} \| 409 {"error":"Esta divergência já foi ajustada."} | [real-inventory-retry.json](teste-evidencias/real-inventory-retry.json) |
| INV-15 | passou | 409 {"error":"O estoque atual é menor que a redução do ajuste."} | [real-inventory-catalog-extra.json](teste-evidencias/real-inventory-catalog-extra.json) |
| INV-16 | passou | 409 {"error":"Não há custo válido para registrar o ajuste."} | [real-replay.json](teste-evidencias/real-replay.json) |
| INV-17 | passou | 403 {"error":"Você não tem permissão para fazer isso."} | [real-inventory-retry.json](teste-evidencias/real-inventory-retry.json) |
| INV-18 | passou | CANCELLED / CANCELLED / HTTP 409 | [real-inventory-retry.json](teste-evidencias/real-inventory-retry.json) |
| INV-19 | passou | 409 {"error":"Esta contagem não aceita mais alterações."} \| 409 {"error":"Esta contagem não aceita mais alterações."} | [real-inventory-retry.json](teste-evidencias/real-inventory-retry.json) |
| INV-20 | passou | Entrada aceita; revisão 20.000 kg, atual 22 kg. Aviso visual requer conferência. | [real-inventory-retry.json](teste-evidencias/real-inventory-retry.json) |
| INV-21 | falhou | Conta o peso por SKU. Ao ajustar redução de 10 kg do lote de duas caixas, HTTP 500 “Erro interno.”, saldo e pesos preservados. Na tela, “O ajuste ficou sem confirmação”. | [capturas/INV-21-repro.png](teste-evidencias/capturas/INV-21-repro.png), [real-validation-final.json](teste-evidencias/real-validation-final.json) |
| INV-22 | passou | Válido: 200; inválido 400 {"error":"Filtro de status inválido."} | [real-inventory-retry.json](teste-evidencias/real-inventory-retry.json) |
| INV-23 | passou | Dez linhas gravadas, 20 ações de campo/salvar, além da digitação e de rolar. Chrome emulado sem teclado virtual do sistema operacional. | [real-inventory-catalog-extra.json](teste-evidencias/real-inventory-catalog-extra.json) |
| REL-01 | passou | Duas linhas no VR para LAB-MOV-01. Arroz 350 kg a 4,20 (custo registrado). | [real-stock-retry.json](teste-evidencias/real-stock-retry.json) |
| REL-02 | passou | Posição bate por item. Fixture SQL marca 0 apareceu como “Sem marca” na API de relatório e no detalhe de Estoque. | [real-validation-final.json](teste-evidencias/real-validation-final.json) |
| REL-03 | passou | Hoje selecionado com fundo preto. | [real-stock-retry.json](teste-evidencias/real-stock-retry.json) |
| REL-04 | passou | Tela com estado vazio. Consulta de 26 anos: 44 linhas em 7 ms. | [real-validation-final.json](teste-evidencias/real-validation-final.json) |
| REL-05 | falhou | Pesagem e entrega REQ-n apareceram. O ajuste entrou na posição de estoque e ficou fora do relatório de movimentações. API com tipo AJUSTE retornou HTTP 400. | [capturas/REL-05-real-cross.png](teste-evidencias/capturas/REL-05-real-cross.png), [real-cross.json](teste-evidencias/real-cross.json) |
| PERM-01 | falhou | A grade abaixo preserva os quatro perfis antes de qualquer concessão. Gerente e Estoquista só abriram Requisições dentre as 13 rotas. | [capturas/PERM-01-real-roles-layout.png](teste-evidencias/capturas/PERM-01-real-roles-layout.png), [real-roles-layout.json](teste-evidencias/real-roles-layout.json) |
| PERM-02 | passou | Entrada: 403 \| Saída: 403 \| Transferência: 403 \| Caixas: 403 \| Catálogo: 403 \| Separação: 403 \| Entrega: 403 \| Ajuste: 403 \| Administração: 403 | [real-roles-layout.json](teste-evidencias/real-roles-layout.json) |
| LAY-01 | passou | Pesos 400/600 registrados; recursos somente em /static/fonts/. | [real-roles-layout.json](teste-evidencias/real-roles-layout.json) |
| LAY-02 | passou | 26 capturas e inspeção de estilo computado. | [real-roles-layout.json](teste-evidencias/real-roles-layout.json) |
| LAY-03 | passou | 26 capturas e inspeção de estilo computado. | [real-roles-layout.json](teste-evidencias/real-roles-layout.json) |
| LAY-04 | passou | 26 capturas e inspeção de estilo computado. | [real-roles-layout.json](teste-evidencias/real-roles-layout.json) |
| LAY-05 | falhou | Barra inferior a 11 px frente à exceção de 12 px. Textos de listas de Catálogo, Produtos e Administração no desktop a 13,33 px. | [capturas/LAY-05-real-roles-layout.png](teste-evidencias/capturas/LAY-05-real-roles-layout.png), [real-roles-layout.json](teste-evidencias/real-roles-layout.json) |
| LAY-06 | passou | Painéis de página sem contorno fechado ou fundo colorido nas capturas. | [real-final-ui.json](teste-evidencias/real-final-ui.json) |
| LAY-07 | passou | Situações em texto no detalhe de Requisições e Contagem, com registros e capturas nas duas larguras. | [real-final-ui.json](teste-evidencias/real-final-ui.json) |
| LAY-08 | falhou | Catálogo, Produtos, Contagem, Relatórios e Administração não indicaram a página atual na barra inferior fechada de 390 px. | [capturas/LAY-08-real-roles-layout.png](teste-evidencias/capturas/LAY-08-real-roles-layout.png), [real-roles-layout.json](teste-evidencias/real-roles-layout.json) |
| LAY-09 | passou | Nenhuma violação automática de color-contrast. Regras adicionais em axe-full.json, incluindo aria-required-parent, aria-required-children. | [real-full-axe.json](teste-evidencias/real-full-axe.json) |
| LAY-10 | passou | Ciclos completos de Tab nas 26 aberturas, 745 focos registrados. | [real-validation-final.json](teste-evidencias/real-validation-final.json) |
| LAY-11 | falhou | Sem rolagem lateral nas aberturas medidas. Em Caixas, link de Administração com 17 px de altura, abaixo dos 44 px. | [capturas/LAY-11-real-roles-layout.png](teste-evidencias/capturas/LAY-11-real-roles-layout.png), [real-roles-layout.json](teste-evidencias/real-roles-layout.json) |
| LAY-12 | falhou | O slogan de login “Qualidade todo dia!” contém ponto de exclamação. | [capturas/LAY-12-real-roles-layout.png](teste-evidencias/capturas/LAY-12-real-roles-layout.png), [real-roles-layout.json](teste-evidencias/real-roles-layout.json) |
| ESP-01 | passou | 15 variáveis declaradas no firmware com o mesmo valor. | [real-esp.json](teste-evidencias/real-esp.json) |
| ESP-02 | passou | 10 capturas; sem achados nos elementos visíveis da montagem estática. | [real-esp.json](teste-evidencias/real-esp.json) |
| ESP-03 | passou | FW_VERSION 1.7.0. O workflow publicou totalfood-esp32-s3-n16r8-v1.7.0 no run 43848227831. Arquivos de firmware e workflow idênticos aos do SHA testado. | [real-esp.json](teste-evidencias/real-esp.json) |
| ESP-04 | passou | Saída 0 (14 testes registrados no firmware.log). | [real-esp.json](teste-evidencias/real-esp.json) |
| TRV-01 | falhou | 20 rotas exercitadas. Encerramento de requisição respondeu 200 com o mesmo operation_id e observação alterada. Os demais replays exercitados mantiveram o resultado. | [capturas/TRV-01-real-replay.png](teste-evidencias/capturas/TRV-01-real-replay.png), [real-replay.json](teste-evidencias/real-replay.json) |
| TRV-02 | passou | Pesagens HTTP 200/409, peso e lote 2 kg; MOV-10 e REQ-34 verificados. | [real-cross.json](teste-evidencias/real-cross.json) |
| TRV-03 | passou | 34 pares SKU/depósito, 15 lotes, 2 reservas agregadas; estoques visíveis conciliados. | [real-integrity-final.json](teste-evidencias/real-integrity-final.json) |
| TRV-04 | falhou | Dois HTTP 500 no mesmo ajuste de lote controlado por caixa, primeiro via API e depois na interface. Nenhum outro 5xx na trilha registrada. | [capturas/TRV-04-real-integrity-final.png](teste-evidencias/capturas/TRV-04-real-integrity-final.png), [real-integrity-final.json](teste-evidencias/real-integrity-final.json) |
| TRV-05 | falhou | Ajuste de caixa devolve “Erro interno.”, sem dizer que a baixa precisa ocorrer pela caixa. | [capturas/TRV-05-real-final-ui.png](teste-evidencias/capturas/TRV-05-real-final-ui.png), [real-final-ui.json](teste-evidencias/real-final-ui.json) |
| TRV-06 | passou | CSP self, nosniff, HttpOnly e SameSite=Strict; credenciais de teste e cookie da sessão ausentes nos logs revisados. | [real-integrity-final.json](teste-evidencias/real-integrity-final.json) |
| TRV-07 | passou | Sessão e saldo 391 un preservados após restart. | [real-mov-access-retry.json](teste-evidencias/real-mov-access-retry.json) |
| TRV-08 | passou | Medições: /stock 1366 px 652 ms \| /stock 390 px 584 ms \| /home 1366 px 600 ms \| /material-requests 1366 px 617 ms \| /reports 1366 px 598 ms \| /material-requests 390 px 602 ms \| /reports 390 px 565 ms | [real-stock-retry.json](teste-evidencias/real-stock-retry.json) |
| TRV-09 | passou | 43 migrações 001–042; registros e timestamps idênticos após recriar o app. | [real-integrity-final.json](teste-evidencias/real-integrity-final.json) |
| TRV-10 | falhou | Entradas, caixas, requisição, recebimento parcial, encerramento, pesagem, contagem e ajuste concluídos. Ledger bateu linha a linha. Relatórios omitiram o ajuste. | [capturas/TRV-10-real-cross.png](teste-evidencias/capturas/TRV-10-real-cross.png), [real-cross.json](teste-evidencias/real-cross.json) |

### Matriz de acesso inicial

Cada célula mostra presença no menu e abertura pela URL. “Bloqueou” indica redirecionamento a /no-access. O HTTP final dessa tela é 200 e não contém dados da tela pedida.

| Rota | Diretor | Gerente | Estoquista | Cozinha |
|---|---|---|---|---|
| `/home` | Sim / abriu | Não / bloqueou | Não / bloqueou | Não / bloqueou |
| `/stock` | Sim / abriu | Não / bloqueou | Não / bloqueou | Não / bloqueou |
| `/entry` | Sim / abriu | Não / bloqueou | Não / bloqueou | Não / bloqueou |
| `/exit` | Sim / abriu | Não / bloqueou | Não / bloqueou | Não / bloqueou |
| `/transfer` | Sim / abriu | Não / bloqueou | Não / bloqueou | Não / bloqueou |
| `/crates` | Sim / abriu | Não / bloqueou | Não / bloqueou | Não / bloqueou |
| `/material-requests` | Sim / abriu | Sim / abriu | Sim / abriu | Sim / abriu |
| `/catalog` | Sim / abriu | Não / bloqueou | Não / bloqueou | Não / bloqueou |
| `/products` | Sim / abriu | Não / bloqueou | Não / bloqueou | Não / bloqueou |
| `/inventory` | Sim / abriu | Não / bloqueou | Não / bloqueou | Não / bloqueou |
| `/reports` | Sim / abriu | Não / bloqueou | Não / bloqueou | Não / bloqueou |
| `/admin` | Sim / abriu | Não / bloqueou | Não / bloqueou | Não / bloqueou |
| `/fiscal` | Não / abriu | Não / bloqueou | Não / bloqueou | Não / bloqueou |

Cozinha recebeu 403 em todas as nove tentativas de gravação (entrada, saída, transferência, caixas, catálogo, separação, entrega, ajuste e administração). [kitchen-denied.json](teste-evidencias/kitchen-denied.json)
 A coleta adicional a 390 px após as concessões está em [permissions-390-configured.json](teste-evidencias/permissions-390-configured.json).

### Achados adicionais do axe-core

| Regra | Ocorrências nas aberturas |
|---|---:|
| `aria-required-children` | 4 |
| `aria-required-parent` | 19 |

Em Início, o axe encontrou linhas sem o contexto de tabela esperado. Nas telas de lote vazias, encontrou tabelas sem os elementos filhos exigidos. Seletores e mensagens por tela e largura em [axe-full.json](teste-evidencias/axe-full.json).

## 4. Defeitos

### PERM-01 · bloqueia operação

**Passos.** Criar Gerente e Estoquista com os perfis do banco novo. Entrar e abrir Estoque, Entrada e Relatórios. **Esperado.** Gerente acessa as telas de gestão pertinentes e Estoquista pode receber, retirar, transferir e operar caixas. **Obtido.** Ambos têm somente Requisições entre as 13 rotas testadas. Estoquista também recebe 403 nas APIs de movimentação.

Evidência — [permissions-matrix.json](teste-evidencias/permissions-matrix.json), [profiles-before.txt](teste-evidencias/profiles-before.txt), [real-mov-catalog.log](teste-evidencias/real-mov-catalog.log), [capturas/PERM-01-real-roles-layout.png](teste-evidencias/capturas/PERM-01-real-roles-layout.png).

### ACE-07 · dado errado

**Passos.** Entrar como Diretor, abrir Início, clicar em Sair e voltar no navegador. Em seguida chamar /api/me e recarregar. **Esperado.** A sessão encerrada impede visualizar a página anterior. **Obtido.** Início foi exibida com o menu e conteúdo, mesmo com /api/me em 401. Recarregar voltou para login.

Evidência — [logout-history.json](teste-evidencias/logout-history.json), [capturas/ACE-07-historico-antes-reload.png](teste-evidencias/capturas/ACE-07-historico-antes-reload.png).

### INV-21 / TRV-04 / TRV-05 · bloqueia operação

**Passos.** Receber caixas de 3 e 7 kg. Abrir contagem, contar zero, recontar zero, concluir com observação e aplicar ajuste da redução. **Esperado.** Preservar rastreabilidade e orientar a baixa pela caixa quando o ajuste agregado não for permitido. **Obtido.** Resposta 500 com Erro interno. O log diz stock is tracked by individual crates. Pesos e saldo continuam em 10 kg.

Evidência — [crate-adjust-result.json](teste-evidencias/crate-adjust-result.json), [server-errors.json](teste-evidencias/server-errors.json), [app.log](teste-evidencias/app.log), [capturas/INV-21-repro.png](teste-evidencias/capturas/INV-21-repro.png).

### REL-05 / TRV-10 · dado errado

**Passos.** Concluir o fluxo do dia com ajuste de uma unidade, consultar Entradas e Saídas no depósito e tentar type=AJUSTE. **Esperado.** O relatório exigido neste plano explica todas as quantidades finais linha a linha. **Obtido.** O ledger e a posição fecham. A API de movimentações aceita apenas ENTRADA e SAIDA e rejeita AJUSTE com 400.

Evidência — [day-flow.json](teste-evidencias/day-flow.json), [day-flow-reports.json](teste-evidencias/day-flow-reports.json), [capturas/REL-05-real-cross.png](teste-evidencias/capturas/REL-05-real-cross.png).

### CAT-04 · bloqueia operação

**Passos.** Criar item Melão, cadastrar embalagem e código de barras com brand_id=0. **Esperado.** Criar produto sem marca e mostrá-lo como Sem marca. **Obtido.** Cadastro recusou com HTTP 400 e exigiu marca. Estoque de caixas prosseguiu com marca fictícia.

Evidência — [real-mov-catalog.json](teste-evidencias/real-mov-catalog.json), [http.jsonl](teste-evidencias/http.jsonl), [capturas/CAT-04-real-mov-catalog.png](teste-evidencias/capturas/CAT-04-real-mov-catalog.png).

### ADM-02 · uso ruim

**Passos.** Redefinir a senha de usuário temporário em Administração. Abrir contexto novo e autenticar com a nova senha. **Esperado.** Solicitar nova troca antes de entrar. **Obtido.** Entrou em /home. A redefinição altera o hash e revoga sessões, mas não marca must_change_password.

Evidência — [real-admin.json](teste-evidencias/real-admin.json), [capturas/ADM-02-real-admin.png](teste-evidencias/capturas/ADM-02-real-admin.png).

### ACE-06 · uso ruim

**Passos.** Abrir formulário novo de Produtos em uma aba. Sair pela outra. Submeter o formulário aberto. **Esperado.** Pedir novo login e preservar o contexto. **Obtido.** HTTP 401 com Entre para continuar. A tela fechou o formulário e ofereceu Enviar de novo como se o resultado fosse incerto.

Evidência — [two-tab-logged-out.json](teste-evidencias/two-tab-logged-out.json), [logout-form-visibility.json](teste-evidencias/logout-form-visibility.json), [capturas/ACE-06-real-session-proof.png](teste-evidencias/capturas/ACE-06-real-session-proof.png).

### EST-06 · dado errado

**Passos.** Em Estoque, marcar Só abaixo do mínimo, buscar Pote e trocar VR por Câmara Fria. **Esperado.** A nova lista continua restrita ao Pote abaixo do mínimo. **Obtido.** O checkbox e a busca mantêm os valores, mas aparecem Pote e Melão. Alterar o campo de busca reaplica o filtro.

Evidência — [stock-filter-change.json](teste-evidencias/stock-filter-change.json), [capturas/EST-06-real-stock-retry.png](teste-evidencias/capturas/EST-06-real-stock-retry.png).

### CX-05 / CX-11 · uso ruim

**Passos.** Zerar caixa com etiqueta pendente. Depois pedir dois trabalhos e devolver fail e uncertain com motivo. **Esperado.** Mostrar a causa Caixa vazia e os motivos de falha/incerteza na fila. **Obtido.** Cancelamento e estados persistem. A fila chama o cancelado de Substituída e omite last_error nos três casos.

Evidência — [print-empty-diagnostics.json](teste-evidencias/print-empty-diagnostics.json), [print-state-diagnostics.json](teste-evidencias/print-state-diagnostics.json), [capturas/CX-11-fila.png](teste-evidencias/capturas/CX-11-fila.png).

### CAT-06 · uso ruim

**Passos.** Entrar com perfil de catálogo somente leitura e abrir Produtos. **Esperado.** Ausência da ação de criação. **Obtido.** Botão + Novo produto aparece. Escrita via API continua bloqueada por 403.

Evidência — [real-admin.json](teste-evidencias/real-admin.json), [capturas/CAT-06-real-admin.png](teste-evidencias/capturas/CAT-06-real-admin.png).

### TRV-01 · uso ruim

**Passos.** Encerrar requisição com operation_id fixo, repetir o corpo e repetir com observação diferente. **Esperado.** Repetição idêntica é segura, corpo diferente com o mesmo ID é recusado. **Obtido.** Os três envios retornaram 200. A regra de conflito de corpo não foi observada no encerramento.

Evidência — [replay-detail.json](teste-evidencias/replay-detail.json), [capturas/TRV-01-real-replay.png](teste-evidencias/capturas/TRV-01-real-replay.png).

### EST-07 · cosmético

**Passos.** Comparar a falta de um produto nas telas Estoque e Início. **Esperado.** Mesma regra visual e textual da situação, com Faltam X. **Obtido.** Início usa coluna Falta e somente quantidade/unidade.

Evidência — [real-stock-retry.json](teste-evidencias/real-stock-retry.json), [capturas/EST-07-real-stock-retry.png](teste-evidencias/capturas/EST-07-real-stock-retry.png).

### ADM-08 · uso ruim

**Passos.** Selecionar Estoquista na lista de perfis e ler o agrupamento de permissões. **Esperado.** Rótulos em português. **Obtido.** A tela exibe os identificadores delivery e dispatch.

Evidência — [permission-labels.json](teste-evidencias/permission-labels.json), [capturas/ADM-08-real-final-ui.png](teste-evidencias/capturas/ADM-08-real-final-ui.png).

### LAY-05 / LAY-08 / LAY-11 / LAY-12 · cosmético

**Passos.** Abrir a navegação e os cadastros em 390 e 1366 px. Medir texto, item ativo e alvos de toque. **Esperado.** Pelo menos 14 px, salvo barra a 12 px. Indicação da página atual. Alvos de 44 px. Texto sem exclamação. **Obtido.** Barra medida a 11 px. Texto de listas a 13,33 px no desktop. Administração em Caixas tem 17 px de altura no celular. Cinco páginas em Mais não indicaram a atual na barra fechada. Login mostra Qualidade todo dia!.

Evidência — [layout-detail.json](teste-evidencias/layout-detail.json), [capturas/layout-390-crates.png](teste-evidencias/capturas/layout-390-crates.png), [capturas/layout-1366-products.png](teste-evidencias/capturas/layout-1366-products.png), [capturas/layout-390-login.png](teste-evidencias/capturas/layout-390-login.png).

## 5. Melhorias por impacto operacional

- **Caixas e fila.** Mostrar a causa de cancelamento e o motivo devolvido pela impressora no próprio trabalho. Vincular impressora a depósito. No formato ZPL, revisar a transliteração de Melão Japonês para MELAO JAPONES com uma Zebra antes de mudar a codificação.
- **Requisições.** Acrescentar leitura por QR para marcar uma caixa da separação, preservando a seleção manual. Mostrar a quantidade e unidade também na recusa de itens em UN, sem citar a regra de 20% de KG. Expor encerramentos com diferença em consulta ou relatório, pois a evidência hoje fica no histórico da requisição.
- **Contagem.** Antes de concluir, indicar quais divergências ainda precisam de recontagem. Ao tentar ajustar lote de caixa, abrir a ação que trata os pesos por identificação. O operador hoje precisa interpretar uma resposta genérica.
- **Sessão e administração.** Informar que é necessário entrar novamente quando a edição de perfil revoga uma sessão. Usar o mesmo fluxo de senha temporária após redefinição administrativa.
- **Estoque e início.** Reaplicar busca e abaixo do mínimo após trocar o depósito. Padronizar a quantidade de falta no Início e em Estoque.
- **Relatórios.** Acrescentar consulta explícita de ajustes se a posição precisar ser explicada diretamente pela lista de movimentações. Os tipos comerciais existentes têm outra abrangência.
- **Catálogo e navegação.** Esconder ação indisponível. Permitir sem marca como opção de negócio, com rótulo explícito. Corrigir os tamanhos e indicação do item corrente no celular.

## 6. Riscos e limitações

- O Chrome usa dimensões emuladas. O scanner foi exercitado com um vídeo simulado, e a contagem de dez linhas foi preenchida no navegador. O teclado virtual, a câmera física, a Zebra, o ESP32, o retorno ~HS, a porta 9100 e OTA exigem validação em equipamento. A ausência de equipamento não impediu testar o protocolo da fila por curl.
- O teste de volume usa massa sintética local. Não mede muitos usuários em rede real, latência ao banco remoto nem impressão física. Os tempos das telas carregadas estão em [performance.json](teste-evidencias/performance.json).
- LAY-10 registra o ciclo completo de Tab das 26 aberturas. A amostra não cobre toda combinação de estados ou subdiálogos. Estilos computados e axe-core avaliaram as telas abertas, e as capturas incluem Contagem e Requisições preenchidas.
- Os grants foram feitos depois da captura dos perfis iniciais. O resultado desses fluxos depende das permissões acrescentadas à instância local e não representa a experiência inicial entregue pelo banco novo.
- Os testes de concorrência e de integridade cobrem os SKUs e depósitos gerados. A trilha de domínio, a matriz e os logs foram preservados com os passos e resultados de cada caso.
- O plano mantém 1.8 e 1.9 abertos. A verificação desta rodada usou o código existente, com handlers e SQL no mesmo pacote e app.js concentrando funções. Status físico antes do ACK e aspectos de rede da impressora ficaram sem ensaio em hardware.

## 7. Divergências com a documentação

- O contrato do relatório já restringe Entradas e Saídas aos respectivos tipos e exclui os ajustes. O sistema segue o documento. REL-05 e TRV-10 pedem uma visão adicional de AJUSTE para explicar o saldo.
- O plano de correção descreve como feito o rótulo Sem marca para dados legados. O catálogo recusa criar um SKU novo com brand_id=0. A expectativa de criar produto sem marca precisa ser distinguida da exibição histórica.
- A regra de 20% do contrato de requisições está implementada para KG em geral. Arroz comum passou com 110 separados de 106 pedidos e 108,7 entregues. UN foi recusado. O contrato também descreve corretamente o encerramento com diferença, que a interface mostra no histórico da requisição.
- O guia de estilo diz que Início e Estoque usam a mesma regra de situação. A renderização de Início usa Falta e mostra apenas a quantidade. A medida da barra inferior ficou abaixo da exceção de 12 px documentada.
- O plano de correção registra a correção de CSRF em duas abas, confirmada com criação de produto HTTP 201. O caso de sessão já encerrada ainda leva ao aviso de cadastro sem confirmação.
- O contrato de caixas prevê cancelamento com a causa Caixa vazia. O banco conserva a causa e a fila expõe só estado. O plano registra a falta de vínculo por depósito confirmada no pareamento e a transliteração observada no ZPL.

### Pontos conhecidos

- A tela de Requisições não oferece scanner para selecionar caixas. As opções de relatório permanecem Entradas, Saídas e Estoque na data. Encerramentos com diferença estão no histórico da requisição.
- Tentar entregar 3 kg com reserva de 2 kg retornou HTTP 409 “Entrega maior do que a quantidade separada.” A separação do peso real é necessária antes da entrega. [known-points.json](teste-evidencias/known-points.json)
- A fila ainda não associa impressoras a depósitos. O ZPL traz ^CI28 e translitera Melão Japonês para MELAO JAPONES. INV-21 documenta a contagem do peso total por produto e o erro no ajuste.

## Anexos

Roteiros Playwright `real-*.cjs`, evidências `*.json` e `*.jsonl`, capturas `capturas/`, registro do Compose `app.log` e saídas da suíte em `teste-evidencias/`. Os valores de senha e os tokens foram omitidos das evidências funcionais.

Os arquivos rastreados do repositório foram preservados. Os resultados e os roteiros ficaram em arquivos de teste locais.
