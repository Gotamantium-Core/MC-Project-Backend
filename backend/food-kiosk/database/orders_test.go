package database

import (
	"context"
	"database/sql"
	"errors"
	"math"
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

// TestCreateOrderRejectsOverflowingTotals reaches the overflow-checked
// arithmetic. A price near MaxInt64 is legal (the CHECK only requires >= 0), so
// nothing stops a menu edit from making price * quantity or the running order
// total exceed int64. Wrapping instead of erroring would write a negative total,
// so these must come back as ErrInvalidInput with nothing written.
func TestCreateOrderRejectsOverflowingTotals(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)

	userID := seedUser(t, db, "TVE24CS131", 0)

	// price * quantity overflows on its own.
	seedMenu(t, db, "Absurd Item", "HUGE", int64(math.MaxInt64))

	// Two lines that are each fine on their own but overflow when summed: each is
	// three quarters of MaxInt64, so the pair cannot fit.
	threeQuarters := int64(math.MaxInt64) / 4 * 3
	seedMenu(t, db, "Big A", "BIGA", threeQuarters)
	seedMenu(t, db, "Big B", "BIGB", threeQuarters)

	tests := []struct {
		name  string
		lines []OrderLine
	}{
		{name: "line total overflows", lines: []OrderLine{{Code: "HUGE", Quantity: 2}}},
		{name: "order total overflows", lines: []OrderLine{{Code: "BIGA", Quantity: 1}, {Code: "BIGB", Quantity: 1}}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := CreateOrder(ctx, db, userID, tt.lines)
			if !errors.Is(err, ErrInvalidInput) {
				t.Fatalf("CreateOrder() error = %v, want ErrInvalidInput", err)
			}
		})
	}

	orders, err := ListOrdersByUser(ctx, db, userID)
	if err != nil {
		t.Fatalf("ListOrdersByUser() error = %v", err)
	}
	if orders != nil {
		t.Fatalf("ListOrdersByUser() = %+v, want nil (no order may survive an overflow)", orders)
	}
}

// TestCreateOrderRejectsCombinedQuantityOverCap covers the limit that only exists
// once duplicate codes are merged: 60 + 60 of the same item is 120, which is over
// the per-line cap even though neither line was on its own.
func TestCreateOrderRejectsCombinedQuantityOverCap(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)

	seedMenu(t, db, "Vada", "VADA", 3000)
	// Enough credit for the full cap in one line (100 * 3000).
	userID := seedUser(t, db, "TVE24CS132", 1000000)

	half := int64(maxQuantityPerLine/2 + 1)
	_, err := CreateOrder(ctx, db, userID, []OrderLine{
		{Code: "VADA", Quantity: half},
		{Code: "VADA", Quantity: half},
	})
	if !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("CreateOrder() error = %v, want ErrInvalidInput", err)
	}
	if !strings.Contains(err.Error(), "combined quantity") {
		t.Fatalf("CreateOrder() error = %v, want a combined quantity message", err)
	}

	// The same total in a single line is fine, which is what makes this a merge
	// limit rather than a blanket quantity check.
	if _, err := CreateOrder(ctx, db, userID, []OrderLine{{Code: "VADA", Quantity: maxQuantityPerLine}}); err != nil {
		t.Fatalf("CreateOrder() at exactly the cap error = %v, want nil", err)
	}
}

