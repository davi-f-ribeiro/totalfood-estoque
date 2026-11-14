#include "usb_printer.h"
#include <Update.h>
#include "esp_ota_ops.h"
#include "usb/usb_host.h"
#include "freertos/stream_buffer.h"

#define CHUNK_SIZE   1024
#define TXQ_DEPTH    32                // 32 KB de buffer de impressão
#define IN_SIZE      512
#define MAX_RETRIES  3                 // tentativas por pedaço antes de descartar
#define IN_WINDOW_MS 1500              // tempo máximo esperando resposta da impressora
#define IN_IDLE_MS   300               // fim da resposta: sem dados novos por este tempo

struct Chunk {
  uint16_t len;
  uint16_t job;                        // pedaços do mesmo trabalho têm o mesmo número
  bool     expectResponse;             // último pedaço de uma consulta (~HS etc.)
  uint8_t  data[CHUNK_SIZE];
};

static QueueHandle_t        txQueue;   // aplicação -> USB
static StreamBufferHandle_t rxStream;  // USB -> aplicação

static volatile bool     printerReady = false;
static volatile uint32_t bytesPrinted = 0;
static volatile uint32_t usbErrors    = 0;
static volatile uint16_t vid = 0, pid = 0;
static volatile uint32_t connections  = 0;   // quantas vezes a impressora ficou pronta
static uint16_t lastJob = 0;                 // último número de trabalho (lado da aplicação)
static uint16_t dropJob = 0;                 // trabalho com pedaço perdido: o resto é descartado
static bool     rejectedDev = false;         // há um dispositivo USB que não é impressora

static usb_host_client_handle_t client = nullptr;
static usb_device_handle_t      dev    = nullptr;
static volatile uint8_t newDevAddr = 0;
static volatile bool    devGone    = false;

static uint8_t itfNum = 0xFF, epOut = 0, epIn = 0;
static usb_transfer_t *outXfer = nullptr, *inXfer = nullptr;

// Máquina de estados: nunca há leitura e escrita pendentes ao mesmo tempo.
//   IDLE -> OUT (envia um pedaço) -> IDLE
//                                 -> IN (se o pedaço era uma consulta) -> IDLE
enum UsbState { ST_IDLE, ST_OUT, ST_IN, ST_IN_CANCEL };
static volatile UsbState st = ST_IDLE;

static volatile bool   outDone = false;          // callback de envio terminou
static volatile int    outStatus = 0;
static volatile size_t outActual = 0;
static volatile bool   inDone = false;           // callback de leitura terminou
static volatile int    inStatus = 0;
static volatile size_t inActual = 0;

static volatile bool curPending = false;         // pedaço retirado da fila e ainda não entregue
static uint8_t  retries = 0;
static uint32_t inDeadline = 0, inHardDeadline = 0;

// Sobrevive a ESP.restart() (não é zerado no reinício por software)
RTC_NOINIT_ATTR static uint32_t usbStuckRestarts;

// Versão recém-atualizada ainda em teste? Um reinício nesse período faz o bootloader
// voltar para a versão anterior, então os reinícios por enumeração travada esperam
// a confirmação (cerca de 1 minuto depois de ligar).
static bool imagePendingVerify() {
  esp_ota_img_states_t s;
  return esp_ota_get_state_partition(esp_ota_get_running_partition(), &s) == ESP_OK && s == ESP_OTA_IMG_PENDING_VERIFY;
}

static const char* statusName(int s) {
  switch (s) {
    case USB_TRANSFER_STATUS_COMPLETED: return "COMPLETED";
    case USB_TRANSFER_STATUS_ERROR:     return "ERROR";
    case USB_TRANSFER_STATUS_TIMED_OUT: return "TIMED_OUT";
    case USB_TRANSFER_STATUS_CANCELED:  return "CANCELED";
    case USB_TRANSFER_STATUS_STALL:     return "STALL";
    case USB_TRANSFER_STATUS_NO_DEVICE: return "NO_DEVICE";
    case USB_TRANSFER_STATUS_OVERFLOW:  return "OVERFLOW";
    case USB_TRANSFER_STATUS_SKIPPED:   return "SKIPPED";
    default:                            return "?";
  }
}

