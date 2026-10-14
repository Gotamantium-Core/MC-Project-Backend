package main

import (
	"os"
	"sync"
	"testing"
	"time"

	mqtt "github.com/eclipse/paho.mqtt.golang"
)

// LIVE_MQTT=1 go test -run TestLiveRigSetup ./...
func TestLiveRigSetup(t *testing.T) {
	if os.Getenv("LIVE_MQTT") == "" {
		t.Skip("set LIVE_MQTT=1 for broker test")
	}
	var mu sync.Mutex
	received := make(chan string, 10)
	onMsg := func(_ mqtt.Client, m mqtt.Message) {
		mu.Lock()
		defer mu.Unlock()
		received <- m.Topic() + "=" + string(m.Payload())
	}
	opts := mqtt.NewClientOptions()
	opts.AddBroker("tcp://127.0.0.1:1883")
	opts.SetClientID("rigrepro")
	opts.SetAutoReconnect(true)
	opts.SetOnConnectHandler(func(c mqtt.Client) {
		mu.Lock()
		defer mu.Unlock()
	})
	opts.SetDefaultPublishHandler(onMsg)
	c := mqtt.NewClient(opts)
	if tok := c.Connect(); tok.Wait() && tok.Error() != nil {
		t.Fatal(tok.Error())
	}
	defer c.Disconnect(250)
	if tok := c.Subscribe("canteen/#", 1, onMsg); tok.Wait() && tok.Error() != nil {
		t.Fatal(tok.Error())
	}
	if tok := c.Publish("canteen/rigprobesetup", 0, false, "hi"); tok.Wait() && tok.Error() != nil {
		t.Fatal(tok.Error())
	}
	deadline := time.After(5 * time.Second)
	for {
		select {
		case m := <-received:
			if m == "canteen/rigprobesetup=hi" {
				return // pass
			}
			// retained backlog from earlier seeds; keep waiting
		case <-deadline:
			t.Fatal("probe never arrived")
		}
	}
}
