package mqtt

import (
	"context"
	"food-kiosk/backend"
	"io"
	"log"
	"testing"
)

type fakeQueues struct {
	calls   int
	lane    backend.Lane
	payload string
	err     error
}

func (f *fakeQueues) Snapshot(context.Context) ([]backend.Queue, error) { return nil, nil }
func (f *fakeQueues) Acknowledge(_ context.Context, lane backend.Lane, payload string) error {
	f.calls++
	f.lane = lane
	f.payload = payload
	return f.err
}

type message struct {
	topic, payload  string
	retained, acked bool
}

func (m *message) Duplicate() bool   { return false }
func (m *message) Qos() byte         { return 1 }
func (m *message) Retained() bool    { return m.retained }
func (m *message) Topic() string     { return m.topic }
func (m *message) MessageID() uint16 { return 1 }
func (m *message) Payload() []byte   { return []byte(m.payload) }
func (m *message) Ack()              { m.acked = true }
func TestAcknowledgementRouting(t *testing.T) {
	for topic, lane := range acknowledgements {
		f := &fakeQueues{}
		c := &Client{queues: f, ctx: context.Background(), log: log.New(io.Discard, "", 0), refresh: make(chan struct{}, 1)}
		m := &message{topic: topic, payload: "106"}
		c.receive(nil, m)
		if !m.acked || f.calls != 1 || f.lane != lane || f.payload != "106" {
			t.Fatalf("wrong dispatch: %+v %+v", m, f)
		}
		retained := &message{topic: topic, payload: "106", retained: true}
		c.receive(nil, retained)
		if f.calls != 1 || !retained.acked {
			t.Fatal("retained ack was applied")
		}
		f.err = backend.ErrUnexpectedAck
		m.acked = false
		c.receive(nil, m)
		if !m.acked {
			t.Fatal("invalid transition not discarded")
		}
		f.err = backend.ErrInvalidAck
		m.acked = false
		c.receive(nil, m)
		if !m.acked {
			t.Fatal("malformed payload not discarded")
		}
	}
}
