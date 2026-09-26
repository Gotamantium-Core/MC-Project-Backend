package database

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"strings"
)

// OrderStatus is the lifecycle state of an order.
//
//	pending   -> paid, waiting to be handed over
//	completed -> handed over to the customer
//	cancelled -> voided, and the payment refunded if one was taken
type OrderStatus string

const (
	OrderStatusPending   OrderStatus = "pending"
	OrderStatusCompleted OrderStatus = "completed"
	OrderStatusCancelled OrderStatus = "cancelled"
)

// TransactionType is the direction of a movement on a user's prepaid balance.
type TransactionType string

const (
	// TransactionTypeCredit adds to the balance: a top-up or a refund.
	TransactionTypeCredit TransactionType = "credit"
	// TransactionTypeDebit takes from the balance: paying for an order.
	TransactionTypeDebit TransactionType = "debit"
)

// Guard rails on order input, so a malformed or hostile request cannot produce
// an absurd order or overflow an int64 total.
const (
	maxLinesPerOrder   = 50
	maxQuantityPerLine = 100
)

type Order struct {
	ID          int64
	UserID      int64
	Status      OrderStatus
	TotalPaise  int64
	CreatedAt   string
	CompletedAt *string
	// Items is populated by GetOrderByID only. The list helpers leave it nil to
	// avoid an N+1 query; call GetOrderByID for the full detail view.
	Items []OrderItem
}

type OrderItem struct {
	ID             int64
	OrderID        int64
	MenuID         int64
	ItemName       string
	UnitPricePaise int64
	Quantity       int64
	LineTotalPaise int64
}

type Transaction struct {
	ID              int64
	UserID          int64
	OrderID         *int64
	AmountPaise     int64
	TransactionType TransactionType
	CreatedAt       string
}

// OrderLine is one requested cart entry, identified by the menu item's code.
type OrderLine struct {
	Code     string
	Quantity int64
}

const orderColumns = `id, user_id, status, total_paise, created_at, completed_at`

func scanOrder(s rowScanner) (*Order, error) {
	var order Order
	if err := s.Scan(
		&order.ID,
		&order.UserID,
		&order.Status,
		&order.TotalPaise,
		&order.CreatedAt,
		&order.CompletedAt,
	); err != nil {
		return nil, err
	}
	return &order, nil
}

func scanOrderItem(s rowScanner) (*OrderItem, error) {
	var item OrderItem
	if err := s.Scan(
		&item.ID,
		&item.OrderID,
		&item.MenuID,
		&item.ItemName,
		&item.UnitPricePaise,
		&item.Quantity,
		&item.LineTotalPaise,
	); err != nil {
		return nil, err
	}
	return &item, nil
}

func scanTransaction(s rowScanner) (*Transaction, error) {
	var txn Transaction
	if err := s.Scan(
		&txn.ID,
		&txn.UserID,
		&txn.OrderID,
		&txn.AmountPaise,
		&txn.TransactionType,
		&txn.CreatedAt,
	); err != nil {
		return nil, err
	}
	return &txn, nil
}

func isValidStatus(status OrderStatus) bool {
	switch status {
	case OrderStatusPending, OrderStatusCompleted, OrderStatusCancelled:
		return true
	default:
		return false
	}
}

// canTransition encodes the order lifecycle. cancelled is terminal.
func canTransition(from, to OrderStatus) bool {
	switch from {
	case OrderStatusPending:
		return to == OrderStatusCompleted || to == OrderStatusCancelled
	case OrderStatusCompleted:
		// Handed over, then returned and refunded.
		return to == OrderStatusCancelled
	default:
		return false
	}
}

// normalizeLines validates the cart and merges duplicate codes by summing their
// quantities, so tapping the same item twice yields quantity 2 of one line
// rather than two conflicting lines for the same menu item.
func normalizeLines(lines []OrderLine) (map[string]int64, error) {
	if len(lines) == 0 {
		return nil, fmt.Errorf("%w: order has no lines", ErrInvalidInput)
	}
	if len(lines) > maxLinesPerOrder {
		return nil, fmt.Errorf("%w: order has %d lines, maximum is %d", ErrInvalidInput, len(lines), maxLinesPerOrder)
	}

	quantities := make(map[string]int64, len(lines))
	for _, line := range lines {
		code := strings.TrimSpace(line.Code)
		if code == "" {
			return nil, fmt.Errorf("%w: line has an empty code", ErrInvalidInput)
		}
		if line.Quantity <= 0 {
			return nil, fmt.Errorf("%w: quantity %d for code %q must be positive", ErrInvalidInput, line.Quantity, code)
		}
		if line.Quantity > maxQuantityPerLine {
			return nil, fmt.Errorf("%w: quantity %d for code %q exceeds maximum %d", ErrInvalidInput, line.Quantity, code, maxQuantityPerLine)
		}

		sum, err := checkedAdd(quantities[code], line.Quantity)
		if err != nil {
			return nil, fmt.Errorf("%w: total quantity for code %q overflows", ErrInvalidInput, code)
		}
		if sum > maxQuantityPerLine {
			return nil, fmt.Errorf("%w: combined quantity %d for code %q exceeds maximum %d", ErrInvalidInput, sum, code, maxQuantityPerLine)
		}
		quantities[code] = sum
	}

	return quantities, nil
}

