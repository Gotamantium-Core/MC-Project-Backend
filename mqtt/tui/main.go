// canteen-tui: fake backend + test rig for the canteen ESP32 nodes.
// Owns the three queues, publishes retained heads, advances on real button
// acks (or simulated ones). Commands are single letters followed by Enter:
//
//	1/2/3  enqueue token on counter-a / kitchen / counter-b-ready
//	a/k/b  simulate a button ack for counter-a / kitchen / counter-b
//	q      quit
package main

import (
	"bufio"
	"flag"
	"fmt"
	"log"
	"os"
	"strings"
	"sync"
	"time"

	mqtt "github.com/eclipse/paho.mqtt.golang"
)

// Token is one queue entry. Code is "" on the meals lane.
type Token struct{ ID, Code string }

// advance drops the head when id matches it. False = mismatch, keep queue.
func advance(q []Token, id string) ([]Token, bool) {
	if len(q) == 0 || q[0].ID != id {
		return q, false
	}
	return q[1:], true
}

func fmtA(q []Token) string {
	if len(q) == 0 {
		return "IDLE Q0"
	}
	return fmt.Sprintf("%s Q%d", q[0].ID, len(q)-1)
}

func fmtK(q []Token) string {
	if len(q) == 0 {
		return "IDLE Q0"
	}
	return fmt.Sprintf("%s %s Q%d", q[0].ID, q[0].Code, len(q)-1)
}

func fmtR(q []Token) string {
	if len(q) == 0 {
		return "NONE W0"
	}
	return fmt.Sprintf("%s %s W%d", q[0].ID, q[0].Code, len(q)-1)
}

// rig owns all mutable state. Rule: never block on network while holding mu.
// State reads/writes take the lock briefly; publishes happen outside it.
type rig struct {
	mu     sync.Mutex
	client mqtt.Client
	nextID int
	codes  []string
	codeAt int

	qa, qk, qr []Token
	online     map[string]bool
	log        []string
}

func (r *rig) say(format string, args ...any) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.log = append(r.log, time.Now().Format("15:04:05")+" "+fmt.Sprintf(format, args...))
	if len(r.log) > 12 {
		r.log = r.log[len(r.log)-12:]
	}
}

// heads snapshots the three display payloads.
func (r *rig) heads() (a, k, b string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return fmtA(r.qa), fmtK(r.qk), fmtR(r.qr)
}

// pub publishes one retained head. No locks held: safe from any goroutine.
func (r *rig) pub(topic, payload string) {
	t := r.client.Publish(topic, 1, true, payload)
	t.Wait()
	if t.Error() != nil {
		r.say("PUB %s FAIL %v", topic, t.Error())
		return
	}
	r.say("pub %-24s %s", topic, payload)
}

func (r *rig) publishHeads() {
	a, k, b := r.heads()
	r.pub("canteen/counter-a/now", a)
	r.pub("canteen/kitchen/now", k)
	r.pub("canteen/counter-b/ready", b)
}

func ids(q []Token) string {
	var b strings.Builder
	for i, t := range q {
		if i > 0 {
			b.WriteString(" ")
		}
		b.WriteString(t.ID)
		if t.Code != "" {
			b.WriteString("[" + t.Code + "]")
		}
	}
	if b.Len() == 0 {
		return "-"
	}
	return b.String()
}

func (r *rig) render() {
	r.mu.Lock()
	defer r.mu.Unlock()
	var b strings.Builder
	b.WriteString("\x1b[2J\x1b[H") // clear + home
	b.WriteString("canteen test rig   1/2/3 enqueue   a/k/b ack button   q quit\n\n")
	fmt.Fprintf(&b, "counter-a  now: %-14s queue: %s\n", fmtA(r.qa), ids(r.qa))
	fmt.Fprintf(&b, "kitchen    now: %-14s queue: %s\n", fmtK(r.qk), ids(r.qk))
	fmt.Fprintf(&b, "counter-b  rdy: %-14s queue: %s\n", fmtR(r.qr), ids(r.qr))
	for _, n := range []string{"counter-a", "kitchen", "counter-b"} {
		st := "offline"
		if r.online[n] {
			st = "ONLINE"
		}
		fmt.Fprintf(&b, "  %-10s %s\n", n, st)
	}
	b.WriteString("\n-- log --\n")
	for _, l := range r.log {
		b.WriteString(l + "\n")
	}
	b.WriteString("> ")
	fmt.Print(b.String())
}

// onAck advances a lane. Kitchen acks move the tray to the ready list.
func (r *rig) onAck(lane, id string) {
	r.mu.Lock()
	switch lane {
	case "a":
		if q, ok := advance(r.qa, id); ok {
			r.qa = q
		} else {
			head := fmtA(r.qa)
			r.mu.Unlock()
			r.say("WARN served %s, head is %s", id, head)
			r.render()
			return
		}
	case "k":
		if q, ok := advance(r.qk, id); ok {
			done := r.qk[0]
			r.qk = q
			r.qr = append(r.qr, done)
		} else {
			head := fmtK(r.qk)
			r.mu.Unlock()
			r.say("WARN done %s, head is %s", id, head)
			r.render()
			return
		}
	case "b":
		if q, ok := advance(r.qr, id); ok {
			r.qr = q
		} else {
			head := fmtR(r.qr)
			r.mu.Unlock()
			r.say("WARN collected %s, head is %s", id, head)
			r.render()
			return
		}
	}
	r.mu.Unlock()
	r.say("ack %s on %s", id, lane)
	r.publishHeads()
	r.render()
}

