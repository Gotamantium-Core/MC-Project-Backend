package backend

import (
	"context"
	"errors"
	"food-kiosk/database"
	"path/filepath"
	"strconv"
	"testing"
)

func TestFulfillmentLifecycleAndRecovery(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "queue.db")
	db, err := database.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { db.Close() }()
	if err := database.Initialize(db); err != nil {
		t.Fatal(err)
	}
	user, err := database.CreateUser(ctx, db, "TEST001", "Student", "")
	if err != nil {
		t.Fatal(err)
	}
	for _, code := range []string{"MEAL", "POR", "CHK"} {
		if _, err := database.CreateMenuItem(ctx, db, code, code, 0, 1); err != nil {
			t.Fatal(err)
		}
	}
	order := func(codes ...string) int64 {
		t.Helper()
		var lines []database.OrderLine
		for _, code := range codes {
			lines = append(lines, database.OrderLine{Code: code, Quantity: 1})
		}
		id, err := database.CreateOrder(ctx, db, user, lines)
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	meal := order("MEAL")
	special := order("POR")
	mixed := order("MEAL", "CHK")
	s, err := New(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	snapshot := func() []Queue {
		t.Helper()
		q, err := s.Snapshot(ctx)
		if err != nil {
			t.Fatal(err)
		}
		return q
	}
	q := snapshot()
	if len(q[0].Items) != 1 || len(q[1].Items) != 2 || len(q[2].Items) != 0 {
		t.Fatalf("wrong routing: %+v", q)
	}
	ack := func(lane Lane, id int64) {
		t.Helper()
		if err := s.Acknowledge(ctx, lane, formatID(id)); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.Acknowledge(ctx, CounterA, formatID(special)); !errors.Is(err, ErrUnexpectedAck) {
		t.Fatalf("wrong lane accepted: %v", err)
	}
	if err := s.Acknowledge(ctx, Kitchen, formatID(mixed)); !errors.Is(err, ErrUnexpectedAck) {
		t.Fatalf("out of order accepted: %v", err)
	}
	ack(Kitchen, special)
	ack(Kitchen, special)
	o, err := database.GetOrderByID(ctx, db, special)
	if err != nil || o.Status != database.OrderStatusPending {
		t.Fatalf("tray completed too early: %+v %v", o, err)
	}
	// Reopen the actual DB, not only the service: ready state must survive.
	db.Close()
	db, err = database.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	s, err = New(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	q = snapshot()
	if len(q[2].Items) != 1 || q[2].Items[0].ID != special {
		t.Fatalf("ready state lost: %+v", q)
	}
	ack(CounterA, meal)
	ack(CounterA, meal)
	ack(Ready, special)
	ack(Ready, special)
	if err := database.CancelOrder(ctx, db, mixed); err != nil {
		t.Fatal(err)
	}
	if err := s.Acknowledge(ctx, Kitchen, formatID(mixed)); !errors.Is(err, ErrUnexpectedAck) {
		t.Fatalf("cancelled order accepted: %v", err)
	}
	q = snapshot()
	for _, lane := range q {
		if len(lane.Items) != 0 {
			t.Fatalf("stale queue: %+v", q)
		}
	}
}

func TestPayloadAndInvalidAcknowledgements(t *testing.T) {
	cases := []struct {
		q    Queue
		want string
	}{
		{Queue{Lane: CounterA}, "IDLE Q0"}, {Queue{Lane: Kitchen}, "IDLE Q0"}, {Queue{Lane: Ready}, "NONE W0"},
		{Queue{CounterA, []Entry{{105, "MEAL"}, {106, "MEAL"}}}, "105 Q1"},
		{Queue{Kitchen, []Entry{{106, "POR"}}}, "106 POR Q0"},
		{Queue{Ready, []Entry{{106, "POR"}, {107, "CHK"}}}, "106 POR W2"},
	}
	for _, tt := range cases {
		if got := tt.q.Payload(); got != tt.want {
			t.Errorf("got %q want %q", got, tt.want)
		}
	}
	s := &Service{}
	for _, payload := range []string{"", "0", "-1", " 1", "1\n", "+1", "01", "abc", "9223372036854775808"} {
		if err := s.Acknowledge(context.Background(), CounterA, payload); !errors.Is(err, ErrInvalidAck) {
			t.Errorf("payload %q: %v", payload, err)
		}
	}
	if got := displayCode("pôr / test longer"); got != "PRTESTL" {
		t.Fatalf("unsafe display code: %q", got)
	}
}

func formatID(id int64) string { return strconv.FormatInt(id, 10) }
