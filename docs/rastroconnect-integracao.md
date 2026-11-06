# Integração Rastro Connect — frota e telemetria

## Escopo

Integra o cadastro **existente** de veículos do TotalFood (Go + PostgreSQL) com os identificadores e posições do Rastro Connect, sem substituir a gestão de frota, alterar movimentos de estoque ou executar comandos remotos.

1. O diretor acessa **Administração → Rastreamento da frota**, informa empresa, usuário e senha e salva.
2. Em **Sincronizar frota**, o servidor autentica no Rastro Connect e consulta o endpoint somente de leitura empregado no painel.
3. Os veículos são reconciliados por placa normalizada (`ABC1D23`), sem alteração de capacidade, titularidade, refrigeração ou descrição dos já cadastrados.
4. Um veículo desconhecido é registrado no cadastro existente **inativo e sem capacidade**. A capacidade real deve ser informada em **Locais e frota** antes da ativação. Não inventar quilogramas.
5. A última posição retornada é vinculada ao ID do veículo, junto de **dois horários distintos**: o do rastreador (texto enviado pelo provedor, quando disponível) e o da sincronização no TotalFood. O horário de sincronização **não prova posição GPS recente**; sem fuso/relógio validado do equipamento não é feita inferência de atualidade nem autorização de saída.

## Configuração no servidor

Uma única variável adicional é obrigatória para criptografar a senha de terceiros:

`RASTROCONNECT_KEY`: segredo de **32 bytes**, codificado em **64 caracteres hexadecimais**. Exemplo de geração local: `openssl rand -hex 32`.

Insira o valor como segredo no ambiente do servidor ou Coolify. **Não** inclua o valor no Git, nos logs ou em arquivos públicos. Faça backup seguro do valor: se mudar essa chave, será necessário informar novamente a senha do rastreador.

A senha é cifrada com AES-256-GCM e nonce aleatório por alteração; nunca é exibida por GET. As rotas de configuração e importação usam o RBAC existente (`users/write`), sessão e proteção CSRF. A consulta à telemetria exige `logistics/read`.

## Integração consultiva

- Login: `GET/POST /acl/login/?next=/` (sessão e token do painel).
- Consulta: `POST /home/api/monitoramento/` com autenticação por token, exclusivamente para ler posições e frota.
- **Não** utiliza `POST /api/comando/` ou operações `block`/`unblock`.
- Origem HTTPS é fixa no backend; a UI não aceita URLs arbitrárias.
- Sincronização é **manual nesta versão**; ainda não habilita geofence ou liberação de Cargo Plan.
- A interface HTTP foi identificada em código público do painel, mas não constitui contrato oficial de API. Mudanças no portal podem exigir ajuste do adaptador; confirmar com o fornecedor se há endpoint oficial/token de integração dedicado.
- Registros sem placa válida ou identificador remoto são ignorados com contagem e amostra dos motivos apresentados na Administração. Colisões de placa ↔ identificador suspendem a transação inteira com diagnóstico específico (HTTP 409); nenhum vínculo é substituído automaticamente.

## Validação

O teste de integração do provedor usa um servidor HTTP de simulação e verifica sessão, token e leitura exclusiva. Os testes de PostgreSQL 17 verificam importação repetida, preservação dos veículos existentes, bloqueio de ativação sem capacidade, ausência de duplicação e restrições de acesso.

**Pendente de aceite operacional:** executar uma sincronização autenticada com a frota real no ambiente TotalFood e confrontar quantidade de placas, IDs e horários com o portal Rastro Connect. Nenhum dispositivo deve receber comando durante o teste.

## Geofencing de expedição — worker independente

- Branch de implantação: `feat/geofencing-operacional`. A integração anterior foi reconciliada com a `main` sem substituir seu GPS de patrimônios.
- O campo **Horário-limite de saída** é configurado em cada rota na Expedição, enquanto estiver em rascunho; não é a estimativa de partida usada pelo OSRM.
- O worker é o **mesmo binário Go** do servidor com `GEOFENCE_WORKER=true`, mas **não abre porta HTTP** e não executa bootstrap de usuários. Inicia sem criar um segundo aplicativo.
- Cronjob e GitHub Runner não são usados para monitoramento. O Docker reinicia o serviço automaticamente. `pg_try_advisory_lock` impede duas instâncias ativas de coletarem ao mesmo tempo.
- O worker consulta o provedor inicialmente a cada `GEOFENCE_POLL_SECONDS=60` (mínimo 30, máximo 600). Esta frequência **não garante** que o provedor produza uma posição nova a cada consulta.
- `RASTROCONNECT_KEY` permanece obrigatório para decifrar as credenciais guardadas na Administração. O worker só consulta a frota se as credenciais já estiverem configuradas.
- `RASTROCONNECT_DEVICE_TZ` é opcional para timestamps com fuso explícito, mas **necessário para timestamps sem fuso**, como `2026-10-16 08:21:00`. Defina-o **somente após verificar** o fuso real informado pelo rastreador (por exemplo `America/Fortaleza`). Sem data GPS interpretável, a posição pode ser consultada, mas **não confirma saída ou chegada**.
- Somente timestamps GPS recentes (até 5 minutos, tolerância de 2 minutos para relógio adiantado) alimentam o geofencing. `received_at` e `gps_at` são distintos.
- A saída exige evidência inicial do veículo dentro do perímetro do depósito, seguida de duas leituras GPS distintas, com horários crescentes e margem de 20 m, fora dele. O estado da checagem é persistido; sem origem observada, a saída fica **não confirmada**.
- A chegada exige duas leituras com horários GPS distintos dentro do raio do cliente (margem interna de 15 m). São gravados eventos separados de GPS, sem forçar transição no fluxo manual de comprovação do motorista.
- Raio configurável por local de 30 a 1000 m (padrões 150 m depósito, 100 m cliente), sem PostGIS.
- O monitor da Expedição mostra prazo, saída real, última posição confirmável e chegadas por cliente. Quando o GPS fica antigo, mostra **GPS indisponível**, nunca um registro inventado.
- Histórico: `geofence_positions` (pontos GPS), `geofence_progress` (cursor e confirmações), `geofence_events` (eventos gravados uma única vez com evidência do ponto, centro e raio vigentes), `routes.actual_departure_at`, `route_stops.arrived_at`.
- A coleta não executa comandos de bloqueio/desbloqueio físico, e não promove automaticamente entrega concluída. A prova de entrega permanece independente.

### Implantação no Docker / futura VPS

Configure a chave e, se aplicável, o fuso do **dispositivo**, no ambiente protegido do Docker/Coolify. Execute `docker compose --profile geofencing up -d --build`. **Não execute contra a produção até validar o endpoint e os timestamps com o provedor**.

O worker não tem porta publicada e usa a mesma `DATABASE_URL` e as mesmas migrations do servidor. Na futura VPS, basta transferir a imagem/serviços e os segredos; nenhum runner do GitHub é necessário para manter a coleta ativa.

### Aceite operacional indispensável

1. Salvar credenciais e sincronizar placas contra o portal RastroConnect; confrontar a posição e o timestamp de pelo menos dois veículos.
2. Testar em trajeto real uma saída e duas chegadas; confrontar os timestamps no painel e nos eventos.
3. Simular GPS antigo, reinício do worker e consulta duplicada; provar que não há evento duplicado nem saída/chegada inventada.
4. Validar o horário-limite configurado antes da liberação da rota e a exibição de atraso.
