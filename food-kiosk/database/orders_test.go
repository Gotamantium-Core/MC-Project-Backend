package database

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// seedUser creates a user with the given prepaid credit.
func seedUser(t *testing.T, db *sql.DB, rollNo string, creditPaise int64) int64 {
	t.Helper()
	ctx := context.Background()

	id, err := CreateUser(ctx, db, rollNo, "Test User", "")
	if err != nil {
		t.Fatalf("CreateUser() error = %v", err)
	}
	if creditPaise > 0 {
		if _, err := TopUpUser(ctx, db, id, creditPaise); err != nil {
			t.Fatalf("TopUpUser() error = %v", err)
		}
	}
	return id
}

// seedMenu creates a menu item and returns its ID.
func seedMenu(t *testing.T, db *sql.DB, name, code string, pricePaise int64) int64 {
	t.Helper()

	id, err := CreateMenuItem(context.Background(), db, name, code, pricePaise, 1)
	if err != nil {
		t.Fatalf("CreateMenuItem(%q) error = %v", code, err)
	}
	return id
}

func TestCreateOrder(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)

	chaiID := seedMenu(t, db, "Masala Chai", "CHAI", 2000)
	vadaID := seedMenu(t, db, "Vada", "VADA", 3000)
	userID := seedUser(t, db, "TVE24CS100", 10000)

	orderID, err := CreateOrder(ctx, db, userID, []OrderLine{
		{Code: "CHAI", Quantity: 2},
		{Code: "VADA", Quantity: 1},
	})
	if err != nil {
		t.Fatalf("CreateOrder() error = %v", err)
	}

	order, err := GetOrderByID(ctx, db, orderID)
	if err != nil {
		t.Fatalf("GetOrderByID() error = %v", err)
	}
	if order.UserID != userID {
		t.Fatalf("GetOrderByID() userID = %d, want %d", order.UserID, userID)
	}
	if order.Status != OrderStatusPending {
		t.Fatalf("GetOrderByID() status = %q, want %q", order.Status, OrderStatusPending)
	}
	// 2 * 2000 + 1 * 3000
	if order.TotalPaise != 7000 {
		t.Fatalf("GetOrderByID() total = %d, want 7000", order.TotalPaise)
	}
	if order.CompletedAt != nil {
		t.Fatalf("GetOrderByID() completedAt = %v, want nil", *order.CompletedAt)
	}
	if len(order.Items) != 2 {
		t.Fatalf("GetOrderByID() items length = %d, want 2", len(order.Items))
	}

	byCode := map[string]OrderItem{}
	for _, item := range order.Items {
		byCode[item.ItemName] = item
	}
	if item := byCode["Masala Chai"]; item.MenuID != chaiID || item.Quantity != 2 || item.LineTotalPaise != 4000 {
		t.Fatalf("chai line = %+v", item)
	}
	if item := byCode["Vada"]; item.MenuID != vadaID || item.Quantity != 1 || item.LineTotalPaise != 3000 {
		t.Fatalf("vada line = %+v", item)
	}

	// The debit must have landed exactly once.
	balance, err := GetUserBalancePaise(ctx, db, userID)
	if err != nil {
		t.Fatalf("GetUserBalancePaise() error = %v", err)
	}
	if balance != 3000 {
		t.Fatalf("GetUserBalancePaise() = %d, want 3000", balance)
	}
}