// TestGetOrderByIDRejectsTotalMismatch covers the cross-row check in GetOrderByID.
// orders.total_paise cannot be constrained against its own lines by SQL, so a
// hand-edited or buggy writer could leave a total that disagrees with what the
// items add up to. Reporting that order as if it were valid would misreport what
// the customer was charged.
func TestGetOrderByIDRejectsTotalMismatch(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)

	seedMenu(t, db, "Vada", "VADA", 3000)
	userID := seedUser(t, db, "TVE24CS133", 10000)

	orderID, err := CreateOrder(ctx, db, userID, []OrderLine{{Code: "VADA", Quantity: 1}})
	if err != nil {
		t.Fatalf("CreateOrder() error = %v", err)
	}

	// The orders table only checks total_paise >= 0, so this edit is accepted by
	// the schema; only Go notices the disagreement.
	if _, err := db.ExecContext(ctx, `UPDATE orders SET total_paise = total_paise + 100 WHERE id = ?`, orderID); err != nil {
		t.Fatalf("tamper with order total: %v", err)
	}

	if _, err := GetOrderByID(ctx, db, orderID); err == nil {
		t.Fatal("GetOrderByID() returned an order whose total does not match its lines")
	} else if !strings.Contains(err.Error(), "does not match line sum") {
		t.Fatalf("GetOrderByID() error = %v, want a total mismatch message", err)
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

	if err := DeleteMenuItem(ctx, db, menuID); !errors.Is(err, ErrConflict) {
		t.Fatalf("DeleteMenuItem() on an ordered item error = %v, want ErrConflict", err)
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

// TestRetiredItemHidesButStaysOrderable documents the availability contract.
//
// SetMenuItemAvailability is a display flag, not an access control: it removes
// the item from the customer-facing menu so the kiosk stops offering it, while
// leaving the row intact so the item can be switched back on when it returns.
// CreateOrder deliberately does NOT check is_available, because a code-entered
// order is still priced and recorded normally; the flag exists to hide a sold-out
// item from the screen, not to refuse a customer who already has the code.
//
// This is a decision, not an accident. If the kiosk should instead reject orders
// for a retired item, GetMenuItemsByCodes needs to filter on is_available and
// this test has to be inverted.
func TestRetiredItemHidesButStaysOrderable(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)

	menuID := seedMenu(t, db, "Samosa", "SAM", 1500)
	userID := seedUser(t, db, "TVE24CS151", 10000)

	// Available: shown on the menu and orderable.
	available, err := ListAvailableMenuItems(ctx, db)
	if err != nil {
		t.Fatalf("ListAvailableMenuItems() error = %v", err)
	}
	if len(available) != 1 {
		t.Fatalf("ListAvailableMenuItems() length = %d, want 1", len(available))
	}

	if err := SetMenuItemAvailability(ctx, db, menuID, 0); err != nil {
		t.Fatalf("SetMenuItemAvailability() error = %v", err)
	}

	// Retired: hidden from the customer-facing menu, but still in the full list
	// for admin screens, and still resolvable by code.
	available, err = ListAvailableMenuItems(ctx, db)
	if err != nil {
		t.Fatalf("ListAvailableMenuItems() error = %v", err)
	}
	if available != nil {
		t.Fatalf("ListAvailableMenuItems() = %+v, want nil for a retired item", available)
	}
	all, err := ListMenuItems(ctx, db)
	if err != nil {
		t.Fatalf("ListMenuItems() error = %v", err)
	}
	if len(all) != 1 || all[0].IsAvailable != 0 {
		t.Fatalf("ListMenuItems() = %+v, want the retired item still listed", all)
	}

	item, err := GetMenuItemByCode(ctx, db, "SAM")
	if err != nil {
		t.Fatalf("GetMenuItemByCode() error = %v", err)
	}
	if item.IsAvailable != 0 {
		t.Fatalf("GetMenuItemByCode().IsAvailable = %d, want 0", item.IsAvailable)
	}

	// And a code-entered order still succeeds, priced from the menu as usual.
	orderID, err := CreateOrder(ctx, db, userID, []OrderLine{{Code: "SAM", Quantity: 2}})
	if err != nil {
		t.Fatalf("CreateOrder() on a retired item error = %v", err)
	}
	balance, err := GetUserBalancePaise(ctx, db, userID)
	if err != nil {
		t.Fatalf("GetUserBalancePaise() error = %v", err)
	}
	if balance != 10000-2*1500 {
		t.Fatalf("balance = %d, want %d", balance, 10000-2*1500)
	}

	// Restoring availability brings it back with its price intact.
	if err := SetMenuItemAvailability(ctx, db, menuID, 1); err != nil {
		t.Fatalf("SetMenuItemAvailability() error = %v", err)
	}
	restored, err := ListAvailableMenuItems(ctx, db)
	if err != nil {
		t.Fatalf("ListAvailableMenuItems() error = %v", err)
	}
	if len(restored) != 1 || restored[0].PricePaise != 1500 {
		t.Fatalf("ListAvailableMenuItems() after restore = %+v, want the item back at 1500", restored)
	}

	// And the order placed while it was retired is untouched.
	items, err := ListOrderItems(ctx, db, orderID)
	if err != nil {
		t.Fatalf("ListOrderItems() error = %v", err)
	}
	if len(items) != 1 || items[0].UnitPricePaise != 1500 {
		t.Fatalf("order items = %+v, want the retired-era order preserved", items)
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

// TestDeleteOrderRefundsAndRemovesItems is the core promise of DeleteOrder:
// once it returns nil the customer has their money back and the order is gone.
func TestDeleteOrderRefundsAndRemovesItems(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)

	seedMenu(t, db, "Vada", "VADA", 3000)
	userID := seedUser(t, db, "TVE24CS115", 10000)

	orderID, err := CreateOrder(ctx, db, userID, []OrderLine{{Code: "VADA", Quantity: 2}})
	if err != nil {
		t.Fatalf("CreateOrder() error = %v", err)
	}

	if err := DeleteOrder(ctx, db, orderID); err != nil {
		t.Fatalf("DeleteOrder() error = %v", err)
	}

	if _, err := GetOrderByID(ctx, db, orderID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("GetOrderByID() after delete error = %v, want ErrNotFound", err)
	}

	// The lines go with the order, via ON DELETE CASCADE.
	items, err := ListOrderItems(ctx, db, orderID)
	if err != nil {
		t.Fatalf("ListOrderItems() error = %v", err)
	}
	if items != nil {
		t.Fatalf("ListOrderItems() after delete = %+v, want nil", items)
	}

	// Nobody may be left out of pocket for an order that no longer exists.
	balance, err := GetUserBalancePaise(ctx, db, userID)
	if err != nil {
		t.Fatalf("GetUserBalancePaise() error = %v", err)
	}
	if balance != 10000 {
		t.Fatalf("balance = %d, want 10000 (the refund must land with the delete)", balance)
	}

	// The ledger outlives the purge: both the debit and its refund survive,
	// detached from the order they belonged to.
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
	if txns[0].OrderID != nil {
		t.Fatalf("refund order_id = %d, want nil after the order row is deleted", *txns[0].OrderID)
	}
}

// TestDeleteCancelledOrderDoesNotRefundTwice is the test that has to exist.
//
// Cancelling already refunded the order, so deleting it afterwards must notice
// the balance is settled. Refunding the raw debit total a second time would pay
// the customer back twice for an order they never received.
func TestDeleteCancelledOrderDoesNotRefundTwice(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)

	seedMenu(t, db, "Vada", "VADA", 3000)
	userID := seedUser(t, db, "TVE24CS116", 10000)

	orderID, err := CreateOrder(ctx, db, userID, []OrderLine{{Code: "VADA", Quantity: 2}})
	if err != nil {
		t.Fatalf("CreateOrder() error = %v", err)
	}
	if err := CancelOrder(ctx, db, orderID); err != nil {
		t.Fatalf("CancelOrder() error = %v", err)
	}

	if err := DeleteOrder(ctx, db, orderID); err != nil {
		t.Fatalf("DeleteOrder() error = %v", err)
	}

	// Still exactly the top-up, the debit and the one refund. A fourth row would
	// be a double refund.
	txns, err := ListTransactionsByUser(ctx, db, userID)
	if err != nil {
		t.Fatalf("ListTransactionsByUser() error = %v", err)
	}
	if len(txns) != 3 {
		t.Fatalf("transactions length = %d, want 3 (top-up, debit, single refund)", len(txns))
	}

	balance, err := GetUserBalancePaise(ctx, db, userID)
	if err != nil {
		t.Fatalf("GetUserBalancePaise() error = %v", err)
	}
	if balance != 10000 {
		t.Fatalf("balance = %d, want 10000 (already refunded once, must not credit again)", balance)
	}
}

// TestDeleteCompletedOrderRefunds covers the state an operator is most likely
// to purge: an order that was handed over and then turned out to be wrong.
func TestDeleteCompletedOrderRefunds(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)

	seedMenu(t, db, "Vada", "VADA", 3000)
	userID := seedUser(t, db, "TVE24CS117", 10000)

	orderID, err := CreateOrder(ctx, db, userID, []OrderLine{{Code: "VADA", Quantity: 1}})
	if err != nil {
		t.Fatalf("CreateOrder() error = %v", err)
	}
	if err := CompleteOrder(ctx, db, orderID); err != nil {
		t.Fatalf("CompleteOrder() error = %v", err)
	}

	if err := DeleteOrder(ctx, db, orderID); err != nil {
		t.Fatalf("DeleteOrder() error = %v", err)
	}

	balance, err := GetUserBalancePaise(ctx, db, userID)
	if err != nil {
		t.Fatalf("GetUserBalancePaise() error = %v", err)
	}
	if balance != 10000 {
		t.Fatalf("balance = %d, want 10000 (a completed order still owes a refund)", balance)
	}
}

