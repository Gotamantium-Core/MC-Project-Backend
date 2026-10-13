// Package mqtt translates the station protocol into backend calls. Queue and
// payment decisions remain in backend; this package contains no SQL.
package mqtt

import (
	"context"
	"errors"
	"log"
	"sync"
	"time"

	"food-kiosk/backend"
	paho "github.com/eclipse/paho.mqtt.golang"
)

type Queues interface {
	Snapshot(context.Context) ([]backend.Queue, error)
	Acknowledge(context.Context, backend.Lane, string) error
}
type Config struct {
	Broker, ClientID, Username, Password string
	RefreshInterval                      time.Duration
}
type Client struct {
	client   paho.Client
	queues   Queues
	ctx      context.Context
	log      *log.Logger
	mu       sync.Mutex
	last     map[string]string
	refresh  chan struct{}
	interval time.Duration
}

var displays = map[backend.Lane]string{backend.CounterA: "canteen/counter-a/now", backend.Kitchen: "canteen/kitchen/now", backend.Ready: "canteen/counter-b/ready"}
var acknowledgements = map[string]backend.Lane{"canteen/counter-a/served": backend.CounterA, "canteen/kitchen/done": backend.Kitchen, "canteen/counter-b/collected": backend.Ready}

// New constructs a client without dialing. Run owns its connection lifecycle.
func New(ctx context.Context, cfg Config, queues Queues, logger *log.Logger) (*Client, error) {
	if cfg.Broker == "" || cfg.ClientID == "" || queues == nil {
		return nil, errors.New("broker, client ID and queue service are required")
	}
	if logger == nil {
		logger = log.Default()
	}
	if cfg.RefreshInterval <= 0 {
		cfg.RefreshInterval = time.Second
	}
	c := &Client{queues: queues, ctx: ctx, log: logger, last: map[string]string{}, refresh: make(chan struct{}, 1), interval: cfg.RefreshInterval}
	opts := paho.NewClientOptions().AddBroker(cfg.Broker).SetClientID(cfg.ClientID).
		SetUsername(cfg.Username).SetPassword(cfg.Password).SetCleanSession(false).
		SetAutoReconnect(true).SetConnectTimeout(5 * time.Second).SetMaxReconnectInterval(5 * time.Second).
		SetKeepAlive(20 * time.Second).SetPingTimeout(5 * time.Second).SetWriteTimeout(5 * time.Second).
		SetOrderMatters(false).SetAutoAckDisabled(true)
	opts.SetConnectionLostHandler(func(_ paho.Client, err error) { c.log.Printf("MQTT disconnected: %v", err) })
	opts.SetOnConnectHandler(func(pc paho.Client) {
		topics := map[string]byte{}
		for topic := range acknowledgements {
			topics[topic] = 1
		}
		token := pc.SubscribeMultiple(topics, c.receive)
		if err := wait(token); err != nil {
			c.log.Printf("MQTT subscribe: %v", err)
			pc.Disconnect(0)
			return
		}
		c.mu.Lock()
		c.last = map[string]string{}
		c.mu.Unlock()
		c.notify()
	})
	// Also register routes before connecting so queued persistent-session events
	// have a handler even before the OnConnect subscription finishes.
	c.client = paho.NewClient(opts)
	for topic := range acknowledgements {
		c.client.AddRoute(topic, c.receive)
	}
	return c, nil
}
func wait(t paho.Token) error {
	if !t.WaitTimeout(5 * time.Second) {
		return errors.New("MQTT operation timed out")
	}
	return t.Error()
}
func (c *Client) notify() {
	select {
	case c.refresh <- struct{}{}:
	default:
	}
}

// Re-send the current head even for duplicate/rejected button events: a station
// may already have cleared its screen while waiting for backend confirmation.
func (c *Client) invalidate(lane backend.Lane) {
	c.mu.Lock()
	delete(c.last, displays[lane])
	c.mu.Unlock()
	c.notify()
}
func (c *Client) receive(_ paho.Client, m paho.Message) {
	lane, ok := acknowledgements[m.Topic()]
	if !ok || m.Retained() {
		m.Ack()
		return
	}
	err := c.queues.Acknowledge(c.ctx, lane, string(m.Payload()))
	if err != nil {
		c.log.Printf("MQTT acknowledgement %s rejected: %v", m.Topic(), err)
		// Transient database errors remain unacknowledged, allowing broker retries.
		// Invalid input is harmless to retry, but discarded here by refreshing state.
		if errors.Is(err, backend.ErrUnexpectedAck) || errors.Is(err, backend.ErrInvalidAck) {
			m.Ack()
			c.invalidate(lane)
		} else {
			// Reconnect the persistent session to redeliver unacknowledged events.
			go c.client.Disconnect(0)
		}
		return
	}
	m.Ack()
	c.invalidate(lane)
}
func (c *Client) publish() {
	if !c.client.IsConnectionOpen() {
		return
	}
	// Serialize snapshots and publishing to prevent older retained state winning.
	c.mu.Lock()
	defer c.mu.Unlock()
	queues, err := c.queues.Snapshot(c.ctx)
	if err != nil {
		c.log.Printf("MQTT snapshot: %v", err)
		return
	}
	for _, q := range queues {
		topic := displays[q.Lane]
		payload := q.Payload()
		if c.last[topic] == payload {
			continue
		}
		if err := wait(c.client.Publish(topic, 1, true, payload)); err != nil {
			c.log.Printf("MQTT publish %s: %v", topic, err)
			continue
		}
		c.last[topic] = payload
	}
}

// Run retries initial broker failures and republishes all displays after every
// reconnect. Periodic reconciliation detects orders/cancellations from callers
// of the existing database package without coupling them to MQTT.
func (c *Client) Run() error {
	ticker := time.NewTicker(c.interval)
	defer ticker.Stop()
	defer c.client.Disconnect(250)
	for {
		if c.ctx.Err() != nil {
			return nil
		}
		if !c.client.IsConnected() && !c.client.IsConnectionOpen() {
			if err := wait(c.client.Connect()); err != nil {
				c.log.Printf("MQTT connect: %v", err)
			}
		}
		c.publish()
		select {
		case <-c.ctx.Done():
			return nil
		case <-ticker.C:
		case <-c.refresh:
		}
	}
}
