# Controle de Estoque — Fundação técnica

Fundação mínima para substituir o AppSheet de controle de estoque.

## Tecnologias

- Go
- PostgreSQL
- `net/http`

## Configuração

Variáveis de ambiente:

- `DATABASE_URL` — DSN do PostgreSQL. Obrigatória. Exemplo local: `postgres://usuario:senha@127.0.0.1:5432/estoque?sslmode=disable`
- `HTTP_ADDR` — endereço do servidor HTTP. Opcional, padrão `:8080`.
- `MIGRATIONS_DIR` — diretório de migrations. Opcional, padrão `migrations`.
- `OSRM_URL` — endereço do serviço OSRM usado para ordenar as paradas das rotas, como `http://osrm:5000`. Opcional. Sem ele, ou com o OSRM fora do ar, a rota usa uma estimativa pela distância em linha reta e a tela avisa.
- `CLIENT_IP_HEADER` — cabeçalho, escrito por um proxy confiável, com o IP do cliente para o limite de login. Opcional. **No Render, use `True-Client-IP`**: o Render fica atrás da Cloudflare, que define esse cabeçalho, e o próprio Render não limpa o `X-Forwarded-For` enviado pelo cliente. Só defina se toda requisição passar por esse proxy; se o cabeçalho faltar ou for inválido, vale `TRUSTED_PROXY_HOPS`.
- `TRUSTED_PROXY_HOPS` — quantos proxies confiáveis ficam na frente da aplicação. Opcional, padrão `0` (usa o endereço da conexão). Com `1` ou mais, o IP do cliente para o limite de tentativas de login vem do `X-Forwarded-For`, contando da direita. Só aumente se houver de fato esse número de proxies, senão o cliente consegue forjar o endereço.

O login aceita até 10 senhas erradas por usuário e endereço, e até 50 erros por endereço em qualquer usuário, numa janela de 15 minutos. Logins corretos não contam. O controle fica em memória: com várias instâncias, cada uma conta separado.

Credenciais devem ficar somente no ambiente. Não exponha PostgreSQL diretamente à internet; exponha apenas a aplicação HTTP atrás da rede/firewall apropriado.

## Executar

```sh
export DATABASE_URL='postgres://usuario:senha@127.0.0.1:5432/estoque?sslmode=disable'
/opt/data/go/go/bin/go run ./cmd/server
```

Health check:

```sh
curl -i http://127.0.0.1:8080/health
```

## Executar com Docker Compose

O Compose publica a aplicação na porta `5656` por padrão e permite alterar a porta pelo ambiente:

```sh
APP_PORT=5656 docker compose up --build
```

Para usar outra porta no host:

```sh
APP_PORT=8088 docker compose up --build
```

Variáveis principais do Compose:

- `APP_PORT` — porta publicada no host, padrão `5656`.
- `POSTGRES_DB` — banco PostgreSQL, padrão `estoque`.
- `POSTGRES_USER` — usuário PostgreSQL, padrão `estoque`.
- `POSTGRES_PASSWORD` — senha PostgreSQL, padrão local `estoque_dev_change_me`.
- `BOOTSTRAP_ADMIN_ENABLED` — `true` cria ou redefine o usuário `diretor` ao iniciar. Padrão `false`.
- `BOOTSTRAP_ADMIN_PASSWORD` — senha inicial do Diretor. Obrigatória quando `BOOTSTRAP_ADMIN_ENABLED=true`; sem padrão.

### Primeiro acesso em banco vazio

Sem bootstrap, um banco vazio não tem nenhum usuário. Para criar o Diretor:

```sh
BOOTSTRAP_ADMIN_ENABLED=true BOOTSTRAP_ADMIN_PASSWORD='senha-temporaria' docker compose up --build
```

Entre com `diretor` e a senha temporária; o sistema exige a troca de senha no primeiro acesso. Depois, reinicie **sem** `BOOTSTRAP_ADMIN_ENABLED=true`: enquanto ligado, cada início redefine a senha do Diretor para a temporária e encerra as sessões dele.

Em produção, defina senhas próprias por variável de ambiente ou arquivo `.env` local não versionado.

## Executar o check/teste

O check de integração usa um PostgreSQL real e não cria infraestrutura adicional.

```sh
export TEST_DATABASE_URL='postgres://usuario:senha@127.0.0.1:5432/estoque_test?sslmode=disable'
./check.sh
```

Ele verifica:

- criação/configuração do servidor HTTP;
- conexão com PostgreSQL;
- execução e registro de migrations SQL versionadas;
- `GET /health` com sucesso quando o DB está disponível;
- `GET /health` sem sucesso quando o DB está indisponível.