// ----------------------------------------------------------------------------
static void clientEventCb(const usb_host_client_event_msg_t* msg, void*) {
  if (msg->event == USB_HOST_CLIENT_EVENT_NEW_DEV) {
    Serial.printf("[usb] evento: novo dispositivo no endereço %u\n", msg->new_dev.address);
    newDevAddr = msg->new_dev.address;
  } else if (msg->event == USB_HOST_CLIENT_EVENT_DEV_GONE) {
    if (msg->dev_gone.dev_hdl == dev) devGone = true;
  }
}

// Os callbacks só registram o resultado; o tratamento é feito no laço da tarefa.
static void outCb(usb_transfer_t* x) {
  outStatus = x->status;
  outActual = x->actual_num_bytes;
  outDone   = true;
}

static void inCb(usb_transfer_t* x) {
  inStatus = x->status;
  inActual = x->actual_num_bytes;
  inDone   = true;
}

// Depois de um erro o ESP-IDF deixa o endpoint parado e recusa novos envios.
// halt -> flush -> clear libera o endpoint (o flush cancela o que estiver pendente).
static void recoverEndpoint(uint8_t ep) {
  esp_err_t a = usb_host_endpoint_halt(dev, ep);
  esp_err_t b = usb_host_endpoint_flush(dev, ep);
  esp_err_t c = usb_host_endpoint_clear(dev, ep);
  Serial.printf("[usb] endpoint 0x%02X liberado (halt=%d flush=%d clear=%d)\n", ep, a, b, c);
}

static void closeDevice() {
  if (itfNum != 0xFF) usb_host_interface_release(client, dev, itfNum);
  if (dev) usb_host_device_close(client, dev);
  dev = nullptr;
  itfNum = 0xFF;
  epOut = epIn = 0;
  vid = 0;
  pid = 0;
  devGone = false;
  st = ST_IDLE;
}

// GET_DEVICE_ID (classe USB Printer, bRequest 0): identificação IEEE 1284 da
// impressora. Windows e Linux sempre fazem esse pedido ao conectar a impressora,
// antes de enviar dados. Mesmo método do projeto zebra-bridge-esp32.
static volatile bool ctrlDone = false;
static void ctrlCb(usb_transfer_t*) { ctrlDone = true; }

// Requisição de controle simples (sem dados ou IN de até 8 bytes).
// Retorna o status da transferência; em 'out' copia os bytes recebidos.
static int controlRequest(uint8_t reqType, uint8_t req, uint16_t value, uint16_t index,
                          uint16_t len, uint8_t* out = nullptr, int* outLen = nullptr) {
  const size_t setupLen = sizeof(usb_setup_packet_t);
  usb_transfer_t* x = nullptr;
  if (usb_host_transfer_alloc(setupLen + 64, 0, &x) != ESP_OK || !x) return -1;
  auto* sp = (usb_setup_packet_t*)x->data_buffer;
  sp->bmRequestType = reqType;
  sp->bRequest = req;
  sp->wValue = value;
  sp->wIndex = index;
  sp->wLength = len;
  x->device_handle = dev;
  x->bEndpointAddress = 0;
  x->num_bytes = setupLen + len;
  x->callback = ctrlCb;
  x->context = nullptr;
  ctrlDone = false;
  int status = -2;
  if (usb_host_transfer_submit_control(client, x) == ESP_OK) {
    uint32_t t0 = millis();
    while (!ctrlDone && millis() - t0 < 1000) usb_host_client_handle_events(client, pdMS_TO_TICKS(20));
    if (!ctrlDone) return -3;                         // pendente: não libera
    status = x->status;
    int n = x->actual_num_bytes - (int)setupLen;
    if (out && n > 0) memcpy(out, x->data_buffer + setupLen, n);
    if (outLen) *outLen = n;
  }
  usb_host_transfer_free(x);
  return status;
}

