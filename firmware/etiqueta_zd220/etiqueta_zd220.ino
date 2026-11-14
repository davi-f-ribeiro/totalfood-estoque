// =============================================================================
//  Etiquetas TotalFood — servidor de etiquetas para Zebra ZD220 com ESP32-S3
//
//  - Portal cativo (WiFiManager) para configurar o Wi-Fi
//  - Acesso por http://<nome>.local (nome configurável na interface)
//  - Tela web para preencher os campos e imprimir
//  - Modelo ZPL editável pela web (campos no formato %nome%)
//  - Atualização pelo navegador e pela Arduino IDE, com rollback automático
//    e espera da impressão em andamento antes de reiniciar
//  - LED RGB de status, porta RAW 9100 e MQTT opcionais
//
//  Arduino IDE -> Ferramentas:
//    Placa ............. ESP32S3 Dev Module (core esp32 3.x)
//    Flash Size ........ 16MB (128Mb)
//    Partition Scheme .. 16M Flash (3MB APP/9.9MB FATFS)   (2 slots de app para OTA)
//    PSRAM ............. OPI PSRAM
//    USB Mode .......... Hardware CDC and JTAG
//    USB CDC On Boot ... Disabled
//  Bibliotecas: WiFiManager (tzapu), PubSubClient (Nick O'Leary)
// =============================================================================

#include <WiFi.h>
#include <WebServer.h>
#include <ESPmDNS.h>
#include <ArduinoOTA.h>
#include <Update.h>
#include <Preferences.h>
#include <WiFiManager.h>
#include <PubSubClient.h>
#include <map>
#include <vector>
#include "esp_ota_ops.h"
#include "esp_task_wdt.h"

#include "usb_printer.h"
#include "wms_queue.h"
#include "default_template.h"
#include "web_pages.h"
#include "logo_png.h"

#define FW_VERSION    "1.6.0"
#define AP_NAME       "Etiqueta-Setup"
#define AP_PASSWORD   "etiqueta"          // senha do Wi-Fi do portal cativo (mín. 8)
#define ADMIN_USER    "admin"
#define MAX_TEMPLATE  12000
#define MAX_VALUE_LEN 120
#define MAX_RAW_JOB   65536

// LED RGB (WS2812) da placa. DevKitC-1 v1.0 e clones N16R8: GPIO48.
// DevKitC-1 v1.1: GPIO38. Use -1 para desligar. Algumas placas exigem fechar o jumper "RGB".
#define STATUS_LED_PIN 48
#define STATUS_LED_BRIGHTNESS 20

// Watchdog do laço principal: se o loop() ficar preso por este tempo, a placa reinicia.
#define LOOP_WDT_MS        60000
// Porta 9100: fecha a conexão que ficar este tempo sem enviar dados (PC que dormiu ou saiu da rede).
#define RAW_IDLE_MS        60000
// Wi-Fi caído: tenta reconectar a cada WIFI_RETRY_MS e reinicia a placa depois de WIFI_RESTART_MS.
#define WIFI_RETRY_MS      30000
#define WIFI_RESTART_MS    300000

// Tempo que uma versão nova precisa rodar, com Wi-Fi conectado, para ser confirmada.
#define OTA_CONFIRM_MS 60000

// Cores do LED de status. Fica aqui no topo para os protótipos gerados pela IDE.
enum LedColor { LED_OFF, LED_WIFI, LED_PORTAL, LED_NO_PRINTER, LED_READY, LED_PRINTING, LED_ATTENTION, LED_ERROR, LED_UPDATING };

// Tipo de etiqueta (câmara fria, despacho). Também no topo, pelo mesmo motivo.
struct LabelType {
  const char* id;          // usado na API (?tipo=...)
  const char* tplKey;      // chave do modelo na NVS
  const char* lastKey;     // chave dos últimos valores na NVS
  const char* def;         // modelo padrão embutido no firmware
  String      tpl;         // modelo atual
};

// ------------------------------ Rollback ------------------------------------
// Avisa o core para não confirmar a versão nova logo no boot. A confirmação é feita
// em loop() depois de OTA_CONFIRM_MS com o Wi-Fi conectado; se a versão nova travar
// ou reiniciar antes disso, o bootloader volta para a versão anterior.
extern "C" bool verifyRollbackLater() { return true; }

static bool otaPending = false;

// ----------------------------- Configuração ---------------------------------
struct Settings {
  String   host;
  String   adminPass;
  bool     raw9100;
  bool     mqttEn;
  String   mqttHost;
  uint16_t mqttPort;
  String   mqttUser;
  String   mqttPass;
  String   devId;
  String   wmsUrl;
  String   wmsToken;
} cfg;

Preferences prefs;

// Tipos de etiqueta: cada um tem o próprio modelo e os próprios últimos valores
LabelType labels[] = {
  { "camara",   "tpl",      "last",      DEFAULT_TEMPLATE,          "" },
  { "despacho", "tpl_desp", "last_desp", DEFAULT_TEMPLATE_DESPACHO, "" },
};

static void loadSettings() {
  cfg.host      = prefs.getString("host", "etiqueta");
  cfg.adminPass = prefs.getString("admin_pass", "etiqueta");
  cfg.raw9100   = prefs.getBool("raw9100", true);
  cfg.mqttEn    = prefs.getBool("mqtt_en", false);
  cfg.mqttHost  = prefs.getString("mqtt_host", "");
  cfg.mqttPort  = prefs.getUShort("mqtt_port", 1883);
  cfg.mqttUser  = prefs.getString("mqtt_user", "");
  cfg.mqttPass  = prefs.getString("mqtt_pass", "");
  cfg.devId     = prefs.getString("dev_id", "zd220-1");
  cfg.wmsUrl    = prefs.getString("wms_url", "");
  cfg.wmsToken  = prefs.getString("wms_token", "");

  for (auto& lt : labels) {
    size_t len = prefs.getBytesLength(lt.tplKey);
    if (len > 0 && len <= MAX_TEMPLATE) {
      std::vector<char> buf(len + 1, 0);
      prefs.getBytes(lt.tplKey, buf.data(), len);
      lt.tpl = String(buf.data());
      // Modelo salvo por uma versão antiga, com o logo embutido: troca pela chamada
      // do logo guardado na impressora e regrava (o modelo fica uns 3 a 4 KB menor).
      bool changed = false;
      for (const PrinterLogo& lg : PRINTER_LOGOS) {
        String old = "^GFA," + String(lg.bytes) + "," + String(lg.bytes) + "," + String(lg.rowBytes) + "," + lg.hex;
        int at = lt.tpl.indexOf(old);
        if (at < 0) continue;
        lt.tpl = lt.tpl.substring(0, at) + "^XG" + lg.name + ",1,1" + lt.tpl.substring(at + old.length());
        changed = true;
      }
      if (changed) {
        prefs.putBytes(lt.tplKey, lt.tpl.c_str(), lt.tpl.length());
        Serial.printf("[cfg] modelo %s convertido para o logo na impressora\n", lt.id);
      }
    } else {
      lt.tpl = lt.def;
    }
  }
}