// TestDeleteFreeOrderWritesNoLedgerRow guards the zero-value case. A cart of
// free items never wrote a debit, so there is nothing to give back and the
// ledger must not gain a credit either.
func TestDeleteFreeOrderWritesNoLedgerRow(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)

	seedMenu(t, db, "Water", "WATER", 0)
	userID := seedUser(t, db, "TVE24CS118", 10000)

	orderID, err := CreateOrder(ctx, db, userID, []OrderLine{{Code: "WATER", Quantity: 1}})
	if err != nil {
		t.Fatalf("CreateOrder() error = %v", err)
	}

	if err := DeleteOrder(ctx, db, orderID); err != nil {
		t.Fatalf("DeleteOrder() error = %v", err)
	}

	txns, err := ListTransactionsByUser(ctx, db, userID)
	if err != nil {
		t.Fatalf("ListTransactionsByUser() error = %v", err)
	}
	if len(txns) != 1 {
		t.Fatalf("transactions length = %d, want 1 (the top-up only)", len(txns))
	}
	if txns[0].TransactionType != TransactionTypeCredit || txns[0].AmountPaise != 10000 {
		t.Fatalf("transaction = %+v, want the untouched top-up", txns[0])
	}
}

// TestDeleteOrderWithOverCreditedLedgerTakesNothingBack covers the sign guard in
// DeleteOrder. A ledger hand-edited so the order was credited more than it was
// debited leaves a negative outstanding amount. There is nothing to refund, and
// debiting the difference would take money away from a customer over an operator's
// bookkeeping mistake.
func TestDeleteOrderWithOverCreditedLedgerTakesNothingBack(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)

	seedMenu(t, db, "Vada", "VADA", 3000)
	userID := seedUser(t, db, "TVE24CS134", 10000)

	orderID, err := CreateOrder(ctx, db, userID, []OrderLine{{Code: "VADA", Quantity: 1}})
	if err != nil {
		t.Fatalf("CreateOrder() error = %v", err)
	}

	// An over-generous refund, written straight to the table. The transaction_type
	// CHECK permits a credit, so only the arithmetic in DeleteOrder can catch it.
	if _, err := db.ExecContext(ctx, `INSERT INTO transactions (user_id, order_id, amount_paise, transaction_type) VALUES (?, ?, 5000, 'credit')`, userID, orderID); err != nil {
		t.Fatalf("insert surplus credit: %v", err)
	}

	if err := DeleteOrder(ctx, db, orderID); err != nil {
		t.Fatalf("DeleteOrder() error = %v", err)
	}

	// Outstanding was 3000 - 5000 = -2000, so DeleteOrder must have written no
	// refund at all.
	balance, err := GetUserBalancePaise(ctx, db, userID)
	if err != nil {
		t.Fatalf("GetUserBalancePaise() error = %v", err)
	}
	if balance != 12000 {
		t.Fatalf("balance = %d, want 12000 (10000 top-up - 3000 debit + 5000 credit)", balance)
	}

	if _, err := GetOrderByID(ctx, db, orderID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("GetOrderByID() after delete error = %v, want ErrNotFound", err)
	}
}

func TestDeleteOrderErrors(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)

	if err := DeleteOrder(ctx, db, 999); !errors.Is(err, ErrNotFound) {
		t.Fatalf("DeleteOrder() on a missing order error = %v, want ErrNotFound", err)
	}

	if err := DeleteOrder(ctx, nil, 1); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("DeleteOrder() with a nil handle error = %v, want ErrInvalidInput", err)
	}

	// A *sql.Tx cannot open a nested transaction, and the refund has to live in
	// the same one, so a tx handle is refused up front rather than half-applied.
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("BeginTx() error = %v", err)
	}
	defer tx.Rollback()

	err = DeleteOrder(ctx, tx, 1)
	if err == nil {
		t.Fatal("DeleteOrder() with a tx handle returned nil, want a handle error")
	}
	if !strings.Contains(err.Error(), "requires a *sql.DB handle") {
		t.Fatalf("DeleteOrder() with a tx handle error = %v, want a handle error", err)
	}
}
