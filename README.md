# TotalFood — Gestão de Estoque

Sistema web de estoque e operação da TotalFood: entradas, saídas e transferências com lotes e custo médio, caixas de hortifruti com etiqueta Zebra, requisições internas, inventário, fichas técnicas e CMV, planejamento de cardápio, patrimônio e expedição. Substitui o AppSheet de controle de estoque.

## Tecnologias

- Go 1.24 (`net/http`, sem framework), PostgreSQL (`pgx`)
- HTML, CSS e JavaScript sem build, servidos pelo próprio binário
- Firmware ESP32-S3 para a impressora Zebra ZD220 (`firmware/etiqueta_zd220`)

## Módulos

| Tela | Rota | Permissão para abrir |
|---|---|---|
| Início | `/home` | `stock:read` |
| Estoque | `/stock` | `stock:read` |
| Entrada, Saída, Transferência | `/entry`, `/exit`, `/transfer` | `movement_entry`, `movement_exit`, `movement_transfer` (escrita) |
| Caixas de hortifruti | `/crates` | `movement_entry:write` |
| Requisições | `/material-requests` | `material_requests:read` |
| Catálogo e Produtos | `/catalog`, `/products` | `catalog:read` |
| Auditoria de estoque (inventário) | `/inventory` | `inventory_validation:read` |
| Relatórios | `/reports` | `operations:read` |
| Fichas técnicas e CMV | `/recipes` | `costs:read` |
| Planejamento de cardápio | `/planner` | `planning:read` |
| Importar NF | `/fiscal` | `fiscal_import:write` |
| Patrimônio | `/assets` | `assets:read` |
| Expedição, Locais e frota, Minha rota | `/dispatch`, `/logistics`, `/driver` | `dispatch`, `logistics`, `delivery` (leitura) |
| Administração (usuários, perfis, depósitos, impressoras) | `/admin` | permissões de administração |

A autorização é sempre verificada no servidor; o menu só esconde o que o perfil não pode usar.

## Documentação

| Documento | Conteúdo |
|---|---|
| [Contrato global de engenharia](docs/global-engineering-contract.md) | Regras que toda mudança segue: fonte única, mudança mínima, mensagens, idempotência, migrations, testes e promoção |
| [Guia de estilo do frontend](docs/guia-de-estilo-frontend.md) | Padrões visuais e de interação das telas |
| [Relatórios de movimentação](docs/movement-reports-contract.md) | Contrato dos relatórios de entradas, saídas e saldo |
| [Ficha técnica e CMV](docs/ficha-tecnica-cmv-contract.md) | Contrato de custo por ficha |
| [Planner de cardápio](docs/planner-cardapio-contract.md) | Contrato do planejamento, solver e previsto x realizado |
| [Requisições de materiais](docs/requisicoes-materiais.md) | Fluxo, perfis e endpoints das requisições |
| [Caixas e etiquetas](docs/etiquetas-caixas-fila-manual.md) | Caixas, fila de impressão, ESP32 e Zebra |
| [Deploy](docs/deploy-render-neon.md) | Produção no Render com Neon: variáveis, migrations e conferência do banco |
| [Plano de correção](docs/plano-de-correcao.md) | Situação dos itens da revisão de arquitetura |

## Configuração

Variáveis de ambiente lidas pelo servidor:

| Variável | Padrão | Uso |
|---|---|---|
| `DATABASE_URL` | obrigatória | DSN do PostgreSQL. Ex.: `postgres://usuario:senha@127.0.0.1:5432/estoque?sslmode=disable` |
| `HTTP_ADDR` | `:$PORT` ou `:8080` | Endereço HTTP. |
| `PORT` | — | Porta fornecida pela hospedagem; usada quando `HTTP_ADDR` não está definido. |
| `MIGRATIONS_DIR` | `migrations` | Pasta das migrations. |
| `RUN_MIGRATIONS` | `true` | Aplica as migrations ao iniciar. |
| `COOKIE_SECURE` | `true` | Use `false` só em desenvolvimento HTTP local. |
| `CLIENT_IP_HEADER` | — | Cabeçalho com o IP do cliente escrito por um proxy confiável, para o limite de login. **No Render, `True-Client-IP`.** Só defina se toda requisição passar por esse proxy. |
| `TRUSTED_PROXY_HOPS` | `0` | Proxies confiáveis à frente da aplicação; com 1 ou mais, o IP vem do `X-Forwarded-For` contando da direita. Vale quando `CLIENT_IP_HEADER` não está definido ou falta. |
| `OSRM_URL` | — | Serviço OSRM para ordenar paradas das rotas. Sem ele, a rota usa distância em linha reta e a tela avisa. |
| `FISCAL_ALLOWED_HOSTS` | — | Hosts permitidos para consulta de NF por QR Code. |
| `DB_MAX_OPEN_CONNS`, `DB_MAX_IDLE_CONNS` | `10`, `5` | Pool de conexões. |
| `DB_CONN_MAX_LIFETIME_SECONDS`, `DB_CONN_MAX_IDLE_SECONDS` | `1800`, `300` | Tempo de vida das conexões. |
| `BOOTSTRAP_ADMIN_ENABLED` | `false` | `true` cria ou redefine o usuário `diretor` ao iniciar. |
| `BOOTSTRAP_ADMIN_USER` | — | Obrigatório com o bootstrap ligado; só aceita `diretor` (o Compose já define). |
| `BOOTSTRAP_ADMIN_PASSWORD` | — | Senha temporária do Diretor; obrigatória com o bootstrap ligado. |