// Confere se a impressora está mesmo no estado "configurado"; se não estiver,
// envia SET_CONFIGURATION(1) diretamente.
static void ensureConfigured() {
  uint8_t cfgVal = 0xFF; int n = 0;
  int s = controlRequest(0x80, 0x08 /*GET_CONFIGURATION*/, 0, 0, 1, &cfgVal, &n);
  Serial.printf("[usb] GET_CONFIGURATION: status=%s valor=%d\n", statusName(s), n > 0 ? cfgVal : -1);
  if (s == USB_TRANSFER_STATUS_COMPLETED && n > 0 && cfgVal == 1) return;

  s = controlRequest(0x00, 0x09 /*SET_CONFIGURATION*/, 1, 0, 0);
  Serial.printf("[usb] SET_CONFIGURATION(1): status=%s\n", statusName(s));
  delay(50);
  cfgVal = 0xFF; n = 0;
  s = controlRequest(0x80, 0x08, 0, 0, 1, &cfgVal, &n);
  Serial.printf("[usb] GET_CONFIGURATION depois: status=%s valor=%d\n", statusName(s), n > 0 ? cfgVal : -1);
}

static void fetchDeviceId() {
  const size_t setupLen = sizeof(usb_setup_packet_t);
  usb_transfer_t* x = nullptr;
  if (usb_host_transfer_alloc(setupLen + 248, 0, &x) != ESP_OK || !x) {
    Serial.println("[usb] GET_DEVICE_ID: sem memória");
    return;
  }
  auto* sp = (usb_setup_packet_t*)x->data_buffer;
  sp->bmRequestType = USB_BM_REQUEST_TYPE_DIR_IN | USB_BM_REQUEST_TYPE_TYPE_CLASS | USB_BM_REQUEST_TYPE_RECIP_INTERFACE;
  sp->bRequest = 0;                                  // GET_DEVICE_ID
  sp->wValue   = 0;
  sp->wIndex   = (uint16_t)(itfNum << 8);            // interface no byte alto, alt 0
  sp->wLength  = 248;
  x->device_handle    = dev;
  x->bEndpointAddress = 0;
  x->num_bytes        = setupLen + 248;
  x->callback         = ctrlCb;
  x->context          = nullptr;
  ctrlDone = false;
  esp_err_t e = usb_host_transfer_submit_control(client, x);
  if (e != ESP_OK) {
    Serial.printf("[usb] GET_DEVICE_ID recusado: %s\n", esp_err_to_name(e));
  } else {
    uint32_t t0 = millis();
    while (!ctrlDone && millis() - t0 < 1000) usb_host_client_handle_events(client, pdMS_TO_TICKS(20));
    if (!ctrlDone) {
      Serial.println("[usb] GET_DEVICE_ID sem resposta em 1 s");
      return;                                        // não libera: a transferência ainda está pendente
    } else if (x->status != USB_TRANSFER_STATUS_COMPLETED) {
      Serial.printf("[usb] GET_DEVICE_ID falhou: %s (%d)\n", statusName(x->status), x->status);
    } else {
      int n = x->actual_num_bytes - (int)setupLen - 2;   // 2 bytes de tamanho no início
      if (n > 0) {
        char id[200];
        if (n > (int)sizeof(id) - 1) n = sizeof(id) - 1;
        memcpy(id, x->data_buffer + setupLen + 2, n);
        id[n] = 0;
        Serial.printf("[usb] id da impressora: %s\n", id);
      } else {
        Serial.println("[usb] GET_DEVICE_ID respondeu vazio");
      }
    }
  }
  usb_host_transfer_free(x);
}

