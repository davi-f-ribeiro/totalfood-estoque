# Deploy: Render + Neon

## Produção

- A aplicação de produção roda no Render (`totalfood-estoque.onrender.com`), com o banco no Neon.
- As migrations são aplicadas pela própria aplicação ao iniciar (`RUN_MIGRATIONS` no padrão `true`): o `schema_migrations` de produção registra cada migration no horário da implantação correspondente.
- Instância gratuita: o Render desliga a instância por inatividade e a primeira requisição depois disso pode demorar 50 segundos ou mais. O firmware do ESP32 faz um `GET /health` antes de reservar etiquetas por esse motivo.
- O Render fica atrás da Cloudflare e não limpa o `X-Forwarded-For` enviado pelo cliente. Para o limite de login usar o IP real, defina `CLIENT_IP_HEADER=True-Client-IP`.
- O certificado de `*.onrender.com` alterna entre Let's Encrypt e Google Trust Services. Quem acessa por HTTPS com lista própria de raízes (o firmware, em `wms_ca.h`) precisa aceitar as duas cadeias.

## Configuração da aplicação

Variáveis de ambiente usadas pela aplicação:

- `DATABASE_URL` — connection string PostgreSQL/Neon. Em Neon deve usar SSL (`sslmode=require` ou equivalente fornecido pelo Neon).
- `PORT` — porta fornecida pelo Render. A aplicação escuta em `:$PORT` quando `HTTP_ADDR` não está definido.
- `HTTP_ADDR` — override opcional para uso local; no Render, não definir.
- `MIGRATIONS_DIR` — diretório de migrations dentro da imagem (`/app/migrations`).
- `RUN_MIGRATIONS` — padrão `true`: a aplicação aplica as migrations ao iniciar. É o modo usado no Render. Use `false` só se as migrations forem aplicadas por uma etapa separada.
- `COOKIE_SECURE` — padrão seguro; usar `false` somente em desenvolvimento HTTP local.
- `CLIENT_IP_HEADER` — cabeçalho com o IP do cliente escrito pelo proxy da hospedagem (no Render, `True-Client-IP`). Opcional; tem prioridade sobre `TRUSTED_PROXY_HOPS`. Só usar se toda requisição passar por esse proxy.
- `TRUSTED_PROXY_HOPS` — número de proxies confiáveis na frente da aplicação, para o limite de login usar o IP real do cliente via `X-Forwarded-For`. Padrão `0`. Confirmar quantos saltos a hospedagem adiciona antes de definir; um valor alto demais deixa o cliente forjar o endereço.
- `DB_MAX_OPEN_CONNS` — padrão `10`.
- `DB_MAX_IDLE_CONNS` — padrão `5`.
- `DB_CONN_MAX_LIFETIME_SECONDS` — padrão `1800`.
- `DB_CONN_MAX_IDLE_SECONDS` — padrão `300`.
- `BOOTSTRAP_ADMIN_ENABLED` / `BOOTSTRAP_ADMIN_USER` (sempre `diretor`) / `BOOTSTRAP_ADMIN_PASSWORD` — não usar no deploy da base já preparada; servem apenas para bootstrap de banco vazio. Ligado, cada início redefine a senha do Diretor e encerra as sessões dele.
- `FISCAL_ALLOWED_HOSTS` — opcional, legado do módulo fiscal preservado.

## Migrations

A aplicação possui runner interno que aplica `migrations/*.sql` em ordem e registra `schema_migrations`.
No Render, a aplicação aplica as migrations pendentes ao iniciar, antes de aceitar requisições. Para conferir uma migration contra uma cópia do banco (por exemplo, um branch do Neon), rode o binário apontando para a cópia; ele aplica as pendentes, sobe o servidor e pode ser encerrado em seguida:

```bash
# exemplo com DSN sem valor real no arquivo
export DATABASE_URL='<NEON_BRANCH_DATABASE_URL>'
export MIGRATIONS_DIR=/app/migrations
/app/estoque
```

Com mais de uma instância, prefira aplicar as migrations antes da implantação (com `RUN_MIGRATIONS=false` no serviço), para duas instâncias não as aplicarem ao mesmo tempo.

A migration `011_password_change_required.sql` é obrigatória antes da aplicação depender de `users.must_change_password`.

## Referência: migração do PostgreSQL anterior para o Neon (já realizada)

Usar dump em formato custom e restore no Neon:

```bash
export SOURCE_DATABASE_URL='<POSTGRES_ORIGEM>'
export NEON_DATABASE_URL='<NEON_DESTINO>'

pg_dump --format=custom --no-owner --no-acl --file=estoque.dump "$SOURCE_DATABASE_URL"
pg_restore --clean --if-exists --no-owner --no-acl --dbname="$NEON_DATABASE_URL" estoque.dump
```

Preservar a base atual, incluindo usuários desativados e usuário `diretor` com troca obrigatória. Não redefinir usuários nem senhas depois do restore.

## Validação origem x Neon

Comparar contagens e invariantes:

```sql
SELECT 'inventory_items', count(*) FROM inventory_items
UNION ALL SELECT 'product_skus', count(*) FROM product_skus
UNION ALL SELECT 'warehouses', count(*) FROM warehouses
UNION ALL SELECT 'movements', count(*) FROM movements
UNION ALL SELECT 'movement_lines', count(*) FROM movement_lines
UNION ALL SELECT 'assets', count(*) FROM assets
UNION ALL SELECT 'asset_types', count(*) FROM asset_types
UNION ALL SELECT 'asset_events', count(*) FROM asset_events
UNION ALL SELECT 'users', count(*) FROM users
UNION ALL SELECT 'profiles', count(*) FROM profiles
UNION ALL SELECT 'permissions', count(*) FROM permissions
UNION ALL SELECT 'profile_permissions', count(*) FROM profile_permissions;
```

Saldo por SKU/local:

```sql
SELECT sku_id, warehouse_id,
       sum(CASE WHEN direction='IN' THEN quantity ELSE -quantity END) AS quantity
FROM movement_lines
GROUP BY sku_id, warehouse_id
ORDER BY sku_id, warehouse_id;
```

Saldo geral:

```sql
SELECT sku_id,
       sum(CASE WHEN direction='IN' THEN quantity ELSE -quantity END) AS quantity
FROM movement_lines
GROUP BY sku_id
ORDER BY sku_id;
```

Referências históricas:

```sql
SELECT count(*) FROM movements m LEFT JOIN users u ON u.id=m.user_id WHERE m.user_id IS NOT NULL AND u.id IS NULL;
SELECT count(*) FROM asset_events e LEFT JOIN users u ON u.id=e.user_id WHERE u.id IS NULL;
SELECT count(DISTINCT operation_id), count(*) FROM movements;
```