O login aceita até 10 senhas erradas por usuário e endereço, e até 50 erros por endereço em qualquer usuário, numa janela de 15 minutos. Logins corretos não contam. O controle fica em memória: com várias instâncias, cada uma conta separado.

Credenciais ficam só no ambiente. Não exponha o PostgreSQL à internet; publique apenas a aplicação HTTP.

## Executar

```sh
export DATABASE_URL='postgres://usuario:senha@127.0.0.1:5432/estoque?sslmode=disable'
go run ./cmd/server
curl -i http://127.0.0.1:8080/health
```

### Com Docker Compose

```sh
APP_PORT=5656 docker compose up --build
```

Variáveis do Compose: `APP_PORT` (padrão `5656`), `POSTGRES_DB`, `POSTGRES_USER`, `POSTGRES_PASSWORD` (padrão local `estoque_dev_change_me`), `BOOTSTRAP_ADMIN_ENABLED` e `BOOTSTRAP_ADMIN_PASSWORD`.

### Primeiro acesso em banco vazio

Sem bootstrap, um banco vazio não tem nenhum usuário. Para criar o Diretor:

```sh
BOOTSTRAP_ADMIN_ENABLED=true BOOTSTRAP_ADMIN_PASSWORD='senha-temporaria' docker compose up --build
```

Entre com `diretor` e a senha temporária; o sistema exige a troca de senha no primeiro acesso. Depois, reinicie **sem** `BOOTSTRAP_ADMIN_ENABLED=true`: enquanto ligado, cada início redefine a senha do Diretor e encerra as sessões dele.

## Testes

Os testes usam um PostgreSQL real; cada teste cria e apaga o próprio schema.

```sh
export TEST_DATABASE_URL='postgres://usuario:senha@127.0.0.1:5432/estoque_test?sslmode=disable'
./check.sh            # go vet + go test; GO=/caminho/do/go para outro binário
```

Além do domínio (estoque, custo, concorrência, idempotência, permissões), a suíte verifica:

- formatação `gofmt` de todo o pacote;
- números de migration únicos (o `033` duplicado é exceção registrada);
- que toda rota citada em `docs/` e neste README existe no servidor;
- que todo nome de constraint usado para traduzir erros do banco existe no esquema.

Interface: `scripts/crates-playwright-e2e.cjs` testa a tela de Caixas no Chrome a 390 e 1440 px, e `scripts/material-requests-playwright-e2e.cjs` testa a tela de Requisições a 390 px. Firmware: `python3 -m unittest discover -s firmware/tests`.

## CI e promoção

| Workflow | Quando roda | O que faz |
|---|---|---|
| `warehouse-feature-ci` | push em `feat/**`, `feature/**`, `fix/**` | `go vet`, testes com `-race` em PostgreSQL 17, build, Docker, `node --check` |
| `crates-ui-ci` | mesmas branches, quando a tela de Caixas muda | Playwright da tela de Caixas |
| `material-requests-ui-ci` | mesmas branches, quando a tela de Requisições muda | Playwright da tela de Requisições |
| `firmware-ci` | mesmas branches, quando o firmware muda | compila o firmware ESP32 e publica o binário |
| `production-gate` | push em `main` e `hotfix/**` | mesmo conjunto do `warehouse-feature-ci` no commit de produção |

Toda mudança entra por branch e PR; o merge para `main` exige CI verde no SHA exato e aprovação explícita (ver o contrato global, seção 10).
