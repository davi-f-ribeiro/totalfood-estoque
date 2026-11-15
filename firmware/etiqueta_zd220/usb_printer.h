#pragma once
#include <Arduino.h>

// Driver USB host para impressoras da classe USB Printer (0x07) no ESP32-S3.
// Roda em tarefas FreeRTOS próprias; as funções abaixo podem ser chamadas do loop().

void     usbPrinterBegin();
bool     usbPrinterReady();
bool     usbPrinterHasSpace();                                      // há espaço na fila de envio
bool     usbPrinterIdle();                                          // nada na fila nem em envio
// expectResponse: depois de enviar, lê a resposta da impressora (consultas ~HS, ~HQES, ^HH).
// sameJob: continuação do trabalho anterior (dados que chegam em partes, como na porta 9100).
// Se um pedaço de um trabalho se perde no USB, o resto dele é descartado.
bool     usbPrinterSend(const uint8_t* data, size_t len, uint32_t timeoutMs = 15000, bool expectResponse = false,
                        bool sameJob = false);
size_t   usbPrinterRead(uint8_t* buf, size_t max);                  // respostas da impressora (não bloqueia)
uint32_t usbPrinterBytes();
uint32_t usbPrinterErrors();
uint16_t usbPrinterVid();
uint16_t usbPrinterPid();
uint32_t usbPrinterConnections();                                   // aumenta a cada vez que a impressora fica pronta
