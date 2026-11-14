# Caixas de hortifruti + fila manual de impressão (ESP32-S3/Zebra ZD220)

## Escopo

- O recebimento em **/crates** aproveita o SKU existente de unidade base KG, o movimento ENTRADA, os lotes do WMS e os depósitos; não cria estoque paralelo.
- Cada caixa recebe um ID imutável `CX-000001` e uma posição X/Y no recebimento. Peso líquido inicial e atual são armazenados com três casas decimais.
- NF e fornecedor são vinculados ao movimento. O lote interno é o ID original de `stock_lots`; o lote de origem informado pelo vendedor é armazenado separadamente e pode ficar ausente. O ID interno não prova por si só a rastreabilidade de origem.
- A tela **/crates** lê o QR Code via câmera usando o scanner já disponível no WMS (com fallback manual).
- A pesagem de retorno com peso inferior realiza SAÍDA vinculada ao lote **específico** da caixa. Aumento de peso exige nova entrada/correção auditável, não está permitido no fluxo de retorno.
- A transferência da caixa realiza TRANSFERÊNCIA real entre depósitos, conserva o ID e o lote de recebimento, porém aponta a caixa para um lote de estoque no depósito de destino. O consumo posterior considera esse lote.
- Saídas e transferências genéricas não podem alocar um lote atualmente associado a caixas rastreadas; use a operação individual.
- A fila de impressão é persistente no Postgres; nenhuma chamada automática sai do ESP para o Render. Trabalhos antigos ainda pendentes são cancelados ao gerar nova revisão.

## Pareamento (uma vez)

1. No WMS, com permissão de administração de depósitos, acesse **Caixas de hortifruti → Parear impressora**, informe nome e copie o token exibido **uma única vez**.
2. No ESP, acesse `http://etiqueta.local/config` como administrador e informe URL HTTPS pública do WMS e token. Salve.
3. No WMS, receba caixas informando pesos individualizados. A entrada gera automaticamente jobs e as etiquetas 60×60 mm.
4. Na tela inicial do ESP, toque **Buscar e imprimir etiquetas**. A busca acontece **apenas ao tocar**.
5. Confira fila e status no WMS. Em caso de falta de ACK ou reinício, não há reimpressão automática: o trabalho fica incerto e requer decisão do operador.

O token é específico de dispositivo, armazenado como hash no servidor e não volta nas consultas de configuração do ESP. O ESP grava o segredo local em Preferences/NVS (proteja acesso físico e admin da rede local).

## API

| Ação | Método e rota | Proteção |
|---|---|---|
| Entrada identificada | `POST /api/crates/receive` | sessão, CSRF, movimento entrada |
| Consulta caixa | `GET /api/crates/{id}` | sessão, estoque leitura |
| Pesagem retorno | `POST /api/crates/{id}/weigh` | sessão, CSRF, movimento saída |
| Transferência caixa | `POST /api/crates/{id}/transfer` | sessão, CSRF, movimento transferência |
| Parear impressora | `POST /api/print/devices` | sessão, CSRF, admin depósitos |
| Consultar impressoras | `GET /api/print/devices` | sessão, admin depósitos |
| Revogar impressora | `POST /api/print/devices/{id}/revoke` | sessão, CSRF, admin depósitos |
| Consultar fila | `GET /api/print/jobs` | sessão, movimento entrada |
| Reenviar falha/resultado incerto | `POST /api/print/jobs/{id}/retry` | sessão, CSRF, movimento entrada |
| Reservar trabalho | `POST /api/print/jobs/claim?format=zpl` | Bearer do ESP |
| Confirmar envio USB | `POST /api/print/jobs/{id}/ack` | Bearer do ESP + claim token |
| Informar falha USB | `POST /api/print/jobs/{id}/fail` | Bearer do ESP + claim token |

Reserve sempre com transação e `FOR UPDATE SKIP LOCKED`, ACK é idempotente. Lease expirado fica `UNCERTAIN`, sem reimpressão automática.

## Testes / aprovação

- `go test ./...`
- `TEST_DATABASE_URL=postgres://... go test ./cmd/server -run TestCratesReceivingWeighingAndPrintQueue -v`
- `TEST_DATABASE_URL=postgres://... go test -race ./...`
- `node --check cmd/server/static/crates.js`
- Validar tela a 390 px e fluxo real com impressora Zebra ZD220, ESP32-S3 N16R8, USB, OTA, ausência de papel/USB, falha HTTPS e render com início frio.

**Firmware:** pasta `firmware/etiqueta_zd220` deriva da versão 1.5.1 salva no Drive; versão alterada para 1.6.0. Compilar com Arduino ESP32 core **3.0.5**, placa ESP32S3 Dev Module, Flash 16M, OPI PSRAM, USB Hardware CDC/JTAG, configuração OTA compatível. Não assumir que a versão 3.3.x funcione para USB da Zebra. O certificado usado pelo Render deve encadear em uma das CAs embutidas em `wms_ca.h`; verificar antes da instalação.

**Limites conhecidos:** "enviado" significa que o driver USB aceitou/transmitiu o trabalho; sem confirmação física de etiqueta emitida. Não é uma garantia de exatamente-uma-impressão física. O operador deve resolver estado `UNCERTAIN` antes de reimprimir.

**Sem promoção automática:** revisão do diff, gates e teste com hardware antes de autorizar o merge.
