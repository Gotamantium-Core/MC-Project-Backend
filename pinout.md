# Node Pinouts — Canteen Queue System

All three nodes are wired identically. Only the `ROLE` flag and what's on the LCD differs. Build one, copy it twice.

## Parts per node

- 1× ESP32-C3 board
- 1× LCD1602 with I2C backpack
- 1× HYDZ 5V active piezo buzzer (2-pin)
- 1× tactile push button

## Common wiring (Node 1, 2, 3)

Find these **labeled pins** on your ESP32-C3 board (physical row order varies by clone — go by label, not position):

- LCD SDA → **GPIO5**
- LCD SCL → **GPIO6**
- Buzzer `+` → **GPIO7**, Buzzer `-` → **GND**
  - `+` is the longer leg / marked side. Piezo draws only a few mA, so it runs straight off the GPIO — no transistor, no resistor.
  - It says 5V on the can but is plenty loud on 3.3V logic.
- Button → **GPIO10** to **GND** (`INPUT_PULLUP`, no resistor — one leg to G10, other leg to GND)
- LCD VCC → **VIN / 5V** pin (5V from USB), LCD GND → **GND**
- Don't touch **GPIO8/9** (strapping) or **GPIO18/19** (USB serial).

```
              USB 5V power
                  |
           +--------------+
           |  ESP32-C3    |
           |              |
LCD SDA ---| GPIO5    VIN |--- LCD VCC (5V)
LCD SCL ---| GPIO6    GND |--- LCD GND -+- Buzzer (-)
           |              |             +- Button leg B
Buzzer(+) -| GPIO7    GND |--- (spare GND)
Button ----| GPIO10       |
           +--------------+

LCD:     VCC -> VIN, GND -> GND, SDA -> G5, SCL -> G6
Buzzer:  + -> G7, - -> GND
Button:  G10 -> button -> GND
```

Breadboard view (MB102, one node):

```
[ESP32-C3] straddles center gap, USB facing outward

red rail (+)  : VIN -- LCD VCC
blue rail (-): GND -- LCD GND -- Buzzer (-) -- Button leg B

G5  -- LCD SDA
G6  -- LCD SCL
G7  -- Buzzer (+)
G10 -- Button leg A
```

## Node 1 — Kitchen

- `ROLE = KITCHEN`
- Same wiring as above
- LCD shows: `TK:106 [POR]` / `Q:3`
- Button publishes `kitchen/done <shown>`

## Node 2 — Counter A

- `ROLE = COUNTER_A`
- Same wiring as above
- LCD shows: `NOW:105` / `Q:44`
- Button publishes `counter-a/served <shown>`

## Node 3 — Counter B

- `ROLE = COUNTER_B`
- Same wiring as above
- LCD shows: `RDY:106 [POR]` / `W:2`
- Button publishes `counter-b/collected <shown>`

## First-boot checks, per node

- Flash a 10-line I2C scanner first — confirm `0x27` vs `0x3F` before hardcoding.
- Button test: serial prints on press, nothing on release bounce.
- Buzzer test: 150ms beep on boot, then silent. If silent, flip the legs — piezo polarity matters.
