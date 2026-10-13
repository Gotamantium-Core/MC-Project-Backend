The **Distributed IoT Canteen Queue Management System** is a low-cost, high-throughput solution tailored for a high-volume campus canteen in Kerala. It decouples high-speed meal distribution from special order assembly using a 3-node wireless ESP32 network synchronized over MQTT.

---

### Hardware Architecture & Bill of Materials

* **Central Router:** A standard local Wi-Fi router handles network connections, offloading heavy traffic so ESP32s aren't bottlenecked by Access Point limits.
* **Hardware Baseline (Cost: ~₹2,000):**
* 3× ESP32 Development Boards (Handles local terminal logic and MQTT).
* 3× 16x2 I2C LCD Displays (Address `0x27`/`0x3F` for status alerts).
* 3× Active Buzzer Modules (Audio cues for counter steps).
* Pushbuttons using `INPUT_PULLUP` (Software debounced for advancing/clearing tokens).
* Standard 5V USB adapters for steady power.



---

### Node Roles & Station Workflows

* **Node 1: Kitchen Assembly Bay (Hot-Holding Warmers)**
* **Role:** Manages short-order items (porotta, chicken curry, snacks) pre-prepared in hot warmers.
* **Workflow:** Displays incoming order tokens (`TK: 106 [POR/CHK]`). Staff packages the pre-portioned items on a tray and hits the complete button, clearing the screen and notifying Counter B.


* **Node 2: Counter A (Rapid Meals Line – 85% Volume)**
* **Role:** Paces the rapid-fire standard meal line.
* **Workflow:** Staff scoops rice/curries and hands over pre-staged side dishes directly. Pressing the advance button increments the token display (`NOW SERVING: 105`) and sounds a chime to keep the line moving at ~5–10 seconds per customer. Bypasses Node 1 completely for standard meal orders.


* **Node 3: Counter B (Specials Pickup Terminal – 15% Volume)**
* **Role:** Dedicated collection point for short-order trays compiled by Node 1.
* **Workflow:** Automatically receives ready signals from Node 1, sounding a chime and displaying ready tokens (`READY: 106 [POR/CHK]`). Counter staff hands over the food, verifies the physical or digital token, and clears the screen.



---

### Software & Backend Architecture

* **Communication Layer:** MQTT protocol via HiveMQ cloud broker (or local server). MQTT payload uses short category codes (`MEAL`, `SNACK`, `POR/CHK`) to fit on 16x2 LCDs without scrolling text.
* **Firmware Guardrails:** Non-blocking C++ logic using `millis()` timing instead of `delay()` to maintain constant `client.loop()` pulses, alongside timer-based network auto-reconnect routines.
* **Financial & Wallet Management:**
* All financial logic (prepaid balances, UPI top-ups, monthly postpaid dues caps) is stored and processed on a **Central Web Server** (Node.js/Python/Firebase).
* Atomic balance debits occur on the database *before* an order is created.
* ESP32 nodes only receive pre-validated, authorized Token payloads over MQTT, keeping microcontrollers lightweight and secure against Wi-Fi drops or resets.