func TestCreateOrderSnapshotsMenuPrices(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)

	menuID := seedMenu(t, db, "Masala Chai", "CHAI", 2000)
	userID := seedUser(t, db, "TVE24CS101", 10000)

	orderID, err := CreateOrder(ctx, db, userID, []OrderLine{{Code: "CHAI", Quantity: 1}})
	if err != nil {
		t.Fatalf("CreateOrder() error = %v", err)
	}

	// Reprice and rename the menu item after the order was placed.
	err = UpdateMenuItem(ctx, db, Menu{ID: menuID, ItemName: "Masala Chai (Large)", Code: "CHAI", PricePaise: 5000, IsAvailable: 1})
	if err != nil {
		t.Fatalf("UpdateMenuItem() error = %v", err)
	}

	order, err := GetOrderByID(ctx, db, orderID)
	if err != nil {
		t.Fatalf("GetOrderByID() error = %v", err)
	}
	if len(order.Items) != 1 {
		t.Fatalf("GetOrderByID() items length = %d, want 1", len(order.Items))
	}
	// The historical order must still show what was actually charged.
	if order.Items[0].ItemName != "Masala Chai" {
		t.Fatalf("order item name = %q, want %q", order.Items[0].ItemName, "Masala Chai")
	}
	if order.Items[0].UnitPricePaise != 2000 {
		t.Fatalf("order item price = %d, want 2000", order.Items[0].UnitPricePaise)
	}
	if order.TotalPaise != 2000 {
		t.Fatalf("order total = %d, want 2000", order.TotalPaise)
	}
}

func TestCreateOrderMergesDuplicateLines(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)

	seedMenu(t, db, "Masala Chai", "CHAI", 2000)
	userID := seedUser(t, db, "TVE24CS102", 10000)

	orderID, err := CreateOrder(ctx, db, userID, []OrderLine{
		{Code: "CHAI", Quantity: 1},
		{Code: "CHAI", Quantity: 2},
	})
	if err != nil {
		t.Fatalf("CreateOrder() error = %v", err)
	}

	order, err := GetOrderByID(ctx, db, orderID)
	if err != nil {
		t.Fatalf("GetOrderByID() error = %v", err)
	}
	if len(order.Items) != 1 {
		t.Fatalf("GetOrderByID() items length = %d, want 1 (duplicates should merge)", len(order.Items))
	}
	if order.Items[0].Quantity != 3 {
		t.Fatalf("quantity = %d, want 3", order.Items[0].Quantity)
	}
	if order.TotalPaise != 6000 {
		t.Fatalf("total = %d, want 6000", order.TotalPaise)
	}
}

