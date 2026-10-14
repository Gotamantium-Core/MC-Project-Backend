package database

import (
	"context"
	"errors"
	"testing"
)

func TestGetTransactionByID(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)

	userID := seedUser(t, db, "TVE24CS127", 0)
	txnID, err := TopUpUser(ctx, db, userID, 7500)
	if err != nil {
		t.Fatalf("TopUpUser() error = %v", err)
	}

	txn, err := GetTransactionByID(ctx, db, txnID)
	if err != nil {
		t.Fatalf("GetTransactionByID() error = %v", err)
	}
	if txn.UserID != userID || txn.AmountPaise != 7500 {
		t.Fatalf("GetTransactionByID() txn = %+v", txn)
	}
	if txn.TransactionType != TransactionTypeCredit {
		t.Fatalf("GetTransactionByID() type = %q, want %q", txn.TransactionType, TransactionTypeCredit)
	}
	// A top-up belongs to no order, and that has to survive the round trip.
	if txn.OrderID != nil {
		t.Fatalf("GetTransactionByID() order_id = %d, want nil", *txn.OrderID)
	}

	if _, err := GetTransactionByID(ctx, db, 999); !errors.Is(err, ErrNotFound) {
		t.Fatalf("GetTransactionByID() error = %v, want ErrNotFound", err)
	}
}

// TestListTransactionsByOrder covers the settlement view: one order's debit and
// the refund that cancelled it.
func TestListTransactionsByOrder(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)

	seedMenu(t, db, "Vada", "VADA", 3000)
	userID := seedUser(t, db, "TVE24CS128", 10000)

	orderID, err := CreateOrder(ctx, db, userID, []OrderLine{{Code: "VADA", Quantity: 2}})
	if err != nil {
		t.Fatalf("CreateOrder() error = %v", err)
	}

	// While pending, the only movement is the debit. The user's top-up has a
	// NULL order_id and must not be attributed to this order.
	txns, err := ListTransactionsByOrder(ctx, db, orderID)
	if err != nil {
		t.Fatalf("ListTransactionsByOrder() error = %v", err)
	}
	if len(txns) != 1 {
		t.Fatalf("ListTransactionsByOrder() length = %d, want 1 (the debit only)", len(txns))
	}
	if txns[0].TransactionType != TransactionTypeDebit || txns[0].AmountPaise != 6000 {
		t.Fatalf("ListTransactionsByOrder()[0] = %+v, want debit 6000", txns[0])
	}
	if txns[0].OrderID == nil || *txns[0].OrderID != orderID {
		t.Fatalf("ListTransactionsByOrder()[0].OrderID = %v, want %d", txns[0].OrderID, orderID)
	}

	if err := CancelOrder(ctx, db, orderID); err != nil {
		t.Fatalf("CancelOrder() error = %v", err)
	}

	// Cancelling adds the matching credit, oldest first.
	txns, err = ListTransactionsByOrder(ctx, db, orderID)
	if err != nil {
		t.Fatalf("ListTransactionsByOrder() error = %v", err)
	}
	if len(txns) != 2 {
		t.Fatalf("ListTransactionsByOrder() length = %d, want 2 (debit and refund)", len(txns))
	}
	if txns[0].TransactionType != TransactionTypeDebit || txns[1].TransactionType != TransactionTypeCredit {
		t.Fatalf("ListTransactionsByOrder() = %+v, want debit then credit", txns)
	}
	if txns[1].AmountPaise != 6000 {
		t.Fatalf("refund amount = %d, want 6000 (the full debit)", txns[1].AmountPaise)
	}

	// An order that never existed has no history, and the nil slice is part of
	// the contract the list helpers share.
	unknown, err := ListTransactionsByOrder(ctx, db, 999)
	if err != nil {
		t.Fatalf("ListTransactionsByOrder() error = %v", err)
	}
	if unknown != nil {
		t.Fatalf("ListTransactionsByOrder(999) = %+v, want nil", unknown)
	}
}

func TestListTransactions(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)

	// An untouched database has no ledger to audit.
	empty, err := ListTransactions(ctx, db)
	if err != nil {
		t.Fatalf("ListTransactions() error = %v", err)
	}
	if empty != nil {
		t.Fatalf("ListTransactions() on an empty ledger = %+v, want nil", empty)
	}

	seedMenu(t, db, "Vada", "VADA", 3000)
	first := seedUser(t, db, "TVE24CS129", 10000)
	second := seedUser(t, db, "TVE24CS130", 10000)

	if _, err := CreateOrder(ctx, db, first, []OrderLine{{Code: "VADA", Quantity: 1}}); err != nil {
		t.Fatalf("CreateOrder() error = %v", err)
	}
	secondOrder, err := CreateOrder(ctx, db, second, []OrderLine{{Code: "VADA", Quantity: 2}})
	if err != nil {
		t.Fatalf("CreateOrder() error = %v", err)
	}

	// The audit view spans every user, unlike ListTransactionsByUser.
	txns, err := ListTransactions(ctx, db)
	if err != nil {
		t.Fatalf("ListTransactions() error = %v", err)
	}
	if len(txns) != 4 {
		t.Fatalf("ListTransactions() length = %d, want 4 (two top-ups, two debits)", len(txns))
	}

	// Newest first across users.
	if txns[0].OrderID == nil || *txns[0].OrderID != secondOrder {
		t.Fatalf("newest transaction order_id = %v, want %d", txns[0].OrderID, secondOrder)
	}
	if txns[0].AmountPaise != 6000 || txns[0].TransactionType != TransactionTypeDebit {
		t.Fatalf("newest transaction = %+v, want debit 6000", txns[0])
	}

	// And a per-user read is still scoped to that user.
	scoped, err := ListTransactionsByUser(ctx, db, first)
	if err != nil {
		t.Fatalf("ListTransactionsByUser() error = %v", err)
	}
	if len(scoped) != 2 {
		t.Fatalf("ListTransactionsByUser() length = %d, want 2", len(scoped))
	}
	for _, txn := range scoped {
		if txn.UserID != first {
			t.Fatalf("ListTransactionsByUser() returned user %d, want only %d", txn.UserID, first)
		}
	}
}