func checkedMul(a, b int64) (int64, error) {
	if a != 0 && b > math.MaxInt64/a {
		return 0, fmt.Errorf("multiply overflows int64: %d * %d", a, b)
	}
	return a * b, nil
}

func checkedAdd(a, b int64) (int64, error) {
	if b > 0 && a > math.MaxInt64-b {
		return 0, fmt.Errorf("add overflows int64: %d + %d", a, b)
	}
	if b < 0 && a < math.MinInt64-b {
		return 0, fmt.Errorf("add overflows int64: %d + %d", a, b)
	}
	return a + b, nil
}

// CreateOrder prices a cart and records it as a single atomic unit: the order
// row, one row per line, and the debit against the user's prepaid balance are
// either all committed or all rolled back.
//
// Menu names and prices are snapshotted onto the order items, so editing a menu
// entry later never rewrites what a customer was charged.
//
// Returns ErrNotFound if the user or any code is unknown, ErrInsufficientBalance
// if the balance will not cover the total, and ErrInvalidInput for a malformed
// cart. In every error case the database is left untouched.
func CreateOrder(ctx context.Context, db *sql.DB, userID int64, lines []OrderLine) (int64, error) {
	quantities, err := normalizeLines(lines)
	if err != nil {
		return 0, err
	}

	codes := make([]string, 0, len(quantities))
	for code := range quantities {
		codes = append(codes, code)
	}

	var orderID int64
	err = withTx(ctx, db, func(tx *sql.Tx) error {
		// Turn a missing user into a clean 404 instead of a raw FK violation.
		if _, err := GetUserByID(ctx, tx, userID); err != nil {
			return err
		}

		// Resolve the whole cart in one query, inside the transaction, so the
		// prices used are the ones committed alongside the order.
		menuItems, err := GetMenuItemsByCodes(ctx, tx, codes)
		if err != nil {
			return err
		}

		type pricedLine struct {
			item     Menu
			quantity int64
			total    int64
		}
		priced := make([]pricedLine, 0, len(codes))
		var orderTotal int64
		for _, code := range codes {
			item := menuItems[code]
			qty := quantities[code]

			lineTotal, err := checkedMul(item.PricePaise, qty)
			if err != nil {
				return fmt.Errorf("%w: line total for code %q overflows", ErrInvalidInput, code)
			}
			orderTotal, err = checkedAdd(orderTotal, lineTotal)
			if err != nil {
				return fmt.Errorf("%w: order total overflows", ErrInvalidInput)
			}
			priced = append(priced, pricedLine{item: item, quantity: qty, total: lineTotal})
		}

		result, err := tx.ExecContext(ctx, `INSERT INTO orders (user_id, status, total_paise) VALUES (?, ?, ?)`, userID, string(OrderStatusPending), orderTotal)
		if err != nil {
			return fmt.Errorf("insert order: %w", err)
		}
		if orderID, err = result.LastInsertId(); err != nil {
			return fmt.Errorf("get inserted order ID: %w", err)
		}

		for _, line := range priced {
			_, err := tx.ExecContext(ctx, `INSERT INTO order_items (order_id, menu_id, item_name, unit_price_paise, quantity, line_total_paise) VALUES (?, ?, ?, ?, ?, ?)`,
				orderID, line.item.ID, line.item.ItemName, line.item.PricePaise, line.quantity, line.total)
			if err != nil {
				return fmt.Errorf("insert order item %q: %w", line.item.Code, err)
			}
		}

		balance, err := GetUserBalancePaise(ctx, tx, userID)
		if err != nil {
			return err
		}
		if balance < orderTotal {
			return fmt.Errorf("%w: balance %d paise, order total %d paise", ErrInsufficientBalance, balance, orderTotal)
		}

		// A cart of entirely free items totals zero, and the ledger rejects
		// zero-value rows, so record no movement rather than failing the order.
		if orderTotal > 0 {
			if _, err := insertTransaction(ctx, tx, userID, &orderID, orderTotal, TransactionTypeDebit); err != nil {
				return err
			}
		}

		return nil
	})
	if err != nil {
		return 0, err
	}

	return orderID, nil
}

