package server

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"food-kiosk/database"
)

// mealCode is the single menu code that routes to Counter A. Kept as a named
// constant because the whole lane split hinges on it.
const mealCode = "MEAL"

// Credential is a row of the additive credentials table.
type Credential struct {
	ID           int64
	Principal    string
	PasswordHash string
	Role         Role
	UserID       sql.NullInt64
	CreatedAt    string
}

// normalizePrincipal canonicalises a login name the same way users.roll_no is
// canonicalised, so a customer registers and signs in with any casing.
func normalizePrincipal(principal string) (string, error) {
	normalized := strings.ToUpper(strings.TrimSpace(principal))
	if normalized == "" {
		return "", fmt.Errorf("%w: principal is empty", database.ErrInvalidInput)
	}
	return normalized, nil
}

// CreateCredential stores a hashed secret for a principal.
func CreateCredential(ctx context.Context, db database.DB, principal, passwordHash string, role Role, userID *int64) (int64, error) {
	normalized, err := normalizePrincipal(principal)
	if err != nil {
		return 0, err
	}

	var uid any
	if userID != nil {
		uid = *userID
	}

	result, err := db.ExecContext(ctx,
		`INSERT INTO credentials (principal, password_hash, role, user_id) VALUES (?, ?, ?, ?)`,
		normalized, passwordHash, string(role), uid)
	if err != nil {
		return 0, fmt.Errorf("insert credential: %w", err)
	}

	id, err := result.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("get inserted credential ID: %w", err)
	}
	return id, nil
}

// GetCredentialByPrincipal looks a login up by its canonical principal.
func GetCredentialByPrincipal(ctx context.Context, db database.DB, principal string) (*Credential, error) {
	normalized, err := normalizePrincipal(principal)
	if err != nil {
		return nil, err
	}

	var (
		c    Credential
		role string
	)
	err = db.QueryRowContext(ctx,
		`SELECT id, principal, password_hash, role, user_id, created_at FROM credentials WHERE principal = ?`,
		normalized,
	).Scan(&c.ID, &c.Principal, &c.PasswordHash, &role, &c.UserID, &c.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("%w: credentials for %q", database.ErrNotFound, normalized)
	}
	if err != nil {
		return nil, fmt.Errorf("get credential: %w", err)
	}

	c.Role = Role(role)
	return &c, nil
}

// UpsertAdminCredential creates or refreshes an admin login. Idempotent, so it
// is safe to run on every startup: an existing principal has its password
// rotated rather than failing on the UNIQUE constraint.
func UpsertAdminCredential(ctx context.Context, db database.DB, principal, passwordHash string) error {
	normalized, err := normalizePrincipal(principal)
	if err != nil {
		return err
	}

	_, err = db.ExecContext(ctx,
		`INSERT INTO credentials (principal, password_hash, role, user_id) VALUES (?, ?, 'admin', NULL)
		 ON CONFLICT(principal) DO UPDATE SET password_hash = excluded.password_hash, role = 'admin'`,
		normalized, passwordHash)
	if err != nil {
		return fmt.Errorf("upsert admin credential: %w", err)
	}
	return nil
}

// BootstrapAdmin creates or refreshes the admin credential named in the config.
func BootstrapAdmin(ctx context.Context, db database.DB, principal, password string) error {
	hash, err := HashPassword(password)
	if err != nil {
		return err
	}
	return UpsertAdminCredential(ctx, db, principal, hash)
}

// SetOrderReady marks an order as packed by the kitchen and waiting for pickup.
func SetOrderReady(ctx context.Context, db database.DB, orderID int64) error {
	_, err := db.ExecContext(ctx,
		`INSERT INTO ready_orders (order_id) VALUES (?) ON CONFLICT(order_id) DO UPDATE SET ready_at = CURRENT_TIMESTAMP`,
		orderID)
	if err != nil {
		return fmt.Errorf("set order ready: %w", err)
	}
	return nil
}

// ClearOrderReady removes an order from the ready list, e.g. once collected.
func ClearOrderReady(ctx context.Context, db database.DB, orderID int64) error {
	_, err := db.ExecContext(ctx, `DELETE FROM ready_orders WHERE order_id = ?`, orderID)
	if err != nil {
		return fmt.Errorf("clear order ready: %w", err)
	}
	return nil
}

// ListReadyOrderIDs returns the ids of orders packed but not yet collected,
// oldest-ready first. A completed or cancelled order is filtered out even if
// its ready row lingers.
func ListReadyOrderIDs(ctx context.Context, db database.DB) ([]int64, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT r.order_id
		FROM ready_orders r
		JOIN orders o ON o.id = r.order_id
		WHERE o.status = 'pending'
		ORDER BY r.ready_at, r.order_id`)
	if err != nil {
		return nil, fmt.Errorf("list ready orders: %w", err)
	}
	defer rows.Close()

	return scanIDs(rows)
}

// ListPendingOrderIDsByLane returns the ids of pending orders in a lane,
// oldest first ("a" = only MEAL lines, "kitchen" = any non-MEAL line).
//
// Lane is derived by joining order_items to menu.code. That means it reflects
// the current code: renaming a menu code later would re-attribute history. If
// that becomes a problem, snapshot the lane on the order at creation.
func ListPendingOrderIDsByLane(ctx context.Context, db database.DB, lane string) ([]int64, error) {
	var having string
	switch lane {
	case "a":
		having = `HAVING SUM(CASE WHEN m.code = ? THEN 0 ELSE 1 END) = 0`
	case "kitchen":
		having = `HAVING SUM(CASE WHEN m.code = ? THEN 0 ELSE 1 END) > 0`
	default:
		return nil, invalidLane(lane)
	}

	query := `
		SELECT o.id
		FROM orders o
		JOIN order_items oi ON oi.order_id = o.id
		JOIN menu m ON m.id = oi.menu_id
		WHERE o.status = 'pending'
		GROUP BY o.id
		` + having + `
		ORDER BY o.id`

	rows, err := db.QueryContext(ctx, query, mealCode)
	if err != nil {
		return nil, fmt.Errorf("list pending orders by lane: %w", err)
	}
	defer rows.Close()

	return scanIDs(rows)
}

// invalidLane wraps ErrInvalidInput for an unrecognised lane name.
func invalidLane(lane string) error {
	return fmt.Errorf("%w: unknown lane %q", database.ErrInvalidInput, lane)
}

// scanIDs collects a single-column result of order ids.
func scanIDs(rows *sql.Rows) ([]int64, error) {
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("scan order id: %w", err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate order ids: %w", err)
	}
	return ids, nil
}
