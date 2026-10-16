# Integração Rastro Connect — frota e telemetria

## Escopo

Integra o cadastro **existente** de veículos do TotalFood (Go + PostgreSQL) com os identificadores e posições do Rastro Connect, sem substituir a gestão de frota, alterar movimentos de estoque ou executar comandos remotos.

1. O diretor acessa **Administração → Rastreamento da frota**, informa empresa, usuário e senha e salva.
2. Em **Sincronizar frota**, o servidor autentica no Rastro Connect e consulta o endpoint somente de leitura empregado no painel.
3. Os veículos são reconciliados por placa normalizada (`ABC1D23`), sem alteração de capacidade, titularidade, refrigeração ou descrição dos já cadastrados.
4. Um veículo desconhecido é registrado no cadastro existente **inativo e sem capacidade**. A capacidade real deve ser informada em **Locais e frota** antes da ativação. Não inventar quilogramas.
5. A última posição retornada é vinculada ao ID do veículo, junto do horário informado pelo provedor e do horário da consulta ao servidor. A telemetria não é considerada uma confirmação de GPS atual ou de localização segura.

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
- Se a API retornar veículos sem placa válida ou identificador, não são importados; não adivinhar dados.

## Validação

O teste de integração do provedor usa um servidor HTTP de simulação e verifica sessão, token e leitura exclusiva. Os testes de PostgreSQL 17 verificam importação repetida, preservação dos veículos existentes, bloqueio de ativação sem capacidade, ausência de duplicação e restrições de acesso.

**Pendente de aceite operacional:** executar uma sincronização autenticada com a frota real no ambiente TotalFood e confrontar quantidade de placas, IDs e horários com o portal Rastro Connect. Nenhum dispositivo deve receber comando durante o teste.