// GetOrderByID returns a single order with its items attached.
func GetOrderByID(ctx context.Context, db DB, id int64) (*Order, error) {
	order, err := scanOrder(db.QueryRowContext(ctx, `SELECT `+orderColumns+` FROM orders WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("%w: order ID %d", ErrNotFound, id)
	}
	if err != nil {
		return nil, fmt.Errorf("get order by ID: %w", err)
	}

	items, err := ListOrderItems(ctx, db, id)
	if err != nil {
		return nil, err
	}
	order.Items = items

	// Defend against a total that disagrees with its own lines, which would
	// otherwise silently misreport what was charged.
	var summed int64
	for _, item := range items {
		var err error
		if summed, err = checkedAdd(summed, item.LineTotalPaise); err != nil {
			return nil, fmt.Errorf("order %d line totals overflow", id)
		}
	}
	if summed != order.TotalPaise {
		return nil, fmt.Errorf("order %d total %d paise does not match line sum %d paise", id, order.TotalPaise, summed)
	}

	return order, nil
}

// ListOrderItems returns the lines of an order, ordered by id.
func ListOrderItems(ctx context.Context, db DB, orderID int64) ([]OrderItem, error) {
	rows, err := db.QueryContext(ctx, `SELECT id, order_id, menu_id, item_name, unit_price_paise, quantity, line_total_paise FROM order_items WHERE order_id = ? ORDER BY id`, orderID)
	if err != nil {
		return nil, fmt.Errorf("list order items: %w", err)
	}
	defer rows.Close()

	var items []OrderItem
	for rows.Next() {
		item, err := scanOrderItem(rows)
		if err != nil {
			return nil, fmt.Errorf("scan order item: %w", err)
		}
		items = append(items, *item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate order items: %w", err)
	}

	return items, nil
}

// ListOrdersByUser returns a user's order history, newest first. Items are not
// attached; use GetOrderByID for detail.
func ListOrdersByUser(ctx context.Context, db DB, userID int64) ([]Order, error) {
	return queryOrders(ctx, db, `SELECT `+orderColumns+` FROM orders WHERE user_id = ? ORDER BY id DESC`, userID)
}

// ListOrdersByStatus returns orders in a given state, oldest first, which is the
// order the kiosk should serve next.
func ListOrdersByStatus(ctx context.Context, db DB, status OrderStatus) ([]Order, error) {
	if !isValidStatus(status) {
		return nil, fmt.Errorf("%w: unknown order status %q", ErrInvalidInput, status)
	}
	return queryOrders(ctx, db, `SELECT `+orderColumns+` FROM orders WHERE status = ? ORDER BY id`, string(status))
}

func queryOrders(ctx context.Context, db DB, query string, args ...any) ([]Order, error) {
	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list orders: %w", err)
	}
	defer rows.Close()

	var orders []Order
	for rows.Next() {
		order, err := scanOrder(rows)
		if err != nil {
			return nil, fmt.Errorf("scan order: %w", err)
		}
		orders = append(orders, *order)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate orders: %w", err)
	}

	return orders, nil
}

// CompleteOrder marks a paid order as handed over and stamps completed_at.
// Only a pending order can be completed.
func CompleteOrder(ctx context.Context, db DB, orderID int64) error {
	return transitionOrder(ctx, db, orderID, OrderStatusCompleted)
}

// CancelOrder voids an order. If the order had been paid, the amount is refunded
// to the user's balance in the same transaction, so the two can never disagree.
// Cancelling an already cancelled order returns ErrConflict.
func CancelOrder(ctx context.Context, db DB, orderID int64) error {
	return transitionOrder(ctx, db, orderID, OrderStatusCancelled)
}

func transitionOrder(ctx context.Context, db DB, orderID int64, to OrderStatus) error {
	if db == nil {
		return fmt.Errorf("%w: nil database handle", ErrInvalidInput)
	}

	// The refund has to be written in the same transaction as the status change,
	// so this needs the concrete *sql.DB rather than the read-only DB interface.
	pool, ok := db.(*sql.DB)
	if !ok {
		return fmt.Errorf("transition order: requires a *sql.DB handle, got %T", db)
	}

	return withTx(ctx, pool, func(tx *sql.Tx) error {
		order, err := scanOrder(tx.QueryRowContext(ctx, `SELECT `+orderColumns+` FROM orders WHERE id = ?`, orderID))
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("%w: order ID %d", ErrNotFound, orderID)
		}
		if err != nil {
			return fmt.Errorf("get order for transition: %w", err)
		}

		if order.Status == to {
			return fmt.Errorf("%w: order %d is already %s", ErrConflict, orderID, to)
		}
		if !canTransition(order.Status, to) {
			return fmt.Errorf("%w: cannot go from %s to %s for order %d", ErrConflict, order.Status, to, orderID)
		}

		if to == OrderStatusCancelled {
			// Refund only if this order actually took money. createOrder always
			// debits, so normally this is true; the check keeps a hand-written or
			// legacy order from minting credit.
			debited, err := orderDebitPaise(ctx, tx, orderID)
			if err != nil {
				return err
			}
			if debited > 0 {
				if _, err := insertTransaction(ctx, tx, order.UserID, &orderID, debited, TransactionTypeCredit); err != nil {
					return err
				}
			}
		}

		// CURRENT_TIMESTAMP keeps completed_at in the same UTC format as the
		// created_at default, so both are directly comparable.
		_, err = tx.ExecContext(ctx, `UPDATE orders SET status = ?, completed_at = CURRENT_TIMESTAMP WHERE id = ?`, string(to), orderID)
		if err != nil {
			return fmt.Errorf("update order status: %w", err)
		}

		return nil
	})
}

// orderDebitPaise returns the amount debited for an order, or 0 if it was never
// charged.
func orderDebitPaise(ctx context.Context, db DB, orderID int64) (int64, error) {
	var total sql.NullInt64
	err := db.QueryRowContext(ctx, `SELECT COALESCE(SUM(amount_paise), 0) FROM transactions WHERE order_id = ? AND transaction_type = ?`, orderID, string(TransactionTypeDebit)).Scan(&total)
	if err != nil {
		return 0, fmt.Errorf("sum order debits: %w", err)
	}
	return total.Int64, nil
}

// insertTransaction writes one ledger row. amountPaise must be positive; the
// transaction_type carries the direction.
func insertTransaction(ctx context.Context, db DB, userID int64, orderID *int64, amountPaise int64, txnType TransactionType) (int64, error) {
	if amountPaise <= 0 {
		return 0, fmt.Errorf("%w: transaction amount %d paise must be positive", ErrInvalidInput, amountPaise)
	}
	if txnType != TransactionTypeCredit && txnType != TransactionTypeDebit {
		return 0, fmt.Errorf("%w: unknown transaction type %q", ErrInvalidInput, txnType)
	}

	result, err := db.ExecContext(ctx, `INSERT INTO transactions (user_id, order_id, amount_paise, transaction_type) VALUES (?, ?, ?, ?)`, userID, orderID, amountPaise, string(txnType))
	if err != nil {
		return 0, fmt.Errorf("insert transaction: %w", err)
	}

	id, err := result.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("get inserted transaction ID: %w", err)
	}

	return id, nil
}

// TopUpUser credits a user's prepaid balance and returns the new transaction ID.
// This is the only way a balance increases other than a refund.
func TopUpUser(ctx context.Context, db DB, userID int64, amountPaise int64) (int64, error) {
	if _, err := GetUserByID(ctx, db, userID); err != nil {
		return 0, err
	}
	return insertTransaction(ctx, db, userID, nil, amountPaise, TransactionTypeCredit)
}

// GetUserBalancePaise returns the prepaid balance: total credits minus total
// debits. It may be negative if an operator overrode the ledger, which callers
// should treat as "no credit available".
func GetUserBalancePaise(ctx context.Context, db DB, userID int64) (int64, error) {
	var credits, debits sql.NullInt64
	err := db.QueryRowContext(ctx, `
		SELECT
			COALESCE(SUM(CASE WHEN transaction_type = ? THEN amount_paise ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN transaction_type = ? THEN amount_paise ELSE 0 END), 0)
		FROM transactions WHERE user_id = ?`,
		string(TransactionTypeCredit), string(TransactionTypeDebit), userID,
	).Scan(&credits, &debits)
	if err != nil {
		return 0, fmt.Errorf("get user balance: %w", err)
	}

	balance, err := checkedAdd(credits.Int64, -debits.Int64)
	if err != nil {
		return 0, fmt.Errorf("user %d balance overflows int64", userID)
	}

	return balance, nil
}

// ListTransactionsByUser returns a user's ledger, newest first.
func ListTransactionsByUser(ctx context.Context, db DB, userID int64) ([]Transaction, error) {
	rows, err := db.QueryContext(ctx, `SELECT id, user_id, order_id, amount_paise, transaction_type, created_at FROM transactions WHERE user_id = ? ORDER BY id DESC`, userID)
	if err != nil {
		return nil, fmt.Errorf("list transactions: %w", err)
	}
	defer rows.Close()

	var txns []Transaction
	for rows.Next() {
		txn, err := scanTransaction(rows)
		if err != nil {
			return nil, fmt.Errorf("scan transaction: %w", err)
		}
		txns = append(txns, *txn)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate transactions: %w", err)
	}

	return txns, nil
}
