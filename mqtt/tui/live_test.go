package main

import (
	"os"
	"testing"
	"time"

	mqtt "github.com/eclipse/paho.mqtt.golang"
)

// LIVE_MQTT=1 go test -run TestLiveBroker ./...
func TestLiveBroker(t *testing.T) {
	if os.Getenv("LIVE_MQTT") == "" {
		t.Skip("set LIVE_MQTT=1 for broker test")
	}
	received := make(chan string, 10)
	opts := mqtt.NewClientOptions()
	opts.AddBroker("tcp://127.0.0.1:1883")
	opts.SetClientID("pahotest")
	opts.SetDefaultPublishHandler(func(_ mqtt.Client, m mqtt.Message) {
		received <- m.Topic() + "=" + string(m.Payload())
	})
	c := mqtt.NewClient(opts)
	if tok := c.Connect(); tok.Wait() && tok.Error() != nil {
		t.Fatal(tok.Error())
	}
	defer c.Disconnect(250)
	if tok := c.Subscribe("canteen/pahotest", 1, nil); tok.Wait() && tok.Error() != nil {
		t.Fatal(tok.Error())
	}
	if tok := c.Publish("canteen/pahotest", 0, false, "hi"); tok.Wait() && tok.Error() != nil {
		t.Fatal(tok.Error())
	}
	select {
	case m := <-received:
		if m != "canteen/pahotest=hi" {
			t.Fatal("wrong message:", m)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no message received")
	}
}