static void openDevice(uint8_t addr) {
  esp_err_t oe = usb_host_device_open(client, addr, &dev);
  if (oe != ESP_OK) {
    Serial.printf("[usb] não foi possível abrir o dispositivo %u: %s (%d)\n", addr, esp_err_to_name(oe), oe);
    dev = nullptr;
    return;
  }

  const usb_device_desc_t* dd;
  usb_host_get_device_descriptor(dev, &dd);
  vid = dd->idVendor;
  pid = dd->idProduct;
  Serial.printf("[usb] dispositivo %04X:%04X conectado\n", dd->idVendor, dd->idProduct);   // Zebra = 0A5F

  usb_device_info_t info;
  if (usb_host_device_info(dev, &info) == ESP_OK)
    Serial.printf("[usb] velocidade=%s ep0_mps=%u config=%u\n",
                  info.speed == USB_SPEED_FULL ? "FULL" : (info.speed == USB_SPEED_LOW ? "LOW" : "?"),
                  info.bMaxPacketSize0, info.bConfigurationValue);

  const usb_config_desc_t* cfg;
  usb_host_get_active_config_descriptor(dev, &cfg);
  const uint8_t* p   = (const uint8_t*)cfg;
  const uint8_t* end = p + cfg->wTotalLength;

  // Diagnóstico: lista interfaces e endpoints como a impressora declara
  for (const uint8_t* q = p; q + 2 <= end && q[0] >= 2 && q + q[0] <= end; q += q[0]) {
    if (q[1] == USB_B_DESCRIPTOR_TYPE_INTERFACE && q[0] >= 9)
      Serial.printf("[usb]   itf %u alt %u classe %02X/%02X/%02X eps=%u\n", q[2], q[3], q[5], q[6], q[7], q[4]);
    else if (q[1] == USB_B_DESCRIPTOR_TYPE_ENDPOINT && q[0] >= 7)
      Serial.printf("[usb]     ep 0x%02X tipo %u mps %u intervalo %u\n", q[2], q[3] & 0x03, q[4] | (q[5] << 8), q[6]);
  }

  bool inPrinterItf = false;
  uint8_t candidate = 0xFF;
  // Percorre os descritores conferindo tamanhos, para não ler fora do buffer
  // caso o dispositivo envie um descritor malformado.
  while (p + 2 <= end && p[0] >= 2 && p + p[0] <= end) {
    if (p[1] == USB_B_DESCRIPTOR_TYPE_INTERFACE && p[0] >= 9) {
      if (epOut) break;                // já achamos a interface de impressora
      auto itf = (const usb_intf_desc_t*)p;
      inPrinterItf = (itf->bInterfaceClass == 0x07) && (itf->bAlternateSetting == 0);
      if (inPrinterItf) candidate = itf->bInterfaceNumber;
    } else if (p[1] == USB_B_DESCRIPTOR_TYPE_ENDPOINT && p[0] >= 7 && inPrinterItf) {
      auto ep = (const usb_ep_desc_t*)p;
      if ((ep->bmAttributes & 0x03) == 0x02) {            // bulk
        if (ep->bEndpointAddress & 0x80) { if (!epIn)  epIn  = ep->bEndpointAddress; }
        else                             { if (!epOut) epOut = ep->bEndpointAddress; }
      }
    }
    p += p[0];
  }

  if (!epOut || usb_host_interface_claim(client, dev, candidate, 0) != ESP_OK) {
    Serial.println("[usb] não é uma impressora USB (ou falha ao assumir a interface)");
    rejectedDev = true;
    epOut = epIn = 0;
    closeDevice();
    return;
  }
  itfNum = candidate;
  usbStuckRestarts = 0;                // enumerou: zera o contador de reinícios
  ensureConfigured();
  fetchDeviceId();                     // como Windows/Linux fazem antes de enviar dados
  st = ST_IDLE;
  retries = 0;
  rejectedDev = false;
  connections = connections + 1;
  printerReady = true;
  Serial.printf("[usb] impressora pronta: itf=%u OUT=0x%02X IN=0x%02X\n", itfNum, epOut, epIn);
}

