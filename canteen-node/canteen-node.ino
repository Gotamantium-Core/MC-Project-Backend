// Canteen queue node — one sketch, three roles.
// Flash per node after changing ROLE below. No delay() anywhere;
// loop() must keep spinning so client.loop() stays responsive.
#include <WiFi.h>
#include <esp_wifi.h>

#include <PubSubClient.h>
#include <Wire.h>
#include <LiquidCrystal_I2C.h>

// ---- per-node config: change these four, nothing else ----
#define ROLE_KITCHEN 1
#define ROLE_COUNTER_A 2
#define ROLE_COUNTER_B 3
#define ROLE ROLE_COUNTER_B // <-- set per node before flashing

#define WIFI_SSID "Income Tax Dept Van #3"
#define WIFI_PASS "may the sauce be with you"
#define MQTT_HOST "10.129.75.137" // this machine's LAN IP (mosquitto); recheck after network changes
#define MQTT_PORT 1883

#define LCD_ADDR 0x27 // run an I2C scanner first; try 0x3F if blank
#define PIN_SDA 5
#define PIN_SCL 6
#define PIN_BUZZER 7 // HYDZ 2-pin piezo: + here, - to GND
#define PIN_BUTTON 10 // tactile: G10 -> button -> GND, INPUT_PULLUP

// ---- role-derived topics ----
#if ROLE == ROLE_KITCHEN
#define NODE_ID "kitchen"
#define SUB_TOPIC "canteen/kitchen/now"
#define PUB_TOPIC "canteen/kitchen/done"
#elif ROLE == ROLE_COUNTER_A
#define NODE_ID "counter-a"
#define SUB_TOPIC "canteen/counter-a/now"
#define PUB_TOPIC "canteen/counter-a/served"
#else
#define NODE_ID "counter-b"
#define SUB_TOPIC "canteen/counter-b/ready"
#define PUB_TOPIC "canteen/counter-b/collected"
#endif
#define LWT_TOPIC "canteen/" NODE_ID "/online"

// ponytail: PubSubClient publishes QoS0 (no QoS1 publish API); fine on local
// LAN — backend dedupes via CompleteOrder ErrConflict. Subscribes at QoS1.

WiFiClient espClient;
PubSubClient client(espClient);
LiquidCrystal_I2C lcd(LCD_ADDR, 16, 2);

// Display state. hasContent=false means IDLE/NONE on screen.
String shownId, shownExtra, shownDepth;
bool hasContent = false;
String lastL0 = "\xff", lastL1 = "\xff"; // force first draw

unsigned long beepOffAt = 0;
void beep() {
  digitalWrite(PIN_BUZZER, HIGH);
  beepOffAt = millis() + 150;
}
void serviceBeep() {
  if (beepOffAt && (long)(millis() - beepOffAt) >= 0) {
    digitalWrite(PIN_BUZZER, LOW);
    beepOffAt = 0;
  }
}

void render() {
  String l0, l1;
#if ROLE == ROLE_KITCHEN
  if (!hasContent) l0 = "IDLE";
  else {
    l0 = "TK:" + shownId;
    if (shownExtra.length()) l0 += " [" + shownExtra + "]";
    l1 = "Q:" + shownDepth;
  }
#elif ROLE == ROLE_COUNTER_A
  if (!hasContent) l0 = "IDLE";
  else {
    l0 = "NOW:" + shownId;
    l1 = "Q:" + shownDepth;
  }
#else
  if (!hasContent) l0 = "NONE";
  else {
    l0 = "RDY:" + shownId;
    if (shownExtra.length()) l0 += " [" + shownExtra + "]";
    l1 = "W:" + shownDepth;
  }
#endif
  l0 = l0.substring(0, 16);
  l1 = l1.substring(0, 16);
  if (l0 == lastL0 && l1 == lastL1) return; // LCD writes only on change
  lastL0 = l0;
  lastL1 = l1;
  lcd.clear();
  lcd.setCursor(0, 0);
  lcd.print(l0);
  lcd.setCursor(0, 1);
  lcd.print(l1);
}

// Payloads: "105 Q44" | "106 POR Q3" | "106 POR W2" | "IDLE Q0" | "NONE W0"
void onMsg(char *topic, byte *payload, unsigned int len) {
  String msg;
  for (unsigned int i = 0; i < len; i++) msg += (char)payload[i];
  msg.trim();
  if (msg.startsWith("IDLE") || msg.startsWith("NONE")) {
    hasContent = false;
    shownId = "";
  } else {
    int s1 = msg.indexOf(' ');
    if (s1 < 0) {
      shownId = msg;
      shownExtra = "";
      shownDepth = "";
    } else {
      shownId = msg.substring(0, s1);
      String rest = msg.substring(s1 + 1);
      rest.trim();
      int s2 = rest.indexOf(' ');
      if (s2 < 0) {
        if (rest.length() && (rest[0] == 'Q' || rest[0] == 'W')) {
          shownDepth = rest.substring(1);
          shownExtra = "";
        } else {
          shownExtra = rest;
          shownDepth = "";
        }
      } else {
        shownExtra = rest.substring(0, s2);
        shownDepth = rest.substring(s2 + 1);
        if (shownDepth.length() && (shownDepth[0] == 'Q' || shownDepth[0] == 'W'))
          shownDepth = shownDepth.substring(1);
      }
    }
    hasContent = shownId.length() > 0;
  }
  render();
  beep();
}

