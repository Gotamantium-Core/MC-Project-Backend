// Package backend owns fulfillment state. It has no dependency on MQTT.
package backend

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"

	"food-kiosk/database"
)

type Lane string

const (
	CounterA Lane = "a"
	Kitchen  Lane = "kitchen"
	Ready    Lane = "ready"
)

var ErrInvalidAck = errors.New("invalid acknowledgement")
var ErrUnexpectedAck = errors.New("acknowledgement does not match the current queue head")

type Entry struct {
	ID   int64
	Code string
}
type Queue struct {
	Lane  Lane
	Items []Entry
}

// Depth excludes the displayed head in service queues; ready depth includes it.
func (q Queue) Payload() string {
	if len(q.Items) == 0 {
		if q.Lane == Ready {
			return "NONE W0"
		}
		return "IDLE Q0"
	}
	h := q.Items[0]
	if q.Lane == CounterA {
		return fmt.Sprintf("%d Q%d", h.ID, len(q.Items)-1)
	}
	if q.Lane == Ready {
		return fmt.Sprintf("%d %s W%d", h.ID, h.Code, len(q.Items))
	}
	return fmt.Sprintf("%d %s Q%d", h.ID, h.Code, len(q.Items)-1)
}

type Service struct {
	db *sql.DB
	mu sync.Mutex
}

func New(ctx context.Context, db *sql.DB) (*Service, error) {
	// Additive schema: preserves existing database helpers and schema version.
	_, err := db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS fulfillment (
 order_id INTEGER PRIMARY KEY REFERENCES orders(id) ON DELETE CASCADE,
 lane TEXT NOT NULL CHECK(lane IN ('a','kitchen','ready')),
 code TEXT NOT NULL,
 ready_sequence INTEGER UNIQUE
 )`)
	if err != nil {
		return nil, fmt.Errorf("initialize fulfillment: %w", err)
	}
	return &Service{db: db}, nil
}

func displayCode(code string) string {
	var b strings.Builder
	for _, r := range strings.ToUpper(code) {
		if (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_' || r == '-' {
			b.WriteRune(r)
			if b.Len() == 7 {
				break
			}
		}
	}
	if b.Len() == 0 {
		return "SPECIAL"
	}
	return b.String()
}

// reconcile recovers orders committed before a crash or created by existing
// database callers. Classification is frozen once recorded, including its code.
func (s *Service) reconcile(ctx context.Context) error {
	rows, err := s.db.QueryContext(ctx, `SELECT o.id, COALESCE((SELECT m.code FROM order_items i JOIN menu m ON m.id=i.menu_id WHERE i.order_id=o.id AND m.code <> 'MEAL' ORDER BY i.id LIMIT 1),'MEAL') FROM orders o LEFT JOIN fulfillment f ON f.order_id=o.id WHERE o.status='pending' AND f.order_id IS NULL ORDER BY o.id`)
	if err != nil {
		return err
	}
	var missing []Entry
	for rows.Next() {
		var e Entry
		if err := rows.Scan(&e.ID, &e.Code); err != nil {
			rows.Close()
			return err
		}
		missing = append(missing, e)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, e := range missing {
		lane := Kitchen
		if e.Code == "MEAL" {
			lane = CounterA
		}
		if _, err := s.db.ExecContext(ctx, `INSERT OR IGNORE INTO fulfillment(order_id,lane,code) VALUES(?,?,?)`, e.ID, lane, displayCode(e.Code)); err != nil {
			return err
		}
	}
	return nil
}
func (s *Service) queue(ctx context.Context, lane Lane) (Queue, error) {
	q := Queue{Lane: lane, Items: []Entry{}}
	if lane != CounterA && lane != Kitchen && lane != Ready {
		return q, fmt.Errorf("invalid lane %q", lane)
	}
	rows, err := s.db.QueryContext(ctx, `SELECT f.order_id,f.code FROM fulfillment f JOIN orders o ON o.id=f.order_id WHERE o.status='pending' AND f.lane=? ORDER BY COALESCE(f.ready_sequence,f.order_id),f.order_id`, lane)
	if err != nil {
		return q, err
	}
	defer rows.Close()
	for rows.Next() {
		var e Entry
		if err := rows.Scan(&e.ID, &e.Code); err != nil {
			return q, err
		}
		q.Items = append(q.Items, e)
	}
	return q, rows.Err()
}
func (s *Service) Snapshot(ctx context.Context) ([]Queue, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.reconcile(ctx); err != nil {
		return nil, err
	}
	var result []Queue
	for _, lane := range []Lane{CounterA, Kitchen, Ready} {
		q, err := s.queue(ctx, lane)
		if err != nil {
			return nil, err
		}
		result = append(result, q)
	}
	return result, nil
}

// Acknowledge rejects malformed, retained, wrong-lane and out-of-order events.
// Duplicates are accepted only if the corresponding transition already happened.
func (s *Service) Acknowledge(ctx context.Context, lane Lane, payload string) error {
	id, err := strconv.ParseInt(payload, 10, 64)
	if err != nil || id <= 0 || strconv.FormatInt(id, 10) != payload {
		return fmt.Errorf("%w: token %q", ErrInvalidAck, payload)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.reconcile(ctx); err != nil {
		return err
	}
	var storedLane Lane
	var status database.OrderStatus
	err = s.db.QueryRowContext(ctx, `SELECT f.lane,o.status FROM fulfillment f JOIN orders o ON o.id=f.order_id WHERE o.id=?`, id).Scan(&storedLane, &status)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrUnexpectedAck
	}
	if err != nil {
		return fmt.Errorf("lookup acknowledged token: %w", err)
	}
	if status == database.OrderStatusCancelled {
		return ErrUnexpectedAck
	}
	if lane == Kitchen && storedLane == Ready {
		return nil
	}
	if status == database.OrderStatusCompleted && storedLane == lane && (lane == CounterA || lane == Ready) {
		return nil
	}
	q, err := s.queue(ctx, lane)
	if err != nil {
		return err
	}
	if storedLane != lane || len(q.Items) == 0 || q.Items[0].ID != id {
		return ErrUnexpectedAck
	}
	if lane == Kitchen {
		_, err = s.db.ExecContext(ctx, `UPDATE fulfillment SET lane='ready',ready_sequence=(SELECT COALESCE(MAX(ready_sequence),0)+1 FROM fulfillment) WHERE order_id=? AND lane='kitchen' AND EXISTS(SELECT 1 FROM orders WHERE id=? AND status='pending')`, id, id)
		return err
	}
	err = database.CompleteOrder(ctx, s.db, id)
	if errors.Is(err, database.ErrConflict) {
		return ErrUnexpectedAck
	}
	return err
}