static bool submitOut(const Chunk& c) {
  memcpy(outXfer->data_buffer, c.data, c.len);
  outXfer->num_bytes        = c.len;
  outXfer->device_handle    = dev;
  outXfer->bEndpointAddress = epOut;
  outXfer->callback         = outCb;
  outXfer->context          = nullptr;
  outDone = false;
  esp_err_t e = usb_host_transfer_submit(outXfer);
  if (e != ESP_OK) Serial.printf("[usb] envio recusado pelo host (esp_err=%d)\n", e);
  return e == ESP_OK;
}

static bool submitIn() {
  inXfer->num_bytes        = IN_SIZE;          // múltiplo do tamanho de pacote (64)
  inXfer->device_handle    = dev;
  inXfer->bEndpointAddress = epIn;
  inXfer->callback         = inCb;
  inXfer->context          = nullptr;
  inDone = false;
  return usb_host_transfer_submit(inXfer) == ESP_OK;
}

static void usbClientTask(void*) {
  usb_host_client_config_t cc = {};
  cc.is_synchronous    = false;
  cc.max_num_event_msg = 5;
  cc.async.client_event_callback = clientEventCb;
  cc.async.callback_arg = nullptr;
  ESP_ERROR_CHECK(usb_host_client_register(&cc, &client));

  usb_host_transfer_alloc(CHUNK_SIZE, 0, &outXfer);
  usb_host_transfer_alloc(IN_SIZE, 0, &inXfer);

  static Chunk cur;

  // Dispositivos enumerados antes do registro deste cliente não geram NEW_DEV.
  // Por isso, consulta a lista de endereços já conectados logo no início.
  uint8_t addrs[8];
  int numAddrs = 0;
  if (usb_host_device_addr_list_fill(sizeof(addrs), addrs, &numAddrs) == ESP_OK && numAddrs > 0) {
    Serial.printf("[usb] %d dispositivo(s) já conectado(s) no início\n", numAddrs);
    newDevAddr = addrs[0];
  }

  uint32_t lastInfo = 0;
  uint32_t stuckSince = 0;
  for (;;) {
    usb_host_client_handle_events(client, pdMS_TO_TICKS(5));

    // Diagnóstico enquanto não há impressora: quantos dispositivos o host enxerga
    if (!dev && millis() - lastInfo > 5000) {
      lastInfo = millis();
      usb_host_lib_info_t li;
      if (usb_host_lib_info(&li) == ESP_OK) {
        Serial.printf("[usb] aguardando impressora: dispositivos=%d clientes=%d\n", li.num_devices, li.num_clients);
        // Impressora no barramento mas enumeração travada: a ZD220 mantém o estado USB
        // entre reinícios da placa e, em sessões alternadas, a enumeração não conclui.
        // Reiniciar a placa resolve na tentativa seguinte (limite de 3 seguidas).
        if (li.num_devices > 0 && imagePendingVerify()) {
          Serial.println("[usb] enumeração travada; o reinício espera a confirmação da versão nova");
        } else if (li.num_devices > 0) {
          if (!stuckSince) stuckSince = millis();
          else if (millis() - stuckSince > 8000 && usbStuckRestarts < 3) {
            usbStuckRestarts++;
            Serial.printf("[usb] enumeração travada; reiniciando a placa (%lu/3)\n", (unsigned long)usbStuckRestarts);
            delay(100);
            ESP.restart();
          } else if (millis() - stuckSince > 600000 && !rejectedDev && !Update.isRunning()) {
            // Depois das 3 tentativas rápidas, continua tentando a cada 10 minutos
            Serial.println("[usb] enumeração ainda travada; reiniciando a placa");
            delay(100);
            ESP.restart();
          }
        } else {
          stuckSince = 0;
          rejectedDev = false;
        }
        // Há dispositivo enumerado e nenhum aberto: tenta abrir pela lista de endereços
        if (li.num_devices > 0 && !newDevAddr) {
          uint8_t al[8]; int na = 0;
          if (usb_host_device_addr_list_fill(sizeof(al), al, &na) == ESP_OK && na > 0) {
            Serial.printf("[usb] tentando abrir endereço %u\n", al[0]);
            newDevAddr = al[0];
          }
        }
      }
    }

    if (newDevAddr && !dev) {
      uint8_t a = newDevAddr;
      newDevAddr = 0;
      openDevice(a);
    }

    if (devGone) {
      if (printerReady) Serial.println("[usb] impressora desconectada");
      printerReady = false;
      curPending = false;
      // Espera as transferências pendentes voltarem (com NO_DEVICE) antes de fechar
      bool busy = (st == ST_OUT && !outDone) || ((st == ST_IN || st == ST_IN_CANCEL) && !inDone);
      if (!busy) closeDevice();
      continue;
    }

    if (!printerReady) { xQueueReset(txQueue); continue; }

    switch (st) {
      case ST_IDLE:
        if (!curPending && xQueueReceive(txQueue, &cur, 0) == pdTRUE) {
          // Um pedaço deste trabalho já foi perdido: descarta o resto, para a
          // impressora não receber uma etiqueta pela metade.
          if (dropJob && cur.job == dropJob) break;
          dropJob = 0;
          curPending = true; retries = 0;
        }
        if (curPending) {
          if (submitOut(cur)) st = ST_OUT;
          else {
            recoverEndpoint(epOut);
            if (++retries >= MAX_RETRIES) { curPending = false; dropJob = cur.job; usbErrors = usbErrors + 1; }
          }
        }
        break;

      case ST_OUT:
        if (!outDone) break;
        if (outStatus == USB_TRANSFER_STATUS_COMPLETED) {
          bytesPrinted = bytesPrinted + outActual;
          curPending = false;
          if (cur.expectResponse && epIn && submitIn()) {
            inDeadline     = millis() + IN_WINDOW_MS;
            inHardDeadline = millis() + IN_WINDOW_MS + 1000;
            st = ST_IN;
          } else {
            st = ST_IDLE;
          }
        } else if (outStatus == USB_TRANSFER_STATUS_NO_DEVICE) {
          st = ST_IDLE;                                   // devGone cuida do resto
        } else {
          Serial.printf("[usb] erro no envio: %s (%d), tentativa %u de %u\n",
                        statusName(outStatus), outStatus, retries + 1, MAX_RETRIES);
          recoverEndpoint(epOut);
          if (++retries >= MAX_RETRIES) {
            Serial.println("[usb] pedaço descartado após falhas seguidas; o resto do trabalho será descartado");
            curPending = false;
            dropJob = cur.job;
            usbErrors = usbErrors + 1;
          }
          st = ST_IDLE;                                   // reenvia o mesmo pedaço (curPending)
        }
        break;

      case ST_IN:
        if (inDone) {
          if (inStatus == USB_TRANSFER_STATUS_COMPLETED && inActual > 0) {
            xStreamBufferSend(rxStream, inXfer->data_buffer, inActual, 0);
            inDeadline = millis() + IN_IDLE_MS;           // pode vir mais um pedaço da resposta
            if ((int32_t)(inHardDeadline - millis()) > 0 && submitIn()) break;
          } else if (inStatus != USB_TRANSFER_STATUS_COMPLETED && inStatus != USB_TRANSFER_STATUS_NO_DEVICE) {
            Serial.printf("[usb] erro na leitura: %s (%d)\n", statusName(inStatus), inStatus);
            recoverEndpoint(epIn);
          }
          st = ST_IDLE;
        } else if ((int32_t)(millis() - inDeadline) > 0) {
          // Sem resposta no prazo: cancela a leitura pendente
          usb_host_endpoint_halt(dev, epIn);
          usb_host_endpoint_flush(dev, epIn);             // callback volta com CANCELED
          st = ST_IN_CANCEL;
        }
        break;

      case ST_IN_CANCEL:
        if (inDone) {
          usb_host_endpoint_clear(dev, epIn);
          st = ST_IDLE;
        }
        break;
    }
  }
}

