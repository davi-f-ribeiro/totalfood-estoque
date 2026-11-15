#include "wms_queue.h"
#include "wms_ca.h"
#include "usb_printer.h"
#include <WiFi.h>
#include <WiFiClientSecure.h>
#include <HTTPClient.h>
#include <Preferences.h>
#include <time.h>

static volatile bool queueActive=false;
static volatile uint32_t queuePrinted=0;
static char queueInfo[160]="Aguardando acionamento manual.";
static portMUX_TYPE queueMux=portMUX_INITIALIZER_UNLOCKED;
static String cloudURL,cloudToken;
static constexpr size_t maxZPL=65536;
static constexpr int maxJobsPerPress=100;

static void queueMessage(const char* message){
 portENTER_CRITICAL(&queueMux);
 strncpy(queueInfo,message,sizeof(queueInfo)-1);
 queueInfo[sizeof(queueInfo)-1]=0;
 portEXIT_CRITICAL(&queueMux);
}
static void queueMessage(const String& message){queueMessage(message.c_str());}
bool wmsQueueBusy(){return queueActive;}
String wmsQueueStatus(){
 char message[sizeof(queueInfo)];
 uint32_t printed=queuePrinted;bool active=queueActive;
 portENTER_CRITICAL(&queueMux);
 memcpy(message,queueInfo,sizeof(queueInfo));
 portEXIT_CRITICAL(&queueMux);
 String result="{\"busy\":";
 result+=active?"true":"false";
 result+=",\"printed\":"+String(printed)+",\"message\":\"";
 // Output is only a local diagnostic; do not include tokens or cloud payload.
 for(const char*p=message;*p;p++)if(*p!='"'&&*p!='\\'&&(uint8_t)*p>=32)result+=*p;
 return result+"\"}";
}
static bool timeReady(){
 if(time(nullptr)>1700000000)return true;
 configTime(0,0,"pool.ntp.org","time.google.com");
 for(int attempt=0;attempt<35;attempt++){
  if(time(nullptr)>1700000000)return true;
  delay(250);
 }
 return false;
}
static void configureHTTP(HTTPClient& http,WiFiClientSecure& client,const String& uri){
 client.setCACert(WMS_ROOT_CA);
 http.begin(client,uri);
 http.setConnectTimeout(20000);
 http.setTimeout(65000); // HTTPClient::setTimeout takes uint16_t in ESP32 core 3.0.5.
 http.setReuse(false);
}
// Wake Render using a read-only request before reserving a label.
static bool warmUpWMS(const String& url){
 for(int attempt=0;attempt<2;attempt++){
  WiFiClientSecure client;
  HTTPClient http;
  configureHTTP(http,client,url+"/health");
  const int status=http.GET();
  http.end();
  if(status==200)return true;
  if((status>0&&status<500)||WiFi.status()!=WL_CONNECTED)return false;
 }
 return false;
}
static bool sendResult(const String& url,const String& token,uint64_t id,const String& claimToken,const String& state){
 WiFiClientSecure client;
 HTTPClient http;
 String endpoint=url+"/api/print/jobs/"+String((unsigned long)id)+(state=="SENT"?"/ack":state=="FAILED"?"/fail":"/uncertain");
 configureHTTP(http,client,endpoint);
 http.addHeader("Authorization","Bearer "+token);
 http.addHeader("Content-Type","application/json");
 String body="{\"claim_token\":\""+claimToken+"\"";
 if(state=="FAILED")body+=",\"error\":\"Falha antes do envio USB\"";
 if(state=="UNCERTAIN")body+=",\"error\":\"Reinicio ou falha durante envio USB\"";
 body+="}";
 int code=http.POST(body);
 http.end();
 return code==200;
}
static bool finishPending(Preferences& prefs){
 String state=prefs.getString("state","");
 if(state=="")return true;
 uint32_t id=prefs.getUInt("job",0);
 String claim=prefs.getString("claim","");
 // Reset after claiming, but before saving a USB result, is always uncertain.
 // Keep durable claim metadata until the server acknowledges the uncertainty.
 if(state!="CLAIMED"&&state!="SENT"&&state!="FAILED"&&state!="UNCERTAIN"){
  queueMessage("Estado de impressao desconhecido; confira a fila.");
  return false;
 }
 if(!id||claim.length()<32){queueMessage("Confirmacao local invalida; verifique a fila.");return false;}
 String outcome=state=="CLAIMED"?"UNCERTAIN":state;
 if(!sendResult(cloudURL,cloudToken,id,claim,outcome)){
  queueMessage("Sem confirmação do servidor. Toque novamente sem reimprimir.");
  return false;
 }
 prefs.remove("state");prefs.remove("job");prefs.remove("claim");
 return true;
}
static void queueWorker(void*){
 Preferences prefs;
 if(!prefs.begin("wms-ack",false)){
  queueMessage("Falha ao abrir registro de confirmação.");
  queueActive=false;vTaskDelete(nullptr);return;
 }
 if(!timeReady()){
  queueMessage("Sem hora confiável para validar HTTPS.");
  prefs.end();queueActive=false;vTaskDelete(nullptr);return;
 }
 queueMessage("Conectando ao TotalFood. A primeira consulta pode demorar...");
 if(!warmUpWMS(cloudURL)){
  queueMessage("Servidor TotalFood indisponível. Tente novamente.");
  prefs.end();queueActive=false;vTaskDelete(nullptr);return;
 }
 // A reboot may have interrupted USB sending. Reconcile without reprinting
 // and stop this button press so an operator can review the uncertainty.
 String previous=prefs.getString("state","");
 if(!finishPending(prefs)){
  prefs.end();queueActive=false;vTaskDelete(nullptr);return;
 }
 if(previous=="CLAIMED"||previous=="UNCERTAIN"){
  queueMessage("Envio anterior incerto. Confira no WMS antes de reenviar.");
  prefs.end();queueActive=false;vTaskDelete(nullptr);return;
 }
 for(int count=0;count<maxJobsPerPress;count++){
  if(WiFi.status()!=WL_CONNECTED){queueMessage("Wi-Fi desconectado. Fila preservada.");break;}
  WiFiClientSecure client;
  HTTPClient http;
  configureHTTP(http,client,cloudURL+"/api/print/jobs/claim?format=zpl");
  const char* wanted[]={"X-Print-Job-ID","X-Print-Claim"};
  http.collectHeaders(wanted,2);
  http.addHeader("Authorization","Bearer "+cloudToken);
  http.addHeader("Content-Type","application/json");
  queueMessage("Consultando fila TotalFood...");
  int code=http.POST("{}");
  if(code==204){
   http.end();
   queueMessage(queuePrinted?"Fila concluída.":"Nenhuma etiqueta pendente.");
   break;
  }
  if(code!=200){
   http.end();queueMessage("Servidor indisponível ou credenciais inválidas.");break;
  }
  String jobID=http.header("X-Print-Job-ID");
  String claimToken=http.header("X-Print-Claim");
  String zpl=http.getString();
  http.end();
  uint32_t id=(uint32_t)jobID.toInt();
  if(!id||claimToken.length()<32||zpl.length()==0||zpl.length()>maxZPL||
      !zpl.startsWith("^XA")||zpl.indexOf("^XZ")<0){
   queueMessage("Trabalho recebido inválido; verifique a fila no WMS.");
   break; // Claim expires to UNCERTAIN, never silently retry.
  }
  // Durable intent BEFORE sending; a reset does not generate a duplicate.
  if(prefs.putUInt("job",id)==0 ||
     prefs.putString("claim",claimToken)==0 ||
     prefs.putString("state","CLAIMED")==0){
   queueMessage("Falha ao salvar a reserva no ESP. Nenhuma etiqueta enviada.");
   break; // The server will mark the unacknowledged lease UNCERTAIN.
  }
  bool sent=false, attempted=false;
  if(usbPrinterReady()){
   uint32_t start=millis();
   while(!usbPrinterIdle()&&millis()-start<10000)delay(50);
   if(usbPrinterIdle()){
    uint32_t errors=usbPrinterErrors();
    attempted=true; // The driver may have sent some data even if it returns false.
    bool queued=usbPrinterSend((const uint8_t*)zpl.c_str(),zpl.length(),10000,false);
    if(queued){
     start=millis();
     while(!usbPrinterIdle()&&millis()-start<30000)delay(50);
     sent=usbPrinterReady()&&usbPrinterIdle()&&usbPrinterErrors()==errors;
    }
   }
  }
  String outcome=sent?"SENT":(attempted?"UNCERTAIN":"FAILED");
  if(prefs.putString("state",outcome)==0){
   queueMessage("Falha ao registrar resultado USB. Verifique a etiqueta no WMS.");
   break; // CLAIMED remains the conservative recovery state.
  }
  if(!finishPending(prefs)){break;}
  if(!sent){
   queueMessage(attempted?"Envio USB incerto. Confira a fila antes de reenviar.":
                         "Impressora indisponivel. Etiqueta nao enviada.");
   break;
  }
  queuePrinted++;
  queueMessage("Etiqueta enviada ao USB. Buscando a próxima...");
  delay(20);
  if(count==maxJobsPerPress-1)queueMessage("Limite de 100 trabalhos por acionamento.");
 }
 prefs.end();
 queueActive=false;
 vTaskDelete(nullptr);
}
bool wmsQueueStart(const String& baseURL,const String& bearerToken){
 if(queueActive||WiFi.status()!=WL_CONNECTED||!usbPrinterReady()||
   !baseURL.startsWith("https://")||bearerToken.length()<32)return false;
 cloudURL=baseURL;cloudToken=bearerToken;
 while(cloudURL.endsWith("/"))cloudURL.remove(cloudURL.length()-1);
 queuePrinted=0;queueActive=true;
 queueMessage("Iniciando consulta manual...");
 BaseType_t ok=xTaskCreatePinnedToCore(queueWorker,"wms_print",12288,nullptr,1,nullptr,0);
 if(ok!=pdPASS){queueActive=false;queueMessage("Memória insuficiente para consulta.");return false;}
 return true;
}
