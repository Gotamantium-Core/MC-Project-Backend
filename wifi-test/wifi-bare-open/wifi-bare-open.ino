// Bare OPEN-network join test: no password at all. If THIS works but WPA2
// doesn't, it's crypto negotiation; if this fails too, it's L2/TX/core.
#include <WiFi.h>

#define WIFI_SSID "Income Tax Dept Van #3"

void ev(WiFiEvent_t e, WiFiEventInfo_t i) {
  if (e == ARDUINO_EVENT_WIFI_STA_CONNECTED) Serial.println("EV assoc-ok");
  else if (e == ARDUINO_EVENT_WIFI_STA_GOT_IP) {
    Serial.print("EV got-ip ");
    Serial.println(WiFi.localIP());
  } else if (e == ARDUINO_EVENT_WIFI_STA_DISCONNECTED) {
    Serial.print("EV drop ");
    Serial.println(i.wifi_sta_disconnected.reason);
  }
}

void setup() {
  Serial.begin(115200);
  delay(500);
  Serial.println("bare-open test");
  WiFi.mode(WIFI_STA);
  WiFi.setSleep(false);
  WiFi.onEvent(ev);
  WiFi.setAutoReconnect(true);
  WiFi.begin(WIFI_SSID);
}

void loop() {
  static unsigned long t = 0;
  if (millis() - t > 10000) {
    t = millis();
    Serial.print("status=");
    Serial.println((int)WiFi.status());
  }
}