func (r *rig) enqueue(lane string) {
	r.mu.Lock()
	id := fmt.Sprintf("%d", r.nextID)
	r.nextID++
	switch lane {
	case "a":
		r.qa = append(r.qa, Token{ID: id})
	case "k":
		r.qk = append(r.qk, Token{ID: id, Code: r.codes[r.codeAt%len(r.codes)]})
		r.codeAt++
	case "b":
		r.qr = append(r.qr, Token{ID: id, Code: r.codes[r.codeAt%len(r.codes)]})
		r.codeAt++
	}
	r.mu.Unlock()
	r.say("new token %s on %s", id, lane)
	r.publishHeads()
	r.render()
}

// ackHead publishes a button press for the lane head through MQTT so the
// simulated path is identical to hardware.
func (r *rig) ackHead(lane string) {
	r.mu.Lock()
	var topic, id string
	switch lane {
	case "a":
		topic = "canteen/counter-a/served"
		if len(r.qa) > 0 {
			id = r.qa[0].ID
		}
	case "k":
		topic = "canteen/kitchen/done"
		if len(r.qk) > 0 {
			id = r.qk[0].ID
		}
	case "b":
		topic = "canteen/counter-b/collected"
		if len(r.qr) > 0 {
			id = r.qr[0].ID
		}
	}
	r.mu.Unlock()
	if id == "" {
		r.say("nothing to ack on %s", lane)
		r.render()
		return
	}
	t := r.client.Publish(topic, 1, false, id)
	t.Wait()
	if t.Error() != nil {
		r.say("ACK %s FAIL %v", topic, t.Error())
		r.render()
	}
}

// onMessage routes inbound MQTT: acks advance lanes, LWT tracks presence,
// our own retained heads echo back and are ignored. Never blocks on network.
func (r *rig) onMessage(_ mqtt.Client, m mqtt.Message) {
	topic, payload := m.Topic(), strings.TrimSpace(string(m.Payload()))
	switch {
	case topic == "canteen/counter-a/served":
		r.onAck("a", payload)
	case topic == "canteen/kitchen/done":
		r.onAck("k", payload)
	case topic == "canteen/counter-b/collected":
		r.onAck("b", payload)
	case strings.HasSuffix(topic, "/online"):
		parts := strings.Split(topic, "/")
		if len(parts) == 3 {
			online := payload == "1"
			r.mu.Lock()
			r.online[parts[1]] = online
			r.mu.Unlock()
			status := "offline"
			if online {
				status = "online"
			}
			r.say("%s %s", parts[1], status)
			r.render()
		}
	default:
		// our own retained heads echo back; ignore
	}
}

func main() {
	host := flag.String("broker", "127.0.0.1", "MQTT broker host")
	port := flag.Int("port", 1883, "MQTT broker port")
	debug := flag.Bool("debug", false, "paho packet logging to stderr")
	flag.Parse()
	if *debug {
		mqtt.DEBUG = log.New(os.Stderr, "[mqtt] ", 0)
	}

	r := &rig{
		nextID: 105,
		codes:  []string{"POR", "CHK", "SNACK"},
		online: map[string]bool{},
	}

	opts := mqtt.NewClientOptions()
	opts.AddBroker(fmt.Sprintf("tcp://%s:%d", *host, *port))
	opts.SetClientID(fmt.Sprintf("tui-%d", os.Getpid()))
	opts.SetAutoReconnect(true)
	opts.SetOrderMatters(false) // handlers must never stall each other
	opts.SetOnConnectHandler(func(c mqtt.Client) {
		r.say("connected")
		r.render()
	})
	opts.SetDefaultPublishHandler(r.onMessage)
	r.client = mqtt.NewClient(opts)
	if t := r.client.Connect(); t.Wait() && t.Error() != nil {
		fmt.Println("connect:", t.Error())
		os.Exit(1)
	}
	if t := r.client.Subscribe("canteen/#", 1, r.onMessage); t.Wait() && t.Error() != nil {
		fmt.Println("subscribe:", t.Error())
		os.Exit(1)
	}

	// Start drained: nodes show IDLE/NONE until tokens are enqueued.
	// (Also overwrites any stale retained heads from earlier runs.)
	r.publishHeads()
	r.render()

	sc := bufio.NewScanner(os.Stdin)
	for sc.Scan() {
		switch strings.TrimSpace(sc.Text()) {
		case "":
		case "1":
			r.enqueue("a")
		case "2":
			r.enqueue("k")
		case "3":
			r.enqueue("b")
		case "a":
			r.ackHead("a")
		case "k":
			r.ackHead("k")
		case "b":
			r.ackHead("b")
		case "q":
			r.client.Disconnect(250)
			fmt.Println("\nbye")
			return
		}
	}
	r.client.Disconnect(250)
}
