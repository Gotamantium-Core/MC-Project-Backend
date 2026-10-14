package database

import (
	"context"
	"errors"
	"testing"
)

func TestGetOrderItemByID(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)

	seedMenu(t, db, "Vada", "VADA", 3000)
	userID := seedUser(t, db, "TVE24CS124", 10000)

	orderID, err := CreateOrder(ctx, db, userID, []OrderLine{{Code: "VADA", Quantity: 2}})
	if err != nil {
		t.Fatalf("CreateOrder() error = %v", err)
	}

	items, err := ListOrderItems(ctx, db, orderID)
	if err != nil {
		t.Fatalf("ListOrderItems() error = %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("ListOrderItems() length = %d, want 1", len(items))
	}

	item, err := GetOrderItemByID(ctx, db, items[0].ID)
	if err != nil {
		t.Fatalf("GetOrderItemByID() error = %v", err)
	}
	if item.OrderID != orderID || item.ItemName != "Vada" || item.Quantity != 2 {
		t.Fatalf("GetOrderItemByID() item = %+v", item)
	}
	if item.UnitPricePaise != 3000 || item.LineTotalPaise != 6000 {
		t.Fatalf("GetOrderItemByID() money = %+v, want 3000 x 2 = 6000", item)
	}

	if _, err := GetOrderItemByID(ctx, db, 999); !errors.Is(err, ErrNotFound) {
		t.Fatalf("GetOrderItemByID() error = %v, want ErrNotFound", err)
	}
}

// TestListOrderItemsByMenuItem covers the sales-history query behind
// idx_order_items_menu_id, which nothing used before it.
func TestListOrderItemsByMenuItem(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)

	vadaID := seedMenu(t, db, "Vada", "VADA", 3000)
	chaiID := seedMenu(t, db, "Masala Chai", "CHAI", 2000)
	userID := seedUser(t, db, "TVE24CS125", 100000)

	firstOrder, err := CreateOrder(ctx, db, userID, []OrderLine{
		{Code: "VADA", Quantity: 1},
		{Code: "CHAI", Quantity: 2},
	})
	if err != nil {
		t.Fatalf("CreateOrder() error = %v", err)
	}
	secondOrder, err := CreateOrder(ctx, db, userID, []OrderLine{{Code: "VADA", Quantity: 3}})
	if err != nil {
		t.Fatalf("CreateOrder() error = %v", err)
	}

	// Both orders touched VADA, oldest first, across orders.
	vadaLines, err := ListOrderItemsByMenuItem(ctx, db, vadaID)
	if err != nil {
		t.Fatalf("ListOrderItemsByMenuItem() error = %v", err)
	}
	if len(vadaLines) != 2 {
		t.Fatalf("ListOrderItemsByMenuItem(vada) length = %d, want 2", len(vadaLines))
	}
	if vadaLines[0].OrderID != firstOrder || vadaLines[0].Quantity != 1 {
		t.Fatalf("ListOrderItemsByMenuItem(vada)[0] = %+v, want the first order's single vada", vadaLines[0])
	}
	if vadaLines[1].OrderID != secondOrder || vadaLines[1].Quantity != 3 {
		t.Fatalf("ListOrderItemsByMenuItem(vada)[1] = %+v, want the second order's three vadas", vadaLines[1])
	}

	// The query filters by menu_id, so the other item must not leak in.
	chaiLines, err := ListOrderItemsByMenuItem(ctx, db, chaiID)
	if err != nil {
		t.Fatalf("ListOrderItemsByMenuItem(chai) error = %v", err)
	}
	if len(chaiLines) != 1 || chaiLines[0].OrderID != firstOrder {
		t.Fatalf("ListOrderItemsByMenuItem(chai) = %+v, want only the first order's chai line", chaiLines)
	}

	// An item nobody has ordered yet has no history, and the nil slice is part
	// of the contract the list helpers share.
	unusedID := seedMenu(t, db, "Filter Coffee", "COFFEE", 2500)
	unused, err := ListOrderItemsByMenuItem(ctx, db, unusedID)
	if err != nil {
		t.Fatalf("ListOrderItemsByMenuItem(unused) error = %v", err)
	}
	if unused != nil {
		t.Fatalf("ListOrderItemsByMenuItem(unused) = %+v, want nil", unused)
	}
}

// TestListOrderItemsByMenuItemReadsTheSnapshot pins that this is a history
// query, not a live join: repricing an item must not rewrite its sales record.
func TestListOrderItemsByMenuItemReadsTheSnapshot(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)

	menuID := seedMenu(t, db, "Vada", "VADA", 3000)
	userID := seedUser(t, db, "TVE24CS126", 10000)

	orderID, err := CreateOrder(ctx, db, userID, []OrderLine{{Code: "VADA", Quantity: 1}})
	if err != nil {
		t.Fatalf("CreateOrder() error = %v", err)
	}

	if err := UpdateMenuItem(ctx, db, Menu{ID: menuID, ItemName: "Vada Special", Code: "VADA", PricePaise: 4500, IsAvailable: 1}); err != nil {
		t.Fatalf("UpdateMenuItem() error = %v", err)
	}

	lines, err := ListOrderItemsByMenuItem(ctx, db, menuID)
	if err != nil {
		t.Fatalf("ListOrderItemsByMenuItem() error = %v", err)
	}
	if len(lines) != 1 {
		t.Fatalf("ListOrderItemsByMenuItem() length = %d, want 1", len(lines))
	}
	if lines[0].OrderID != orderID || lines[0].ItemName != "Vada" || lines[0].UnitPricePaise != 3000 {
		t.Fatalf("ListOrderItemsByMenuItem() = %+v, want the snapshotted name and price", lines[0])
	}
}