static void usbLibTask(void*) {
  usb_host_config_t hc = {};
  hc.skip_phy_setup = false;
  hc.intr_flags     = ESP_INTR_FLAG_LEVEL1;
#if ESP_IDF_VERSION >= ESP_IDF_VERSION_VAL(5, 5, 0)
  // As bibliotecas do core Arduino 3.3 vêm com a FIFO do USB favorecendo saída
  // periódica (áudio/vídeo), o que deixa pouco espaço para recepção e para envio
  // bulk. Para a impressora, usa uma divisão equilibrada (256 linhas de 4 bytes no S3).
  hc.fifo_settings_custom.rx_fifo_lines   = 104;
  hc.fifo_settings_custom.nptx_fifo_lines = 64;
  hc.fifo_settings_custom.ptx_fifo_lines  = 32;
#endif
  esp_err_t ie = usb_host_install(&hc);
  if (ie != ESP_OK) {
    Serial.printf("[usb] falha ao iniciar o host USB: %s (%d)\n", esp_err_to_name(ie), ie);
    vTaskDelete(nullptr);
    return;
  }
  Serial.println("[usb] host iniciado");

  // Prioridades iguais às do projeto de referência (esp32-zp450-print-server)
  xTaskCreatePinnedToCore(usbClientTask, "usb_client", 6144, nullptr, 19, nullptr, 0);

  for (;;) {
    uint32_t flags;
    usb_host_lib_handle_events(portMAX_DELAY, &flags);
    if (flags & USB_HOST_LIB_EVENT_FLAGS_NO_CLIENTS) usb_host_device_free_all();
  }
}

