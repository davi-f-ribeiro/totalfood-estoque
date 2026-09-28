# Deploy: PostgreSQL atual -> Neon + Cloud Run

## Configuração da aplicação

Variáveis de ambiente usadas pela aplicação:

- `DATABASE_URL` — connection string PostgreSQL/Neon. Em Neon deve usar SSL (`sslmode=require` ou equivalente fornecido pelo Neon).
- `PORT` — porta fornecida pelo Cloud Run. A aplicação escuta em `:$PORT` quando `HTTP_ADDR` não está definido.
- `HTTP_ADDR` — override opcional local; em Cloud Run não usar, salvo necessidade explícita.
- `MIGRATIONS_DIR` — diretório de migrations dentro da imagem (`/app/migrations`).
- `RUN_MIGRATIONS` — padrão `true` para desenvolvimento/local. Em Cloud Run público, usar `false` depois que a etapa explícita de migration tiver sido executada.
- `COOKIE_SECURE` — padrão seguro; usar `false` somente em desenvolvimento HTTP local.
- `DB_MAX_OPEN_CONNS` — padrão `10`.
- `DB_MAX_IDLE_CONNS` — padrão `5`.
- `DB_CONN_MAX_LIFETIME_SECONDS` — padrão `1800`.
- `DB_CONN_MAX_IDLE_SECONDS` — padrão `300`.
- `BOOTSTRAP_DIRECTOR_LOGIN` / `BOOTSTRAP_DIRECTOR_PASSWORD` — não usar no deploy da base já preparada; servem apenas para bootstrap de banco vazio.
- `FISCAL_ALLOWED_HOSTS` — opcional, legado do módulo fiscal preservado.

## Migrations

A aplicação possui runner interno que aplica `migrations/*.sql` em ordem e registra `schema_migrations`.
Para produção/Neon, executar migrations como etapa explícita antes de trocar tráfego ou iniciar revisão final:

```bash
# exemplo local com DSNs sem valores reais no arquivo
export DATABASE_URL='<NEON_DATABASE_URL>'
export MIGRATIONS_DIR=/app/migrations
/app/estoque
```

Para evitar startup concorrente aplicando migrations durante escala horizontal, preferir um job/revision única de migração antes do serviço público, ou aplicar via `psql` em ordem usando o mesmo diretório `migrations/`.

A migration `011_password_change_required.sql` é obrigatória antes da aplicação depender de `users.must_change_password`.

## Migração PostgreSQL atual -> Neon

Usar dump em formato custom e restore no Neon:

```bash
export SOURCE_DATABASE_URL='<POSTGRES_ORIGEM>'
export NEON_DATABASE_URL='<NEON_DESTINO>'

pg_dump --format=custom --no-owner --no-acl --file=estoque.dump "$SOURCE_DATABASE_URL"
pg_restore --clean --if-exists --no-owner --no-acl --dbname="$NEON_DATABASE_URL" estoque.dump
```

Preservar a base atual, incluindo usuários desativados e usuário `diretor` com troca obrigatória. Não executar novo reset após restore.

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

## Cloud Run proposta inicial

Valores iniciais conservadores, ajustar após conhecer plano Neon:

- Região: escolher após confirmar regiões Neon disponíveis; preferência por menor latência Brasil/Neon. Candidatas usuais: `southamerica-east1` se Neon equivalente estiver disponível; caso contrário `us-east1`/`us-east4`.
- CPU: 1 vCPU.
- Memória: 512Mi.
- Concurrency: 20.
- Min instances: 0.
- Max instances: 2 inicialmente.
- Timeout: 30s.
- Ingress: all, se o serviço deve ser acessível pela internet.
- Autenticação Cloud Run: permitir não autenticado no Cloud Run; a aplicação mantém login próprio.
- Service account: dedicada ao serviço, com acesso apenas aos secrets necessários.

## Secret Manager

Secrets esperados por nome, sem valores no Git:

- `estoque-database-url`
- `estoque-run-migrations` (valor recomendado no serviço público: `false`)

Se futuramente houver segredo de sessão separado, criar secret específico antes de usar. Hoje as sessões usam tokens randômicos server-side em PostgreSQL e cookie HttpOnly.