// Edge-triggered, 30 ms debounce. Returns true once per press.
bool buttonPressed() {
  static int stable = HIGH, lastRaw = HIGH;
  static unsigned long lastChange = 0;
  int raw = digitalRead(PIN_BUTTON);
  if (raw != lastRaw) {
    lastRaw = raw;
    lastChange = millis();
  }
  if (millis() - lastChange >= 30 && raw != stable) {
    stable = raw;
    if (stable == LOW) return true;
  }
  return false;
}

void wifiEvent(WiFiEvent_t event, WiFiEventInfo_t info) {
  if (event == ARDUINO_EVENT_WIFI_STA_CONNECTED) Serial.println("EV assoc-ok");
  else if (event == ARDUINO_EVENT_WIFI_STA_GOT_IP) {
    Serial.print("EV got-ip: ");
    Serial.println(WiFi.localIP());
  } else if (event == ARDUINO_EVENT_WIFI_STA_DISCONNECTED) {
    Serial.print("EV drop reason=");
    Serial.println(info.wifi_sta_disconnected.reason);
  }
}

void wifiMaintain() {
  static bool wasUp = false;
  static unsigned long lastNote = 0;
  if (WiFi.status() == WL_CONNECTED) {
    if (!wasUp) {
      wasUp = true;
      Serial.print("WiFi up: ");
      Serial.println(WiFi.localIP());
    }
    return;
  }
  wasUp = false;
  if (millis() - lastNote < 15000) return; // heartbeat only: never touch the driver here
  lastNote = millis();
  Serial.print("WiFi down... status=");
  Serial.println((int)WiFi.status());
}

void mqttMaintain() {
  static unsigned long lastTry = 0;
  if (client.connected()) return;
  if (WiFi.status() != WL_CONNECTED) return;
  if (millis() - lastTry < 5000) return;
  lastTry = millis();
  String cid = String(NODE_ID) + "-" + String((uint32_t)ESP.getEfuseMac(), HEX);
  if (client.connect(cid.c_str(), NULL, NULL, LWT_TOPIC, 0, true, "0")) {
    client.publish(LWT_TOPIC, "1", true);
    client.subscribe(SUB_TOPIC, 1);
    Serial.println("MQTT up");
  } else {
    Serial.print("MQTT fail rc=");
    Serial.println(client.state());
  }
}

void setup() {
  Serial.begin(115200);
  Serial.print("Joining ");
  Serial.println(WIFI_SSID);
  pinMode(PIN_BUZZER, OUTPUT);
  digitalWrite(PIN_BUZZER, LOW);
  pinMode(PIN_BUTTON, INPUT_PULLUP);
  Wire.begin(PIN_SDA, PIN_SCL);
  lcd.init();
  lcd.backlight();
  lcd.setCursor(0, 0);
  lcd.print("Booting...");
  WiFi.mode(WIFI_STA);
  esp_wifi_set_protocol(WIFI_IF_STA, WIFI_PROTOCOL_11B); // weak-TX batch: DSSS-only, see bare test
  WiFi.setSleep(false); // phone hotspots often drop ESP auth frames when modem-sleep is on
  WiFi.onEvent(wifiEvent);
  WiFi.setAutoReconnect(true);
  WiFi.begin(WIFI_SSID, WIFI_PASS); // called once: re-calling begin() mid-handshake wedges the driver
  WiFi.setTxPower(WIFI_POWER_8_5dBm); // defective-HF-design fix (espressif/arduino-esp32#6767)
  client.setServer(MQTT_HOST, MQTT_PORT);
  client.setCallback(onMsg);
  render();
  beep(); // boot chirp: wiring alive
}

void loop() {
  wifiMaintain();
  mqttMaintain();
  client.loop();
  if (buttonPressed()) {
    if (!hasContent) {
      Serial.println("Button: ignored (idle)");
    } else {
      Serial.print("Button: publish ");
      Serial.print(PUB_TOPIC);
      Serial.print(" ");
      Serial.println(shownId);
      client.publish(PUB_TOPIC, shownId.c_str());
      hasContent = false; // clear locally; next head arrives from backend
      shownId = "";
      render();
      beep();
    }
  }
  serviceBeep();
}
