package mqtt

import (
	"context"
	"fmt"
	"food-kiosk/backend"
	"food-kiosk/database"
	paho "github.com/eclipse/paho.mqtt.golang"
	"io"
	"log"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// Point MQTT_TEST_BROKER at an isolated broker, never a live canteen broker.
func TestBrokerRoundTrip(t *testing.T) {
	broker := os.Getenv("MQTT_TEST_BROKER")
	if broker == "" {
		t.Skip("set MQTT_TEST_BROKER to an isolated MQTT broker")
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	db, err := database.Open(filepath.Join(t.TempDir(), "integration.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := database.Initialize(db); err != nil {
		t.Fatal(err)
	}
	user, err := database.CreateUser(ctx, db, "INTEGRATION", "Student", "")
	if err != nil {
		t.Fatal(err)
	}
	for _, code := range []string{"MEAL", "POR"} {
		if _, err := database.CreateMenuItem(ctx, db, code, code, 0, 1); err != nil {
			t.Fatal(err)
		}
	}
	order := func(code string) int64 {
		t.Helper()
		id, err := database.CreateOrder(ctx, db, user, []database.OrderLine{{Code: code, Quantity: 1}})
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	meal, special := order("MEAL"), order("POR")
	queues, err := backend.New(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	c, err := New(ctx, Config{Broker: broker, ClientID: "canteen-integration-backend", RefreshInterval: 50 * time.Millisecond}, queues, log.New(io.Discard, "", 0))
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- c.Run() }()
	defer func() {
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Error(err)
			}
		case <-time.After(10 * time.Second):
			t.Error("shutdown timed out")
		}
	}()
	messages := make(chan struct{ topic, payload string }, 100)
	node := paho.NewClient(paho.NewClientOptions().AddBroker(broker).SetClientID("canteen-integration-node").SetOrderMatters(false))
	if err := wait(node.Connect()); err != nil {
		t.Fatal(err)
	}
	defer node.Disconnect(250)
	if err := wait(node.Subscribe("canteen/#", 1, func(_ paho.Client, m paho.Message) {
		messages <- struct{ topic, payload string }{m.Topic(), string(m.Payload())}
	})); err != nil {
		t.Fatal(err)
	}
	await := func(topic, payload string) {
		t.Helper()
		timer := time.NewTimer(10 * time.Second)
		defer timer.Stop()
		for {
			select {
			case m := <-messages:
				if m.topic == topic && m.payload == payload {
					return
				}
			case <-timer.C:
				t.Fatalf("did not receive %s = %s", topic, payload)
			}
		}
	}
	publish := func(topic, payload string) {
		t.Helper()
		if err := wait(node.Publish(topic, 1, false, payload)); err != nil {
			t.Fatal(err)
		}
	}
	await(displays[backend.CounterA], fmt.Sprintf("%d Q0", meal))
	// A subscriber arriving late recovers the kitchen head from retained state.
	await(displays[backend.Kitchen], fmt.Sprintf("%d POR Q0", special))
	publish("canteen/kitchen/done", fmt.Sprint(special))
	await(displays[backend.Ready], fmt.Sprintf("%d POR W1", special))
	o, err := database.GetOrderByID(ctx, db, special)
	if err != nil || o.Status != database.OrderStatusPending {
		t.Fatalf("ready order completed: %+v %v", o, err)
	}
	publish("canteen/kitchen/done", fmt.Sprint(special)) // duplicate must not move it twice
	publish("canteen/counter-b/collected", fmt.Sprint(special))
	await(displays[backend.Ready], "NONE W0")
	publish("canteen/counter-a/served", fmt.Sprint(meal))
	await(displays[backend.CounterA], "IDLE Q0")
	// Force a reconnect: unchanged display messages must be republished.
	c.client.Disconnect(0)
	await(displays[backend.CounterA], "IDLE Q0")
	for _, id := range []int64{meal, special} {
		o, err := database.GetOrderByID(ctx, db, id)
		if err != nil || o.Status != database.OrderStatusCompleted {
			t.Fatalf("handover not completed: %+v %v", o, err)
		}
	}
}