// ----------------------------------------------------------------------------
void usbPrinterBegin() {
  // Após ligar a placa (ou reset pelo pino EN) o valor da RTC é lixo: zera.
  esp_reset_reason_t rr = esp_reset_reason();
  if (rr != ESP_RST_SW || usbStuckRestarts > 3) usbStuckRestarts = 0;
  txQueue  = xQueueCreate(TXQ_DEPTH, sizeof(Chunk));
  rxStream = xStreamBufferCreate(2048, 1);
  xTaskCreatePinnedToCore(usbLibTask, "usb_lib", 4096, nullptr, 20, nullptr, 0);
}

bool usbPrinterReady()    { return printerReady; }
bool usbPrinterHasSpace() { return uxQueueSpacesAvailable(txQueue) > 0; }
bool usbPrinterIdle()     { return uxQueueMessagesWaiting(txQueue) == 0 && !curPending && st == ST_IDLE; }
uint32_t usbPrinterBytes()  { return bytesPrinted; }
uint32_t usbPrinterErrors() { return usbErrors; }
uint16_t usbPrinterVid()    { return vid; }
uint16_t usbPrinterPid()    { return pid; }
uint32_t usbPrinterConnections() { return connections; }

bool usbPrinterSend(const uint8_t* d, size_t n, uint32_t timeoutMs, bool expectResponse, bool sameJob) {
  if (!printerReady) return false;
  static Chunk c;                      // estático: não pesa na pilha do loop()
  if (!sameJob || !lastJob) { if (++lastJob == 0) lastJob = 1; }
  while (n) {
    c.job = lastJob;
    c.len = min<size_t>(n, CHUNK_SIZE);
    c.expectResponse = expectResponse && (n <= CHUNK_SIZE);   // só o último pedaço
    memcpy(c.data, d, c.len);
    if (xQueueSend(txQueue, &c, pdMS_TO_TICKS(timeoutMs)) != pdTRUE || !printerReady) return false;
    d += c.len;
    n -= c.len;
  }
  return true;
}

size_t usbPrinterRead(uint8_t* buf, size_t max) {
  return xStreamBufferReceive(rxStream, buf, max, 0);
}