func TestCreateOrderInsufficientBalanceRollsBack(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)

	seedMenu(t, db, "Vada", "VADA", 3000)
	userID := seedUser(t, db, "TVE24CS103", 2500)

	_, err := CreateOrder(ctx, db, userID, []OrderLine{{Code: "VADA", Quantity: 1}})
	if !errors.Is(err, ErrInsufficientBalance) {
		t.Fatalf("CreateOrder() error = %v, want ErrInsufficientBalance", err)
	}

	// The whole unit of work must have been rolled back: no order, no items, and
	// the balance untouched.
	orders, err := ListOrdersByUser(ctx, db, userID)
	if err != nil {
		t.Fatalf("ListOrdersByUser() error = %v", err)
	}
	if len(orders) != 0 {
		t.Fatalf("ListOrdersByUser() length = %d, want 0 after rollback", len(orders))
	}

	var itemCount int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM order_items`).Scan(&itemCount); err != nil {
		t.Fatalf("count order_items: %v", err)
	}
	if itemCount != 0 {
		t.Fatalf("order_items count = %d, want 0 after rollback", itemCount)
	}

	balance, err := GetUserBalancePaise(ctx, db, userID)
	if err != nil {
		t.Fatalf("GetUserBalancePaise() error = %v", err)
	}
	if balance != 2500 {
		t.Fatalf("balance = %d, want 2500 (unchanged)", balance)
	}
}

func TestCreateOrderUnknownCodeRollsBack(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)

	seedMenu(t, db, "Vada", "VADA", 3000)
	userID := seedUser(t, db, "TVE24CS104", 10000)

	// One valid line and one bogus line: neither may be persisted.
	_, err := CreateOrder(ctx, db, userID, []OrderLine{
		{Code: "VADA", Quantity: 1},
		{Code: "GHOST", Quantity: 1},
	})
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("CreateOrder() error = %v, want ErrNotFound", err)
	}

	orders, err := ListOrdersByUser(ctx, db, userID)
	if err != nil {
		t.Fatalf("ListOrdersByUser() error = %v", err)
	}
	if len(orders) != 0 {
		t.Fatalf("ListOrdersByUser() length = %d, want 0 after rollback", len(orders))
	}

	balance, err := GetUserBalancePaise(ctx, db, userID)
	if err != nil {
		t.Fatalf("GetUserBalancePaise() error = %v", err)
	}
	if balance != 10000 {
		t.Fatalf("balance = %d, want 10000 (unchanged)", balance)
	}
}

func TestCreateOrderValidation(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)

	seedMenu(t, db, "Vada", "VADA", 3000)
	userID := seedUser(t, db, "TVE24CS105", 100000)

	tests := []struct {
		name  string
		userI int64
		lines []OrderLine
	}{
		{name: "no lines", userI: userID, lines: nil},
		{name: "empty cart", userI: userID, lines: []OrderLine{}},
		{name: "empty code", userI: userID, lines: []OrderLine{{Code: "  ", Quantity: 1}}},
		{name: "zero quantity", userI: userID, lines: []OrderLine{{Code: "VADA", Quantity: 0}}},
		{name: "negative quantity", userI: userID, lines: []OrderLine{{Code: "VADA", Quantity: -1}}},
		{name: "quantity over cap", userI: userID, lines: []OrderLine{{Code: "VADA", Quantity: maxQuantityPerLine + 1}}},
		{name: "too many lines", userI: userID, lines: makeLines(maxLinesPerOrder + 1)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := CreateOrder(ctx, db, tt.userI, tt.lines)
			if !errors.Is(err, ErrInvalidInput) {
				t.Fatalf("CreateOrder() error = %v, want ErrInvalidInput", err)
			}
		})
	}

	// A user who does not exist.
	_, err := CreateOrder(ctx, db, 9999, []OrderLine{{Code: "VADA", Quantity: 1}})
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("CreateOrder() for missing user error = %v, want ErrNotFound", err)
	}

	orders, err := ListOrdersByUser(ctx, db, userID)
	if err != nil {
		t.Fatalf("ListOrdersByUser() error = %v", err)
	}
	if len(orders) != 0 {
		t.Fatalf("ListOrdersByUser() length = %d, want 0", len(orders))
	}
}

func makeLines(n int) []OrderLine {
	lines := make([]OrderLine, n)
	for i := range lines {
		lines[i] = OrderLine{Code: "VADA", Quantity: 1}
	}
	return lines
}

func TestCreateOrderFreeItemsSkipsLedger(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)

	seedMenu(t, db, "Free Biscuit", "FREE", 0)
	userID := seedUser(t, db, "TVE24CS106", 0)

	orderID, err := CreateOrder(ctx, db, userID, []OrderLine{{Code: "FREE", Quantity: 3}})
	if err != nil {
		t.Fatalf("CreateOrder() error = %v", err)
	}

	order, err := GetOrderByID(ctx, db, orderID)
	if err != nil {
		t.Fatalf("GetOrderByID() error = %v", err)
	}
	if order.TotalPaise != 0 {
		t.Fatalf("total = %d, want 0", order.TotalPaise)
	}

	// A zero-value ledger row is impossible (CHECK amount_paise > 0), so a free
	// order must produce no transaction at all.
	txns, err := ListTransactionsByUser(ctx, db, userID)
	if err != nil {
		t.Fatalf("ListTransactionsByUser() error = %v", err)
	}
	if len(txns) != 0 {
		t.Fatalf("ListTransactionsByUser() length = %d, want 0", len(txns))
	}
}

func TestOrderLifecycle(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)

	seedMenu(t, db, "Vada", "VADA", 3000)
	userID := seedUser(t, db, "TVE24CS107", 10000)

	orderID, err := CreateOrder(ctx, db, userID, []OrderLine{{Code: "VADA", Quantity: 1}})
	if err != nil {
		t.Fatalf("CreateOrder() error = %v", err)
	}

	queue, err := ListOrdersByStatus(ctx, db, OrderStatusPending)
	if err != nil {
		t.Fatalf("ListOrdersByStatus() error = %v", err)
	}
	if len(queue) != 1 {
		t.Fatalf("pending queue length = %d, want 1", len(queue))
	}

	if err := CompleteOrder(ctx, db, orderID); err != nil {
		t.Fatalf("CompleteOrder() error = %v", err)
	}

	order, err := GetOrderByID(ctx, db, orderID)
	if err != nil {
		t.Fatalf("GetOrderByID() error = %v", err)
	}
	if order.Status != OrderStatusCompleted {
		t.Fatalf("status = %q, want %q", order.Status, OrderStatusCompleted)
	}
	if order.CompletedAt == nil || *order.CompletedAt == "" {
		t.Fatal("completedAt was not stamped")
	}

	// Completing twice is a conflict, not a silent no-op.
	if err := CompleteOrder(ctx, db, orderID); !errors.Is(err, ErrConflict) {
		t.Fatalf("CompleteOrder() second call error = %v, want ErrConflict", err)
	}

	// Completing is terminal with respect to further completion.
	queue, err = ListOrdersByStatus(ctx, db, OrderStatusPending)
	if err != nil {
		t.Fatalf("ListOrdersByStatus() error = %v", err)
	}
	if len(queue) != 0 {
		t.Fatalf("pending queue length = %d, want 0", len(queue))
	}
}

func TestCancelOrderRefunds(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)

	seedMenu(t, db, "Vada", "VADA", 3000)
	userID := seedUser(t, db, "TVE24CS108", 10000)

	orderID, err := CreateOrder(ctx, db, userID, []OrderLine{{Code: "VADA", Quantity: 2}})
	if err != nil {
		t.Fatalf("CreateOrder() error = %v", err)
	}

	balance, err := GetUserBalancePaise(ctx, db, userID)
	if err != nil {
		t.Fatalf("GetUserBalancePaise() error = %v", err)
	}
	if balance != 4000 {
		t.Fatalf("balance after order = %d, want 4000", balance)
	}

	if err := CancelOrder(ctx, db, orderID); err != nil {
		t.Fatalf("CancelOrder() error = %v", err)
	}

	// The refund must restore the balance in the same transaction as the
	// status change.
	balance, err = GetUserBalancePaise(ctx, db, userID)
	if err != nil {
		t.Fatalf("GetUserBalancePaise() error = %v", err)
	}
	if balance != 10000 {
		t.Fatalf("balance after cancel = %d, want 10000 (fully refunded)", balance)
	}

	order, err := GetOrderByID(ctx, db, orderID)
	if err != nil {
		t.Fatalf("GetOrderByID() error = %v", err)
	}
	if order.Status != OrderStatusCancelled {
		t.Fatalf("status = %q, want %q", order.Status, OrderStatusCancelled)
	}

	// cancelled is terminal.
	if err := CancelOrder(ctx, db, orderID); !errors.Is(err, ErrConflict) {
		t.Fatalf("CancelOrder() second call error = %v, want ErrConflict", err)
	}
	if err := CompleteOrder(ctx, db, orderID); !errors.Is(err, ErrConflict) {
		t.Fatalf("CompleteOrder() on cancelled order error = %v, want ErrConflict", err)
	}

	// The ledger should show the debit and its matching refund.
	txns, err := ListTransactionsByUser(ctx, db, userID)
	if err != nil {
		t.Fatalf("ListTransactionsByUser() error = %v", err)
	}
	if len(txns) != 3 {
		t.Fatalf("transactions length = %d, want 3 (top-up, debit, refund)", len(txns))
	}
	if txns[0].TransactionType != TransactionTypeCredit || txns[0].AmountPaise != 6000 {
		t.Fatalf("newest transaction = %+v, want credit 6000", txns[0])
	}
}

func TestCancelCompletedOrderRefunds(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)

	seedMenu(t, db, "Vada", "VADA", 3000)
	userID := seedUser(t, db, "TVE24CS109", 10000)

	orderID, err := CreateOrder(ctx, db, userID, []OrderLine{{Code: "VADA", Quantity: 1}})
	if err != nil {
		t.Fatalf("CreateOrder() error = %v", err)
	}
	if err := CompleteOrder(ctx, db, orderID); err != nil {
		t.Fatalf("CompleteOrder() error = %v", err)
	}

	// A completed order can still be returned and refunded.
	if err := CancelOrder(ctx, db, orderID); err != nil {
		t.Fatalf("CancelOrder() error = %v", err)
	}
	balance, err := GetUserBalancePaise(ctx, db, userID)
	if err != nil {
		t.Fatalf("GetUserBalancePaise() error = %v", err)
	}
	if balance != 10000 {
		t.Fatalf("balance = %d, want 10000", balance)
	}
}

func TestOrderNotFound(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)

	if _, err := GetOrderByID(ctx, db, 999); !errors.Is(err, ErrNotFound) {
		t.Fatalf("GetOrderByID() error = %v, want ErrNotFound", err)
	}
	if err := CompleteOrder(ctx, db, 999); !errors.Is(err, ErrNotFound) {
		t.Fatalf("CompleteOrder() error = %v, want ErrNotFound", err)
	}
	if err := CancelOrder(ctx, db, 999); !errors.Is(err, ErrNotFound) {
		t.Fatalf("CancelOrder() error = %v, want ErrNotFound", err)
	}
	if _, err := ListOrdersByStatus(ctx, db, "bogus"); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("ListOrdersByStatus() error = %v, want ErrInvalidInput", err)
	}
}

func TestListOrdersByUserNewestFirst(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)

	seedMenu(t, db, "Vada", "VADA", 3000)
	userID := seedUser(t, db, "TVE24CS110", 100000)

	var ids []int64
	for range 3 {
		id, err := CreateOrder(ctx, db, userID, []OrderLine{{Code: "VADA", Quantity: 1}})
		if err != nil {
			t.Fatalf("CreateOrder() error = %v", err)
		}
		ids = append(ids, id)
	}

	orders, err := ListOrdersByUser(ctx, db, userID)
	if err != nil {
		t.Fatalf("ListOrdersByUser() error = %v", err)
	}
	if len(orders) != 3 {
		t.Fatalf("ListOrdersByUser() length = %d, want 3", len(orders))
	}
	// Newest first, so walk the expected IDs in reverse.
	for i, order := range orders {
		if want := ids[len(ids)-1-i]; order.ID != want {
			t.Fatalf("orders[%d].ID = %d, want %d (newest first)", i, order.ID, want)
		}
	}
}

func TestTopUpUserValidation(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)

	userID := seedUser(t, db, "TVE24CS111", 0)

	for _, amount := range []int64{0, -1} {
		if _, err := TopUpUser(ctx, db, userID, amount); !errors.Is(err, ErrInvalidInput) {
			t.Fatalf("TopUpUser(%d) error = %v, want ErrInvalidInput", amount, err)
		}
	}
	if _, err := TopUpUser(ctx, db, 999, 100); !errors.Is(err, ErrNotFound) {
		t.Fatalf("TopUpUser() for missing user error = %v, want ErrNotFound", err)
	}
}

// TestSchemaRejectsBadMoney guards the CHECK constraints directly, since they
// are the last line of defence if a future code path bypasses the Go validation.
func TestSchemaRejectsBadMoney(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)

	menuID := seedMenu(t, db, "Vada", "VADA", 3000)
	userID := seedUser(t, db, "TVE24CS112", 10000)
	orderID, err := CreateOrder(ctx, db, userID, []OrderLine{{Code: "VADA", Quantity: 1}})
	if err != nil {
		t.Fatalf("CreateOrder() error = %v", err)
	}

	tests := []struct {
		name  string
		query string
		args  []any
	}{
		{name: "negative price", query: `UPDATE menu SET price_paise = -1 WHERE id = ?`, args: []any{menuID}},
		{name: "availability out of range", query: `UPDATE menu SET is_available = 7 WHERE id = ?`, args: []any{menuID}},
		{name: "empty item name", query: `UPDATE menu SET item_name = '  ' WHERE id = ?`, args: []any{menuID}},
		{name: "unknown order status", query: `UPDATE orders SET status = 'teleported' WHERE id = ?`, args: []any{orderID}},
		{name: "zero quantity", query: `UPDATE order_items SET quantity = 0 WHERE order_id = ?`, args: []any{orderID}},
		{name: "inconsistent line total", query: `UPDATE order_items SET line_total_paise = line_total_paise + 1 WHERE order_id = ?`, args: []any{orderID}},
		{name: "unknown transaction type", query: `INSERT INTO transactions (user_id, amount_paise, transaction_type) VALUES (?, 100, 'barter')`, args: []any{userID}},
		{name: "zero transaction amount", query: `INSERT INTO transactions (user_id, amount_paise, transaction_type) VALUES (?, 0, 'credit')`, args: []any{userID}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := db.ExecContext(ctx, tt.query, tt.args...); err == nil {
				t.Fatalf("%s was accepted but should have been rejected", tt.name)
			}
		})
	}
}

// TestForeignKeysAreEnforcedOnEveryConnection is the regression test for the
// per-connection PRAGMA problem.
//
// It deliberately does NOT call Initialize: schema.sql no longer sets
// foreign_keys, so the only thing that can turn enforcement on for a fresh
// connection is the DSN. If someone drops _pragma=foreign_keys(1) from the DSN
// and goes back to a one-off Exec on the pool, this fails.
func TestForeignKeysAreEnforcedOnEveryConnection(t *testing.T) {
	ctx := context.Background()

	db, err := Open(filepath.Join(t.TempDir(), "fk.db"))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer db.Close()

	// Not initialized on purpose: the pragma must come from the DSN alone.
	var on int
	if err := db.QueryRowContext(ctx, `PRAGMA foreign_keys`).Scan(&on); err != nil {
		t.Fatalf("read foreign_keys pragma: %v", err)
	}
	if on != 1 {
		t.Fatalf("foreign_keys = %d on a fresh connection, want 1 (set via DSN)", on)
	}

	// And the enforcement must actually bite: a child row for a parent that
	// does not exist has to be refused.
	if _, err := db.ExecContext(ctx, `INSERT INTO orders (user_id) VALUES (999999)`); err == nil {
		t.Fatal("order for a nonexistent user was accepted; foreign keys are not enforced")
	}
}

// TestDeleteOrderedMenuItemIsRestricted pins the ON DELETE RESTRICT that keeps
// order history pointing at a real menu row.
func TestDeleteOrderedMenuItemIsRestricted(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)

	menuID := seedMenu(t, db, "Vada", "VADA", 3000)
	userID := seedUser(t, db, "TVE24CS113", 10000)

	if _, err := CreateOrder(ctx, db, userID, []OrderLine{{Code: "VADA", Quantity: 1}}); err != nil {
		t.Fatalf("CreateOrder() error = %v", err)
	}

	if err := DeleteMenuItem(ctx, db, menuID); err == nil {
		t.Fatal("deleting an ordered menu item succeeded; ON DELETE RESTRICT is not in effect")
	}

	// Retiring it instead must work, and the historical order must survive.
	if err := SetMenuItemAvailability(ctx, db, menuID, 0); err != nil {
		t.Fatalf("SetMenuItemAvailability() error = %v", err)
	}
	orders, err := ListOrdersByUser(ctx, db, userID)
	if err != nil {
		t.Fatalf("ListOrdersByUser() error = %v", err)
	}
	if len(orders) != 1 {
		t.Fatalf("ListOrdersByUser() length = %d, want 1", len(orders))
	}
	items, err := ListOrderItems(ctx, db, orders[0].ID)
	if err != nil {
		t.Fatalf("ListOrderItems() error = %v", err)
	}
	if len(items) != 1 || items[0].MenuID != menuID {
		t.Fatalf("order items = %+v, want the retired item to still be referenced", items)
	}
}

// TestConcurrentOrdersCannotOverdraw exercises the balance check under parallel
// writers, which is the one place a prepaid kiosk can lose money.
//
// Open pins MaxOpenConns(1) so the pool already serialises this, but the guard
// that actually matters is _txlock=immediate: it takes the write lock at BEGIN
// rather than upgrading mid-transaction, so a second writer waits and then reads
// the first writer's committed debit. That is what keeps this test passing if
// MaxOpenConns is ever raised.
//
// Run with -race (needs cgo) to also check for data races.
func TestConcurrentOrdersCannotOverdraw(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)

	seedMenu(t, db, "Vada", "VADA", 3000)
	userID := seedUser(t, db, "TVE24CS114", 10000)

	// Ten orders of 3000 against a balance of 10000: at most three can succeed.
	const attempts = 10
	var wg sync.WaitGroup
	results := make([]error, attempts)

	for i := range attempts {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, results[i] = CreateOrder(ctx, db, userID, []OrderLine{{Code: "VADA", Quantity: 1}})
		}()
	}
	wg.Wait()

	succeeded := 0
	for i, err := range results {
		switch {
		case err == nil:
			succeeded++
		case errors.Is(err, ErrInsufficientBalance):
			// Expected for the losing writers.
		default:
			t.Fatalf("CreateOrder() attempt %d unexpected error = %v", i, err)
		}
	}

	if succeeded == 0 {
		t.Fatal("no order succeeded; expected up to 3 to fit within the balance")
	}
	if succeeded > 3 {
		t.Fatalf("%d orders succeeded, want at most 3 for a 10000 paise balance", succeeded)
	}

	// The ledger must agree with the balance, and must never be overdrawn.
	balance, err := GetUserBalancePaise(ctx, db, userID)
	if err != nil {
		t.Fatalf("GetUserBalancePaise() error = %v", err)
	}
	if balance < 0 {
		t.Fatalf("balance = %d, want >= 0 (the kiosk oversold credit)", balance)
	}
	if want := int64(10000 - succeeded*3000); balance != want {
		t.Fatalf("balance = %d, want %d", balance, want)
	}

	orders, err := ListOrdersByUser(ctx, db, userID)
	if err != nil {
		t.Fatalf("ListOrdersByUser() error = %v", err)
	}
	if len(orders) != succeeded {
		t.Fatalf("orders = %d, want %d", len(orders), succeeded)
	}
}

// TestInitializeRejectsNewerSchema checks the user_version guard: a database
// written by a future release must not be silently opened by this build, because
// CREATE TABLE IF NOT EXISTS would skip the existing tables and leave them
// un-hardened while reporting success.
func TestInitializeRejectsNewerSchema(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)

	if _, err := db.ExecContext(ctx, `PRAGMA user_version = 9999`); err != nil {
		t.Fatalf("set user_version: %v", err)
	}

	err := Initialize(db)
	if err == nil {
		t.Fatal("Initialize() accepted a newer schema version")
	}
	if !strings.Contains(err.Error(), "newer than supported") {
		t.Fatalf("Initialize() error = %v, want a schema version message", err)
	}
}
