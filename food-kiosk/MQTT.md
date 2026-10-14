# Canteen MQTT

MQTT and backend logic are separate Go packages, composed by `main.go`:

- `database/`: existing orders, payments and database helpers, unchanged.
- `backend/`: queue routing, durable fulfillment stages and validated transitions.
- `mqtt/`: Eclipse Paho connections, station protocol, QoS 1 subscriptions and retained display publications. Depends on the small `Queues` interface; contains no SQL or payment logic.
- `broker/` and `compose.yaml`: local Mosquitto configuration with persistence.

## Run

From `food-kiosk/`, start the local broker and service:

```sh
docker compose up -d
go run .
```

Defaults: database `food-kiosk.db`, broker `tcp://127.0.0.1:1883`, client ID `canteen-backend`. Override with `DATABASE_PATH`, `MQTT_BROKER`, `MQTT_CLIENT_ID`, `MQTT_USERNAME`, `MQTT_PASSWORD`. ESP32s connect to the broker host's LAN address. Use a stable, unique client ID for the backend. Run one backend queue owner per database.

The broker configuration allows anonymous access for the local demo. Add Mosquitto password/ACL configuration for a shared network. The service reconnects without terminating when the broker is unavailable. Stop it with Ctrl+C or SIGTERM.

## Protocol

| Topic | Publisher | Retained | Payload |
|---|---|---|---|
| `canteen/counter-a/now` | backend | yes | `105 Q44` or `IDLE Q0` |
| `canteen/kitchen/now` | backend | yes | `106 POR Q3` or `IDLE Q0` |
| `canteen/counter-b/ready` | backend | yes | `106 POR W2` or `NONE W0` |
| `canteen/counter-a/served` | station | no | `105` |
| `canteen/kitchen/done` | station | no | `106` |
| `canteen/counter-b/collected` | station | no | `106` |

Publish station events at QoS 1. IDs are canonical positive decimal integers; whitespace, signs and leading zeros are rejected. Retained acknowledgements are ignored. `Q` counts customers behind the displayed head. `W` counts all ready trays, including the displayed tray.

Only `MEAL` orders go to Counter A. Any other item routes the whole order to Kitchen. Kitchen `done` persists the move to Ready without completing the order. Only handover events complete orders. Queues are FIFO by order ID; Ready is FIFO by kitchen-ready sequence. Duplicate events are harmless; wrong-lane, cancelled and non-head events cannot complete an order. Duplicate/rejected events re-send the affected station's current display.

## Integration and recovery

Existing callers can keep using `database.CreateOrder` and `database.CancelOrder`. The service reconciles committed pending orders and publishes changed displays every second, so these callers do not import MQTT. No HTTP routes or firmware are added by this change.

An additive `fulfillment` table records each order's lane, display code and ready sequence. Existing pending orders are backfilled on reconciliation. Prepared trays stay in Ready across backend restarts. Orders committed while MQTT is unavailable remain paid and pending, and are published after reconnection. Each reconnect re-subscribes and republishes all displays, including empty queues. Failed publications are retried; backend event messages are acknowledged only after the database transition succeeds. Transient database errors trigger a reconnect to redeliver unacknowledged QoS 1 events through the persistent session.

For legacy orders, the display code comes from the first non-MEAL order-item row. The database currently stores order lines in map iteration order, so this is not necessarily the original cart's first non-MEAL line. The chosen code is frozen once classified, converted to safe uppercase ASCII and limited to seven characters. The LCD code is a hint, not a full tray packing list. Classification of an unprocessed legacy order uses its menu codes at reconciliation time; changing menu codes before that can affect routing.

MQTT QoS 1 requires station firmware to actually publish at QoS 1; a QoS 1 backend subscription cannot upgrade a QoS 0 station publish. Stations still need application-level confirmation/retry while disconnected. The node `online`/LWT topics remain node-owned and are not used to drive fulfillment.

## Verify

```sh
go test -race ./...
go vet ./...
mosquitto_sub -h 127.0.0.1 -q 1 -v -t 'canteen/#'
mosquitto_pub -h 127.0.0.1 -q 1 -t canteen/kitchen/done -m 106
```

The broker round-trip test is opt-in. Use a dedicated test broker: it uses the real `canteen/` topics and must never run against a live service.

```sh
MQTT_TEST_BROKER=tcp://127.0.0.1:21883 go test -race ./mqtt -run TestBrokerRoundTrip -count=1
```

Unit tests cover routing, duplicate events, wrong-lane/non-head/cancelled tokens, persisted ready state after reopening the database, parsing, and empty display states. The round-trip test covers real retained publications, station acknowledgements, completion and reconnect republishing.
