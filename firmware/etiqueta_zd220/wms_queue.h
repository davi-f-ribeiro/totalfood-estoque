#pragma once
#include <Arduino.h>

// Manual-only cloud queue. No recurring requests to Render and no inbound ports.
bool wmsQueueStart(const String& baseURL,const String& bearerToken);
bool wmsQueueBusy();
String wmsQueueStatus();