static void saveSettings() {
  prefs.putString("host", cfg.host);
  prefs.putString("admin_pass", cfg.adminPass);
  prefs.putBool("raw9100", cfg.raw9100);
  prefs.putBool("mqtt_en", cfg.mqttEn);
  prefs.putString("mqtt_host", cfg.mqttHost);
  prefs.putUShort("mqtt_port", cfg.mqttPort);
  prefs.putString("mqtt_user", cfg.mqttUser);
  prefs.putString("mqtt_pass", cfg.mqttPass);
  prefs.putString("dev_id", cfg.devId);
  if (prefs.getString("wms_url", "") != cfg.wmsUrl) prefs.putString("wms_url", cfg.wmsUrl);
  if (prefs.getString("wms_token", "") != cfg.wmsToken) prefs.putString("wms_token", cfg.wmsToken);
}

// Nome de acesso (mDNS): a-z, 0-9 e hífen, 1 a 31 caracteres
static bool validHost(const String& h) {
  if (h.length() < 1 || h.length() > 31 || h[0] == '-' || h[h.length() - 1] == '-') return false;
  for (char c : h) if (!((c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c == '-')) return false;
  return true;
}

// ------------------------------- Modelo -------------------------------------
static bool validFieldName(const String& n) {
  if (n.length() < 1 || n.length() > 32) return false;
  for (char c : n) if (!(isalnum((unsigned char)c) || c == '_')) return false;
  return true;
}

// Campos calculados pelo firmware (não aparecem na tela)
static bool isComputedField(const String& n) {
  return n == "produto_y" || n == "produto_h" || n == "produto_w" || n == "produto_linhas" ||
         n == "destino_y" || n == "destino_h" || n == "destino_w";
}

// Lista os campos %nome% na ordem em que aparecem (sem repetir)
static std::vector<String> templateFields(const String& t) {
  std::vector<String> out;
  int i = 0;
  while ((i = t.indexOf('%', i)) >= 0) {
    int j = t.indexOf('%', i + 1);
    if (j < 0) break;
    String n = t.substring(i + 1, j);
    if (validFieldName(n) && isComputedField(n)) { i = j + 1; continue; }
    if (validFieldName(n)) {
      bool dup = false;
      for (auto& f : out) if (f == n) { dup = true; break; }
      if (!dup) out.push_back(n);
      i = j + 1;
    } else {
      i++;
    }
  }
  return out;
}

// Remove caracteres de comando ZPL (^ ~) e de controle; limita o tamanho
static String sanitize(const String& v) {
  String o;
  o.reserve(v.length());
  for (size_t k = 0; k < v.length(); k++) {
    char c = v[k];
    if (c == '^' || c == '~' || ((uint8_t)c < 0x20)) continue;
    o += c;
  }
  o.trim();
  if (o.length() > MAX_VALUE_LEN) {
    size_t cut = MAX_VALUE_LEN;
    while (cut > 0 && ((uint8_t)o[cut] & 0xC0) == 0x80) cut--;   // não corta no meio de um caractere UTF-8
    o = o.substring(0, cut);
  }
  return o;
}

static String renderLabel(const String& t, std::map<String, String>& vals, int copies) {
  String out;
  out.reserve(t.length() + 256);
  int i = 0, L = t.length();
  while (i < L) {
    if (t[i] == '%') {
      int j = t.indexOf('%', i + 1);
      if (j > i) {
        String n = t.substring(i + 1, j);
        if (validFieldName(n)) { out += vals[n]; i = j + 1; continue; }
      }
    }
    out += t[i++];
  }
  // Quantidade: troca o ^PQ existente ou insere antes do ^XZ final
  String pq = "^PQ" + String(copies) + "\n";
  int p = out.indexOf("^PQ");
  if (p >= 0) {
    int e = p + 3;
    while (e < (int)out.length() && out[e] != '^' && out[e] != '~') e++;
    out = out.substring(0, p) + pq + out.substring(e);
  } else {
    int xz = out.lastIndexOf("^XZ");
    if (xz >= 0) out = out.substring(0, xz) + pq + out.substring(xz);
  }
  return out;
}

// Número de caracteres (não de bytes) de um texto UTF-8
static int utf8Len(const String& s) {
  int n = 0;
  for (size_t k = 0; k < s.length(); k++) if (((uint8_t)s[k] & 0xC0) != 0x80) n++;
  return n;
}

// Nome do produto em destaque (Etiqueta Despacho): escolhe a maior fonte em que o
// nome cabe e centraliza o bloco na faixa do corpo reservada a ele (y 136 a 214,
// entre a faixa do destino e a linha da manipulação).
// Largura média de caractere da fonte 0 da ZD220 ~ 0,5 x w (medida na etiqueta impressa).
static void fitProduct(std::map<String, String>& vals) {
  struct Opt { int h, w, lines; };
  static const Opt opts[] = { {56, 46, 1}, {36, 30, 2}, {30, 25, 2} };
  const int AREA_W = 448, TOP = 136, AREA_H = 78, GAP = 4;
  String name = vals["produto"];
  const Opt* chosen = &opts[2];
  int used = 1;
  for (const Opt& o : opts) {
    float cw = 0.5f * o.w;
    int lines = 1, cur = 0;
    int i = 0, L = name.length();
    while (i < L) {                               // quebra por palavras, como o ^FB
      int sp = name.indexOf(' ', i); if (sp < 0) sp = L;
      int wl = utf8Len(name.substring(i, sp));
      int need = cur ? cur + 1 + wl : wl;
      if (need * cw <= AREA_W || cur == 0) cur = need;
      else { lines++; cur = wl; }
      i = sp + 1;
    }
    used = lines;
    chosen = &o;
    if (lines <= o.lines) break;
  }
  int n = min(used, chosen->lines);
  vals["produto_h"]      = String(chosen->h);
  vals["produto_w"]      = String(chosen->w);
  vals["produto_linhas"] = String(chosen->lines);
  vals["produto_y"]      = String(TOP + (AREA_H - (n * chosen->h + (n - 1) * GAP)) / 2);
}

// Destino na faixa preta (Etiqueta Despacho, faixa de y 90 a 132): uma linha,
// a maior fonte em que o texto cabe (448 pontos menos 12 de folga), centralizada.
static void fitDestino(std::map<String, String>& vals) {
  struct Opt { int h, w; };
  static const Opt opts[] = { {32, 28}, {26, 22}, {22, 18} };
  const int BAND_Y = 90, BAND_H = 42, AREA_W = 436;
  int n = utf8Len(vals["destino"]);
  const Opt* chosen = &opts[2];
  for (const Opt& o : opts) if (n * 0.5f * o.w <= AREA_W) { chosen = &o; break; }
  vals["destino_h"] = String(chosen->h);
  vals["destino_w"] = String(chosen->w);
  vals["destino_y"] = String(BAND_Y + (BAND_H - chosen->h) / 2 + 2);
}

// Lista de destinos sugeridos na tela (um por linha, guardada na NVS)
#define MAX_DESTINOS 60
static const char DEFAULT_DESTINOS[] = "BS Ville\nBS Ville (ADM)\nTikTok\nEngeplan - Maranguape";

static std::vector<String> loadDestinos() {
  std::vector<String> out;
  String s = prefs.getString("destinos", DEFAULT_DESTINOS);
  int i = 0;
  while (i <= (int)s.length()) {
    int e = s.indexOf('\n', i); if (e < 0) e = s.length();
    String d = s.substring(i, e); d.trim();
    if (d.length() && out.size() < MAX_DESTINOS) {
      bool dup = false;
      for (auto& x : out) if (x.equalsIgnoreCase(d)) { dup = true; break; }
      if (!dup) out.push_back(d);
    }
    i = e + 1;
  }
  return out;
}

static bool saveDestinos(const std::vector<String>& list) {
  String s;
  for (auto& d : list) { if (s.length()) s += '\n'; s += d; }
  if (s == prefs.getString("destinos", DEFAULT_DESTINOS)) return true;
  return prefs.putString("destinos", s) > 0 || s.length() == 0;
}

// Destino digitado que ainda não está na lista entra no fim dela
static void rememberDestino(const String& d) {
  if (!d.length()) return;
  auto list = loadDestinos();
  for (auto& x : list) if (x.equalsIgnoreCase(d)) return;
  if (list.size() >= MAX_DESTINOS) return;
  list.push_back(d);
  saveDestinos(list);
}

// Tipo de etiqueta pelo id (camara | despacho); padrão: câmara fria
static LabelType& labelById(const String& id) {
  for (auto& lt : labels) if (id == lt.id) return lt;
  return labels[0];
}

// Últimos valores usados (voltam preenchidos na próxima impressão)
static std::map<String, String> loadLast(const char* key) {
  std::map<String, String> m;
  String s = prefs.getString(key, "");
  int i = 0;
  while (i < (int)s.length()) {
    int e = s.indexOf('\x1e', i); if (e < 0) e = s.length();
    int k = s.indexOf('\x1f', i);
    if (k > i && k < e) m[s.substring(i, k)] = s.substring(k + 1, e);
    i = e + 1;
  }
  return m;
}

// Data e hora da manipulação não são guardadas: a tela sempre as preenche com o
// momento atual, e gravá-las faria cada impressão escrever na flash.
static void saveLast(const char* key, const std::map<String, String>& m) {
  String s;
  for (auto& kv : m) {
    if (isComputedField(kv.first) || kv.first == "manipulacao" || kv.first == "hora_manipulacao") continue;
    s += kv.first; s += '\x1f'; s += kv.second; s += '\x1e';
  }
  if (s != prefs.getString(key, "")) prefs.putString(key, s);   // evita gravar à toa
}

// --------------------------- Status da impressora ---------------------------
struct {
  bool     paperOut = false, paused = false, headUp = false;
  uint32_t validAt  = 0;
} hs;
uint32_t hsSentAt = 0;

// Um trabalho RAW (porta 9100) chega em vários pedaços; nada pode ser
// intercalado no meio dele.
uint32_t lastRawRx = 0;
static bool rawBusy() { return lastRawRx && millis() - lastRawRx < 3000; }

static bool hsFresh() { return hs.validAt && millis() - hs.validAt < 30000; }

static const char* printerState() {
  if (!usbPrinterReady()) return "desconectada";
  if (hsFresh()) {
    if (hs.headUp)   return "cabeca_aberta";
    if (hs.paperOut) return "sem_papel";
    if (hs.paused)   return "pausada";
  }
  return "pronta";
}

static const char* printerStateText(const char* st) {
  if (!strcmp(st, "cabeca_aberta")) return "a tampa da impressora está aberta";
  if (!strcmp(st, "sem_papel"))     return "a impressora está sem etiquetas";
  if (!strcmp(st, "pausada"))       return "a impressora está pausada";
  return "";
}

static void queryStatus() {
  static const uint8_t cmd[] = { '~', 'H', 'S', '\r', '\n' };
  if (rawBusy() || wmsQueueBusy()) return;
  if (usbPrinterReady() && usbPrinterSend(cmd, sizeof(cmd), 0, true)) hsSentAt = millis();
}

// Resposta do ~HS: três blocos <STX>...<ETX>, campos separados por vírgula
static void parseHS(const uint8_t* b, size_t n) {
  std::vector<String> seg;
  String cur; bool in = false;
  for (size_t i = 0; i < n; i++) {
    if (b[i] == 0x02) { in = true; cur = ""; }
    else if (b[i] == 0x03) { if (in) seg.push_back(cur); in = false; }
    else if (in) cur += (char)b[i];
  }
  if (seg.size() < 2) return;
  auto field = [](const String& s, int idx) -> String {
    int start = 0;
    for (int k = 0; k < idx; k++) { start = s.indexOf(',', start); if (start < 0) return ""; start++; }
    int end = s.indexOf(',', start);
    return s.substring(start, end < 0 ? s.length() : end);
  };
  hs.paperOut = field(seg[0], 1) == "1";
  hs.paused   = field(seg[0], 2) == "1";
  hs.headUp   = field(seg[1], 2) == "1";
  hs.validAt  = millis();
}

// Os dados contêm um comando que gera resposta da impressora (~HS, ~HQES, ^HH...)?
// Só nesses casos o driver USB lê a resposta depois de enviar.
static bool hasQuery(const uint8_t* d, size_t n) {
  for (size_t i = 0; i + 1 < n; i++)
    if ((d[i] == '~' || d[i] == '^') && (d[i + 1] == 'H' || d[i + 1] == 'h')) return true;
  return false;
}

// Pode reiniciar/atualizar sem cortar uma impressão?
static bool printerIdle() { return !wmsQueueBusy() && usbPrinterIdle() && !rawBusy(); }

// --------------------------- Logos na impressora ----------------------------
// Os modelos chamam os logos com ^XG; as imagens ficam na memória R: da impressora,
// que se apaga quando ela é desligada. Por isso são reenviadas a cada conexão USB
// e depois de qualquer falha de envio.
static bool updating = false;

static void ensureLogos() {
  static uint32_t sentConn = 0, sentErr = 0;
  if (!usbPrinterReady() || updating || rawBusy() || wmsQueueBusy()) return;
  uint32_t conn = usbPrinterConnections(), err = usbPrinterErrors();
  if (conn == sentConn && err == sentErr) return;
  for (const PrinterLogo& lg : PRINTER_LOGOS) {
    String cmd;
    cmd.reserve(strlen(lg.hex) + 40);
    cmd += "~DG"; cmd += lg.name; cmd += ','; cmd += lg.bytes; cmd += ','; cmd += lg.rowBytes; cmd += ',';
    cmd += lg.hex;
    cmd += "\r\n";
    if (!usbPrinterSend((const uint8_t*)cmd.c_str(), cmd.length(), 3000)) return;   // tenta de novo depois
  }
  sentConn = conn;
  sentErr  = err;
  Serial.println("[usb] logos gravados na impressora");
}

// ------------------------------ LED de status -------------------------------
static void ledSet(LedColor c) {
#if STATUS_LED_PIN >= 0
  const uint8_t b = STATUS_LED_BRIGHTNESS;
  uint8_t r = 0, g = 0, bl = 0;
  switch (c) {
    case LED_WIFI:       r = b; bl = b; break;          // roxo: conectando ao Wi-Fi
    case LED_PORTAL:     r = b; g = b; bl = b; break;   // branco: aguardando configuração do Wi-Fi
    case LED_NO_PRINTER: bl = b; break;                 // azul: rede ok, impressora não detectada
    case LED_READY:      g = b; break;                  // verde: pronta
    case LED_PRINTING:   g = b; bl = b; break;          // ciano: enviando etiqueta
    case LED_ATTENTION:  r = b; g = b / 2; break;       // âmbar: sem etiquetas, pausada, tampa aberta
    case LED_ERROR:      r = b; break;                  // vermelho: falha no USB
    case LED_UPDATING:   r = b; bl = b / 2; break;      // rosa: atualizando o sistema
    default: break;
  }
  rgbLedWrite(STATUS_LED_PIN, r, g, bl);
#endif
}

static void updateLed() {
#if STATUS_LED_PIN >= 0
  static uint32_t last = 0, lastBytes = 0, lastErr = 0, errUntil = 0, busyUntil = 0;
  static int current = -1;
  if (millis() - last < 100) return;
  last = millis();

  uint32_t bytes = usbPrinterBytes(), err = usbPrinterErrors();
  if (err != lastErr) { lastErr = err; errUntil = millis() + 3000; }
  if (bytes - lastBytes > 16) busyUntil = millis() + 500;      // ignora os 5 bytes do ~HS
  lastBytes = bytes;

  LedColor c;
  if (updating)                                 c = LED_UPDATING;
  else if (!WiFi.isConnected())                 c = LED_WIFI;
  else if ((int32_t)(errUntil - millis()) > 0)  c = LED_ERROR;
  else if (!usbPrinterReady())                  c = LED_NO_PRINTER;
  else if ((int32_t)(busyUntil - millis()) > 0) c = LED_PRINTING;
  else if (strcmp(printerState(), "pronta"))    c = LED_ATTENTION;
  else                                          c = LED_READY;
  if (c != current) { current = c; ledSet(c); }
#endif
}

// ------------------------------- Rede ---------------------------------------
WebServer    server(80);
WiFiServer   rawServer(9100);
WiFiClient   rawClient;
WiFiClient   mqttNet;
PubSubClient mqtt(mqttNet);
uint32_t     restartAt = 0;

static String jsonEsc(const String& s) {
  String o;
  o.reserve(s.length() + 8);
  for (size_t i = 0; i < s.length(); i++) {
    char c = s[i];
    if (c == '"' || c == '\\') { o += '\\'; o += c; }
    else if ((uint8_t)c < 0x20) { char b[8]; snprintf(b, sizeof(b), "\\u%04x", c); o += b; }
    else o += c;
  }
  return o;
}

static bool requireAuth() {
  if (server.authenticate(ADMIN_USER, cfg.adminPass.c_str())) return true;
  server.requestAuthentication(BASIC_AUTH, "Etiquetas", "Digite o usuário e a senha de administrador.");
  return false;
}

static void sendJson(int code, const String& body) { server.send(code, "application/json", body); }
static void sendMsg(int code, bool ok, const String& msg) {
  sendJson(code, String("{\"ok\":") + (ok ? "true" : "false") + ",\"msg\":\"" + jsonEsc(msg) + "\"}");
}

static void scheduleRestart() { restartAt = millis() + 1000; }

// --- Páginas: cabeçalho + estilo + marca + corpo + JS comum + script da página ---
static void sendPage(const char* title, const char* bodyA, const char* bodyB) {
  // Envia as partes direto da flash, sem montar a página inteira na RAM
  const char* parts[] = { HEAD_A, title, HEAD_B, bodyA, COMMON_JS, bodyB };
  size_t total = 0;
  for (const char* p : parts) total += strlen(p);
  server.setContentLength(total);
  server.send(200, "text/html; charset=utf-8", "");
  for (const char* p : parts) server.sendContent(p, strlen(p));
}
static void handleIndex()    { sendPage("Imprimir", PAGE_INDEX_A, PAGE_INDEX_B); }
static void handleConfigPg() { if (requireAuth()) sendPage("Configurações", PAGE_CONFIG_A, PAGE_CONFIG_B); }
static void handleTplPg()    { if (requireAuth()) sendPage("Modelo da etiqueta", PAGE_TEMPLATE_A, PAGE_TEMPLATE_B); }
static void handleUpdatePg() { if (requireAuth()) sendPage("Atualizar sistema", PAGE_UPDATE_A, PAGE_UPDATE_B); }

static void handleLogo() {
  server.sendHeader("Cache-Control", "public, max-age=604800");
  server.send_P(200, "image/png", (const char*)LOGO_PNG, sizeof(LOGO_PNG));
}

// --- API de impressão ---
static void handleFields() {
  LabelType& lt = labelById(server.arg("tipo"));
  auto last = loadLast(lt.lastKey);
  String j = "{\"tipo\":\"" + String(lt.id) + "\",\"fields\":[";
  bool first = true;
  for (auto& f : templateFields(lt.tpl)) {
    if (!first) j += ',';
    first = false;
    j += "{\"n\":\"" + jsonEsc(f) + "\",\"v\":\"" + jsonEsc(last[f]) + "\"}";
  }
  String copies = last["__copias"];
  j += "],\"copias\":" + String(copies.length() ? copies.toInt() : 1) + "}";
  sendJson(200, j);
}

static void handlePrint() {
  if (wmsQueueBusy()) { sendMsg(409, false, "Fila do TotalFood em execução. Aguarde terminar."); return; }
  if (updating) { sendMsg(503, false, "O sistema está sendo atualizado. Tente de novo em instantes."); return; }
  if (!usbPrinterReady()) { sendMsg(503, false, "A impressora não está conectada. Confira o cabo USB e tente de novo."); return; }
  if (rawBusy()) { sendMsg(409, false, "A impressora está recebendo outro trabalho pela rede. Tente de novo em instantes."); return; }
  ensureLogos();                               // impressora recém-conectada: grava os logos antes da etiqueta

  LabelType& lt = labelById(server.arg("tipo"));
  std::map<String, String> vals;
  for (auto& f : templateFields(lt.tpl)) vals[f] = sanitize(server.arg(f));
  int copies = constrain(server.arg("copias").toInt(), 1, 999);
  if (lt.tpl.indexOf("%produto_h%") >= 0) fitProduct(vals);
  if (lt.tpl.indexOf("%destino_h%") >= 0) fitDestino(vals);

  String zpl = renderLabel(lt.tpl, vals, copies);
  if (!usbPrinterSend((const uint8_t*)zpl.c_str(), zpl.length(), 5000)) {
    sendMsg(503, false, "Não foi possível enviar para a impressora. Tente de novo.");
    return;
  }
  vals["__copias"] = String(copies);
  saveLast(lt.lastKey, vals);
  if (vals.count("destino")) rememberDestino(vals["destino"]);
  Serial.printf("[web] etiqueta %s enviada (%d cópia(s), %u bytes)\n", lt.id, copies, zpl.length());

  String msg = copies == 1 ? String("1 etiqueta enviada para a impressora.")
                           : String(copies) + " etiquetas enviadas para a impressora.";
  const char* st = printerState();
  if (strcmp(st, "pronta") != 0) msg += String(" Atenção: ") + printerStateText(st) + ".";
  sendMsg(200, true, msg);
}

// ZPL bruto via HTTP (integrações). Exige login de administrador.
static void handleRaw() {
  if (!requireAuth()) return;
  if (wmsQueueBusy()) { sendMsg(409, false, "Fila TotalFood em execução."); return; }
  const String& body = server.arg("plain");
  if (body.length() == 0 || body.length() > MAX_RAW_JOB) {
    sendMsg(413, false, "Envie de 1 byte a 64 KB de ZPL no corpo da requisição.");
    return;
  }
  if (!usbPrinterReady()) { sendMsg(503, false, "A impressora não está conectada."); return; }
  if (rawBusy())          { sendMsg(409, false, "A impressora está recebendo outro trabalho pela rede."); return; }
  bool ok = usbPrinterSend((const uint8_t*)body.c_str(), body.length(), 5000);
  sendMsg(ok ? 202 : 503, ok, ok ? "Enviado para a impressora." : "Não foi possível enviar para a impressora.");
}

static void handleStatus() {
  bool idle = printerIdle();                   // antes da consulta abaixo, que ocupa o USB por um instante
  if (usbPrinterReady() && millis() - hsSentAt > 3000) queryStatus();
  char b[640];
  snprintf(b, sizeof(b),
    "{\"printer\":\"%s\",\"online\":%s,\"idle\":%s,\"vidpid\":\"%04X:%04X\",\"bytes\":%lu,\"errors\":%lu,"
    "\"rssi\":%d,\"ip\":\"%s\",\"host\":\"%s\",\"fw\":\"%s\",\"ota_pending\":%s,\"updating\":%s,\"uptime\":%lu,"
    "\"heap\":%lu,\"heap_min\":%lu,\"heap_block\":%lu,\"reset\":%d}",
    printerState(), usbPrinterReady() ? "true" : "false", idle ? "true" : "false",
    usbPrinterVid(), usbPrinterPid(),
    (unsigned long)usbPrinterBytes(), (unsigned long)usbPrinterErrors(),
    WiFi.RSSI(), WiFi.localIP().toString().c_str(), jsonEsc(cfg.host).c_str(), FW_VERSION,
    otaPending ? "true" : "false", updating ? "true" : "false", (unsigned long)(millis() / 1000),
    (unsigned long)ESP.getFreeHeap(), (unsigned long)ESP.getMinFreeHeap(), (unsigned long)ESP.getMaxAllocHeap(),
    (int)esp_reset_reason());
  sendJson(200, b);
}

// --- API de configuração ---
static void handleGetConfig() {
  if (!requireAuth()) return;
  String j = "{";
  j += "\"host\":\"" + jsonEsc(cfg.host) + "\",";
  j += "\"raw9100\":" + String(cfg.raw9100 ? "true" : "false") + ",";
  j += "\"mqtt_en\":" + String(cfg.mqttEn ? "true" : "false") + ",";
  j += "\"mqtt_host\":\"" + jsonEsc(cfg.mqttHost) + "\",";
  j += "\"mqtt_port\":" + String(cfg.mqttPort) + ",";
  j += "\"mqtt_user\":\"" + jsonEsc(cfg.mqttUser) + "\",";
  j += "\"dev_id\":\"" + jsonEsc(cfg.devId) + "\",";
  j += "\"wms_url\":\"" + jsonEsc(cfg.wmsUrl) + "\",";
  j += "\"wms_paired\":" + String(cfg.wmsToken.length() >= 32 ? "true" : "false") + ",";
  j += "\"default_pass\":" + String(cfg.adminPass == "etiqueta" ? "true" : "false");
  j += "}";
  sendJson(200, j);
}

static void handleSetConfig() {
  if (!requireAuth()) return;
  String host = server.arg("host");
  host.trim();
  host.toLowerCase();
  if (!validHost(host)) {
    sendMsg(400, false, "Nome de acesso inválido. Use letras minúsculas, números e hífen, com até 31 caracteres.");
    return;
  }
  String pass = server.arg("admin_pass");
  if (pass.length() && pass.length() < 6) {
    sendMsg(400, false, "A senha precisa ter pelo menos 6 caracteres.");
    return;
  }
  cfg.host     = host;
  if (pass.length()) cfg.adminPass = pass;
  cfg.raw9100  = server.arg("raw9100") == "1";
  cfg.mqttEn   = server.arg("mqtt_en") == "1";
  cfg.mqttHost = server.arg("mqtt_host");
  cfg.mqttPort = constrain(server.arg("mqtt_port").toInt(), 1, 65535);
  cfg.mqttUser = server.arg("mqtt_user");
  if (server.arg("mqtt_pass").length()) cfg.mqttPass = server.arg("mqtt_pass");
  String dev = server.arg("dev_id");
  if (validFieldName(dev)) cfg.devId = dev;
  String wmsUrl = server.arg("wms_url"); wmsUrl.trim();
  if (wmsUrl.length() > 180 || (wmsUrl.length() && (!wmsUrl.startsWith("https://") || wmsUrl.indexOf("@") >= 0 || wmsUrl.indexOf(" ") >= 0 || wmsUrl.indexOf("?") >= 0 || wmsUrl.indexOf("#") >= 0))) {
    sendMsg(400, false, "Informe um endereço HTTPS válido para o WMS."); return;
  }
  while (wmsUrl.endsWith("/")) wmsUrl.remove(wmsUrl.length() - 1);
  cfg.wmsUrl = wmsUrl;
  if (server.arg("wms_token").length()) {
    if (server.arg("wms_token").length() < 32 || server.arg("wms_token").length() > 256) { sendMsg(400, false, "Token de pareamento inválido."); return; }
    cfg.wmsToken = server.arg("wms_token");
  }
  saveSettings();

  sendMsg(200, true, "Configurações salvas. O equipamento vai reiniciar; acesse http://" + cfg.host + ".local em alguns segundos.");
  scheduleRestart();
}

static void handleGetTemplate() {
  if (!requireAuth()) return;
  server.send(200, "text/plain; charset=utf-8", labelById(server.arg("tipo")).tpl);
}

static void handleSetTemplate() {
  if (!requireAuth()) return;
  String z = server.arg("zpl");
  z.replace("\r\n", "\n");
  if (z.indexOf("^XA") < 0 || z.indexOf("^XZ") < 0) {
    sendMsg(400, false, "O modelo precisa começar com ^XA e terminar com ^XZ.");
    return;
  }
  if (z.length() > MAX_TEMPLATE) {
    sendMsg(400, false, "O modelo passou do limite de 12 KB. Reduza o logo ou o texto.");
    return;
  }
  LabelType& lt = labelById(server.arg("tipo"));
  if (prefs.putBytes(lt.tplKey, z.c_str(), z.length()) != z.length()) {
    sendMsg(500, false, "Não foi possível salvar o modelo. Tente de novo.");
    return;
  }
  lt.tpl = z;
  sendMsg(200, true, "Modelo salvo.");
}

static void handleResetTemplate() {
  if (!requireAuth()) return;
  LabelType& lt = labelById(server.arg("tipo"));
  prefs.remove(lt.tplKey);
  lt.tpl = lt.def;
  sendMsg(200, true, "Modelo padrão restaurado.");
}

// --- Lista de destinos (sugestões do campo Destino) ---
static void handleGetDestinos() {
  String j = "{\"destinos\":[";
  bool first = true;
  for (auto& d : loadDestinos()) {
    if (!first) j += ',';
    first = false;
    j += "\"" + jsonEsc(d) + "\"";
  }
  j += "]}";
  sendJson(200, j);
}

static void handleSetDestinos() {
  if (!requireAuth()) return;
  String s = server.arg("lista");
  s.replace("\r", "");
  std::vector<String> list;
  int i = 0;
  while (i <= (int)s.length()) {
    int e = s.indexOf('\n', i); if (e < 0) e = s.length();
    String d = sanitize(s.substring(i, e));
    if (d.length() && list.size() < MAX_DESTINOS) {
      bool dup = false;
      for (auto& x : list) if (x.equalsIgnoreCase(d)) { dup = true; break; }
      if (!dup) list.push_back(d);
    }
    i = e + 1;
  }
  if (!saveDestinos(list)) { sendMsg(500, false, "Não foi possível salvar a lista. Tente de novo."); return; }
  sendMsg(200, true, String(list.size()) + (list.size() == 1 ? " destino salvo." : " destinos salvos."));
}

// Comandos de manutenção da impressora
static void handleCommand() {
  if (!requireAuth()) return;
  if (wmsQueueBusy()) { sendMsg(409, false, "Fila TotalFood em execução."); return; }
  String c = server.arg("c");
  const char* zpl = nullptr;
  if (c == "calibrar")    zpl = "~JC\r\n";
  else if (c == "config") zpl = "~WC\r\n";
  if (!zpl) { sendMsg(400, false, "Comando desconhecido."); return; }
  bool ok = usbPrinterSend((const uint8_t*)zpl, strlen(zpl), 2000);
  sendMsg(ok ? 200 : 503, ok, ok ? "Comando enviado para a impressora."
                                 : "A impressora não está conectada. Confira o cabo USB e tente de novo.");
}

static void handleWifiReset() {
  if (!requireAuth()) return;
  sendMsg(200, true, "Wi-Fi apagado. Conecte-se à rede " AP_NAME " para configurar de novo.");
  delay(300);
  WiFiManager wm;
  wm.resetSettings();
  scheduleRestart();
}

static void handleRestart() {
  if (!requireAuth()) return;
  sendMsg(200, true, "Reiniciando. A página volta em alguns segundos.");
  scheduleRestart();
}

// --- Atualização pelo navegador ---
// Recusa enquanto houver impressão; grava na partição OTA livre; o Update.end()
// confere o checksum da imagem. Reinicia só depois da fila de impressão esvaziar.
static bool   updFailed = false;
static String updError;

static void handleUpdateUpload() {
  HTTPUpload& up = server.upload();
  if (up.status == UPLOAD_FILE_START) {
    updFailed = false;
    updError  = "";
    if (!server.authenticate(ADMIN_USER, cfg.adminPass.c_str())) {
      updFailed = true; updError = "Digite o usuário e a senha de administrador."; return;
    }
    // A consulta de status (~HS) ocupa o USB por até 1,5 s: espera um pouco antes de recusar
    uint32_t t0 = millis();
    while (!printerIdle() && millis() - t0 < 4000) delay(20);
    if (!printerIdle()) {
      updFailed = true; updError = "A impressora está imprimindo. Aguarde terminar e tente de novo."; return;
    }
    Serial.printf("[ota] recebendo %s\n", up.filename.c_str());
    updating = true;
    if (!Update.begin(UPDATE_SIZE_UNKNOWN)) {
      updFailed = true; updError = "Não foi possível iniciar a atualização. Tente de novo.";
      updating = false;
    }
  } else if (up.status == UPLOAD_FILE_WRITE) {
    feedLoopWDT();                             // o envio inteiro acontece dentro de uma volta do loop()
    if (!updFailed && Update.write(up.buf, up.currentSize) != up.currentSize) {
      updFailed = true; updError = "O arquivo não é uma atualização válida para este equipamento.";
      Update.abort();
      updating = false;
    }
  } else if (up.status == UPLOAD_FILE_END) {
    if (!updFailed && !Update.end(true)) {
      updFailed = true; updError = "O arquivo não é uma atualização válida para este equipamento.";
      updating = false;
    }
  } else if (up.status == UPLOAD_FILE_ABORTED) {
    if (!updFailed) Update.abort();
    updFailed = true; updError = "O envio foi interrompido. Tente de novo.";
    updating = false;
  }
}

static void handleUpdateDone() {
  if (!requireAuth()) return;
  if (updFailed) { sendMsg(400, false, updError); return; }
  Serial.println("[ota] atualização gravada; reiniciando quando a impressora estiver livre");
  sendMsg(200, true, "Atualização concluída. O equipamento vai reiniciar e volta em cerca de 20 segundos.");
  scheduleRestart();
}

// ------------------------------ Wi-Fi ---------------------------------------
static bool portalSaved = false;

static void setupWifi() {
  WiFi.mode(WIFI_STA);
  WiFi.setSleep(false);
  ledSet(LED_WIFI);

  WiFiManager wm;
  WiFiManagerParameter pHost("host", "Nome de acesso (http://NOME.local)", cfg.host.c_str(), 31);
  wm.addParameter(&pHost);
  wm.setSaveParamsCallback([]() { portalSaved = true; });
  wm.setSaveConfigCallback([]() { portalSaved = true; });
  wm.setAPCallback([](WiFiManager*) { ledSet(LED_PORTAL); });
  wm.setHostname(cfg.host.c_str());
  wm.setTitle("Etiquetas TotalFood");
  wm.setConfigPortalTimeout(180);           // sem configuração em 3 min: reinicia e tenta de novo
  wm.setConnectTimeout(20);

  if (!wm.autoConnect(AP_NAME, AP_PASSWORD)) {
    Serial.println("[wifi] portal expirou sem conexão; reiniciando");
    ESP.restart();
  }

  if (portalSaved) {
    String h = String(pHost.getValue());
    h.trim();
    h.toLowerCase();
    if (validHost(h) && h != cfg.host) {
      cfg.host = h;
      saveSettings();
      Serial.println("[wifi] nome de acesso alterado; reiniciando");
      ESP.restart();                         // aplica o novo nome no DHCP e no mDNS
    }
  }
  Serial.printf("[wifi] conectado: %s  ->  http://%s.local\n",
                WiFi.localIP().toString().c_str(), cfg.host.c_str());
}

static void setupOtaAndMdns() {
  ArduinoOTA.setHostname(cfg.host.c_str());  // também inicia o mDNS com esse nome
  ArduinoOTA.setPassword(cfg.adminPass.c_str());
  ArduinoOTA.onStart([]() {
    // Espera até 30 s a impressão em andamento terminar antes de gravar
    uint32_t t0 = millis();
    while (!printerIdle() && millis() - t0 < 30000) { delay(50); feedLoopWDT(); }
    updating = true;
    ledSet(LED_UPDATING);
    Serial.println("[ota] atualização pela IDE iniciada");
  });
  ArduinoOTA.onProgress([](unsigned int, unsigned int) { feedLoopWDT(); });
  ArduinoOTA.onEnd([]()   { Serial.println("[ota] concluída"); });
  ArduinoOTA.onError([](ota_error_t e) { updating = false; Serial.printf("[ota] erro %u\n", e); });
  ArduinoOTA.begin();

  MDNS.setInstanceName("Etiquetas TotalFood");
  MDNS.addService("http", "tcp", 80);
  if (cfg.raw9100) MDNS.addService("pdl-datastream", "tcp", 9100);
}

static void handleQueueStatus() { sendJson(200, wmsQueueStatus()); }
static void handleQueuePull() {
  if (updating || rawBusy() || !wmsQueueStart(cfg.wmsUrl, cfg.wmsToken)) {
    sendMsg(409, false, "Confira Wi-Fi, impressora e pareamento em Configurações. Se a fila já estiver em andamento, aguarde.");
    return;
  }
  sendMsg(202, true, "Buscando etiquetas. Acompanhe o progresso aqui.");
}

static void setupWeb() {
  server.on("/",              HTTP_GET,  handleIndex);
  server.on("/config",        HTTP_GET,  handleConfigPg);
  server.on("/template",      HTTP_GET,  handleTplPg);
  server.on("/update",        HTTP_GET,  handleUpdatePg);
  server.on("/logo.png",      HTTP_GET,  handleLogo);

  server.on("/api/fields",    HTTP_GET,  handleFields);
  server.on("/api/print",     HTTP_POST, handlePrint);
  server.on("/api/queue/pull", HTTP_POST, handleQueuePull);
  server.on("/api/queue/status", HTTP_GET, handleQueueStatus);
  server.on("/api/raw",       HTTP_POST, handleRaw);
  server.on("/api/status",    HTTP_GET,  handleStatus);
  server.on("/health",        HTTP_GET,  handleStatus);

  server.on("/api/config",    HTTP_GET,  handleGetConfig);
  server.on("/api/config",    HTTP_POST, handleSetConfig);
  server.on("/api/template",  HTTP_GET,  handleGetTemplate);
  server.on("/api/template",  HTTP_POST, handleSetTemplate);
  server.on("/api/template/reset", HTTP_POST, handleResetTemplate);
  server.on("/api/destinos",  HTTP_GET,  handleGetDestinos);
  server.on("/api/destinos",  HTTP_POST, handleSetDestinos);
  server.on("/api/command",   HTTP_POST, handleCommand);
  server.on("/api/wifireset", HTTP_POST, handleWifiReset);
  server.on("/api/restart",   HTTP_POST, handleRestart);
  server.on("/api/update",    HTTP_POST, handleUpdateDone, handleUpdateUpload);

  server.onNotFound([]() { server.send(404, "text/plain; charset=utf-8", "Página não encontrada."); });
  server.begin();
}

// ------------------------------ MQTT ----------------------------------------
String T_PRINT, T_RESPONSE, T_STATUS;

static void onMqtt(char* topic, byte* payload, unsigned int len) {
  if (T_PRINT != topic) return;
  if (updating || wmsQueueBusy() || rawBusy() || !usbPrinterSend(payload, len, 5000, hasQuery(payload, len)))
    mqtt.publish(T_RESPONSE.c_str(), "{\"error\":\"printer_not_ready\"}");
}

static void publishStatus() {
  char b[200];
  snprintf(b, sizeof(b), "{\"online\":true,\"printer\":\"%s\",\"bytes\":%lu,\"errors\":%lu,\"rssi\":%d}",
           printerState(), (unsigned long)usbPrinterBytes(), (unsigned long)usbPrinterErrors(), WiFi.RSSI());
  mqtt.publish(T_STATUS.c_str(), b, true);
}

static void handleMqtt() {
  static uint32_t lastTry = 0, lastStatus = 0, retryMs = 15000;
  static String lastState;
  if (!mqtt.connected()) {
    // A tentativa de conexão segura o loop(); com o servidor fora do ar, o intervalo
    // entre tentativas dobra até 5 minutos, para não travar a tela a toda hora.
    if (lastTry && millis() - lastTry < retryMs) return;
    lastTry = millis() | 1;
    String id = "etiqueta-" + cfg.devId;
    const char* lwt = "{\"online\":false}";
    bool ok = cfg.mqttUser.length()
      ? mqtt.connect(id.c_str(), cfg.mqttUser.c_str(), cfg.mqttPass.c_str(), T_STATUS.c_str(), 1, true, lwt)
      : mqtt.connect(id.c_str(), T_STATUS.c_str(), 1, true, lwt);
    if (!ok) {
      retryMs = min<uint32_t>(retryMs * 2, 300000);
      Serial.printf("[mqtt] falha rc=%d; nova tentativa em %lu s\n", mqtt.state(), (unsigned long)(retryMs / 1000));
      return;
    }
    retryMs = 15000;
    mqtt.subscribe(T_PRINT.c_str(), 1);
    Serial.println("[mqtt] conectado");
    lastStatus = 0;
  }
  mqtt.loop();
  String st = printerState();
  if (st != lastState || millis() - lastStatus > 30000) {
    lastState = st;
    lastStatus = millis();
    publishStatus();
  }
}

// ---------------------------- Porta 9100 ------------------------------------
static void handleRaw9100() {
  static uint32_t lastActivity = 0;
  if (!rawClient || !rawClient.connected()) {
    rawClient = rawServer.accept();
    if (!rawClient) return;
    lastActivity = millis();
    Serial.println("[9100] cliente conectado");
  }
  if (updating) { lastActivity = millis(); return; }   // segura os dados no TCP durante a atualização
  static uint8_t buf[1024];
  while (usbPrinterReady() && rawClient.available() && usbPrinterHasSpace()) {
    int n = rawClient.read(buf, sizeof(buf));
    if (n <= 0) break;
    bool sameJob = rawBusy();                 // continuação do trabalho que já está chegando
    lastRawRx = millis() | 1;
    lastActivity = millis();
    usbPrinterSend(buf, n, 0, hasQuery(buf, n), sameJob);
  }
  // Conexão parada (computador dormiu ou saiu da rede): fecha para liberar a porta
  if (millis() - lastActivity > RAW_IDLE_MS) {
    Serial.println("[9100] conexão sem dados; fechada");
    rawClient.stop();
  }
}

// ----------------------- Respostas da impressora ----------------------------
static void handlePrinterResponses() {
  static uint8_t buf[1024];
  static size_t  len = 0;
  static uint32_t lastRx = 0;

  size_t n = usbPrinterRead(buf + len, sizeof(buf) - len);
  if (n) { len += n; lastRx = millis(); }
  if (!len || (millis() - lastRx < 150 && len < sizeof(buf))) return;

  bool internal = (millis() - hsSentAt < 1500) && buf[0] == 0x02;   // resposta ao ~HS da própria interface
  if (internal) {
    parseHS(buf, len);
  } else {
    if (cfg.mqttEn && mqtt.connected()) mqtt.publish(T_RESPONSE.c_str(), buf, len, false);
    if (rawClient && rawClient.connected()) rawClient.write(buf, len);
  }
  len = 0;
}

// --------------------------- Vigia do Wi-Fi ---------------------------------
// A reconexão automática do core às vezes para de tentar. Com o Wi-Fi caído, força
// uma nova tentativa a cada WIFI_RETRY_MS; se não voltar em WIFI_RESTART_MS, reinicia
// a placa (com a impressora livre e sem atualização em andamento).
static void watchWifi() {
  static uint32_t downSince = 0, lastRetry = 0;
  if (WiFi.isConnected()) { downSince = 0; return; }
  uint32_t now = millis();
  if (!downSince) { downSince = now | 1; lastRetry = now; Serial.println("[wifi] conexão perdida"); return; }
  if (now - downSince > WIFI_RESTART_MS && printerIdle() && !updating && !restartAt) {
    Serial.println("[wifi] sem conexão há muito tempo; reiniciando");
    delay(100);
    ESP.restart();
  }
  if (now - lastRetry > WIFI_RETRY_MS) { lastRetry = now; WiFi.reconnect(); }
}

// =============================================================================
void setup() {
  Serial.begin(115200);
  Serial.println("\n[boot] Etiquetas TotalFood v" FW_VERSION);

  const esp_partition_t* running = esp_ota_get_running_partition();
  esp_ota_img_states_t st;
  if (esp_ota_get_state_partition(running, &st) == ESP_OK && st == ESP_OTA_IMG_PENDING_VERIFY) {
    otaPending = true;
    Serial.println("[ota] versão nova em teste; será confirmada após 60 s com Wi-Fi");
  }

  prefs.begin("etq", false);
  loadSettings();

  usbPrinterBegin();
  setupWifi();
  setupOtaAndMdns();
  setupWeb();

  if (cfg.raw9100) rawServer.begin();

  // Watchdog do loop(): só depois do Wi-Fi, porque o portal cativo pode ficar minutos aberto
  esp_task_wdt_config_t wdt = {};
  wdt.timeout_ms     = LOOP_WDT_MS;
  wdt.idle_core_mask = 1 << 0;                // igual ao padrão do core (núcleo 0)
  wdt.trigger_panic  = true;
  esp_task_wdt_reconfigure(&wdt);
  enableLoopWDT();
  if (cfg.mqttEn && cfg.mqttHost.length()) {
    String base = "zebra/" + cfg.devId + "/";
    T_PRINT    = base + "print";
    T_RESPONSE = base + "response";
    T_STATUS   = base + "status";
    mqtt.setServer(cfg.mqttHost.c_str(), cfg.mqttPort);
    mqtt.setCallback(onMqtt);
    mqtt.setBufferSize(32768);
    mqtt.setKeepAlive(30);
    mqtt.setSocketTimeout(4);                 // padrão de 15 s travaria a tela com o servidor fora do ar
  }
}

void loop() {
  server.handleClient();
  ArduinoOTA.handle();

  if (cfg.raw9100 && !wmsQueueBusy()) handleRaw9100();
  if (cfg.mqttEn && cfg.mqttHost.length() && WiFi.isConnected()) handleMqtt();
  handlePrinterResponses();
  ensureLogos();
  updateLed();
  watchWifi();

  // Mantém o status da impressora atualizado (etiquetas, pausa, tampa)
  static uint32_t lastHs = 0;
  if (usbPrinterReady() && !updating && millis() - lastHs > 10000) { lastHs = millis(); queryStatus(); }

  // Confirma a versão nova depois de rodar estável com Wi-Fi
  if (otaPending && millis() > OTA_CONFIRM_MS && WiFi.isConnected()) {
    esp_ota_mark_app_valid_cancel_rollback();
    otaPending = false;
    Serial.println("[ota] versão nova confirmada");
  }

  // Reinício agendado: espera a impressão em andamento terminar (até 30 s)
  if (restartAt && (int32_t)(millis() - restartAt) > 0) {
    if (printerIdle() || millis() - restartAt > 30000) ESP.restart();
  }
  delay(1);
}
