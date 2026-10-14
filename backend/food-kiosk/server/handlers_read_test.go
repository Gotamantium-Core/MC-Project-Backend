package server

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

func walletBalance(t *testing.T, ts *httptest.Server, token, query string) (int64, int) {
	t.Helper()

	resp, data := doJSON(t, http.MethodGet, ts.URL+"/wallet"+query, nil, token)
	if resp.StatusCode != http.StatusOK {
		return 0, resp.StatusCode
	}
	return decodeInto[balanceResponse](t, data).BalancePaise, resp.StatusCode
}

func getOrders(t *testing.T, ts *httptest.Server, token, query string) (orderListResponse, int) {
	t.Helper()

	resp, data := doJSON(t, http.MethodGet, ts.URL+"/orders"+query, nil, token)
	return decodeInto[orderListResponse](t, data), resp.StatusCode
}

func TestWalletReturnsOwnBalance(t *testing.T) {
	ts, _ := newTestServer(t)
	userID, token := registerUser(t, ts, "22CS090", "1111")
	topUp(t, ts, token, userID, 7000)

	balance, status := walletBalance(t, ts, token, "")
	if status != http.StatusOK || balance != 7000 {
		t.Fatalf("wallet = %d (status %d), want 7000", balance, status)
	}
}

func TestWalletForOtherUserForbidden(t *testing.T) {
	ts, _ := newTestServer(t)
	_, tokenA := registerUser(t, ts, "22CS091", "1111")
	idB, _ := registerUser(t, ts, "22CS092", "1111")

	if _, status := walletBalance(t, ts, tokenA, fmt.Sprintf("?user_id=%d", idB)); status != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", status)
	}
}

func TestWalletAsAdminForUser(t *testing.T) {
	ts, db := newTestServer(t)
	admin := seedAdmin(t, ts, db, "BOSS", "admin-password")
	userID, token := registerUser(t, ts, "22CS093", "1111")
	topUp(t, ts, token, userID, 4200)

	balance, status := walletBalance(t, ts, admin, fmt.Sprintf("?user_id=%d", userID))
	if status != http.StatusOK || balance != 4200 {
		t.Fatalf("admin wallet = %d (status %d), want 4200", balance, status)
	}
}

func TestWalletAsAdminWithoutUserIDIsBadRequest(t *testing.T) {
	ts, db := newTestServer(t)
	admin := seedAdmin(t, ts, db, "BOSS", "admin-password")

	if _, status := walletBalance(t, ts, admin, ""); status != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", status)
	}
}

func TestListOrdersScopesAndFilters(t *testing.T) {
	ts, db := newTestServer(t)
	seedMenu(t, db, "MEAL", 5000)
	seedMenu(t, db, "POR", 3000)
	userID, token := registerUser(t, ts, "22CS100", "1111")
	topUp(t, ts, token, userID, 50000)

	first := placeOrder(t, ts, token, userID, []map[string]any{{"code": "MEAL", "quantity": 1}})
	placeOrder(t, ts, token, userID, []map[string]any{{"code": "POR", "quantity": 1}})

	all, status := getOrders(t, ts, token, "")
	if status != http.StatusOK || len(all.Orders) != 2 {
		t.Fatalf("orders = %+v (status %d), want 2", all, status)
	}

	// Cancel one and confirm the status filter reflects it.
	if _, data := doJSON(t, http.MethodPost, fmt.Sprintf("%s/orders/%d/cancel", ts.URL, first.ID), nil, token); data == nil {
		t.Fatal("cancel returned no body")
	}

	pending, _ := getOrders(t, ts, token, "?status=pending")
	if len(pending.Orders) != 1 {
		t.Fatalf("pending = %+v, want 1", pending)
	}
	cancelled, _ := getOrders(t, ts, token, "?status=cancelled")
	if len(cancelled.Orders) != 1 || cancelled.Orders[0].ID != first.ID {
		t.Fatalf("cancelled = %+v, want order %d", cancelled, first.ID)
	}

	// Invalid status is rejected.
	if _, status := getOrders(t, ts, token, "?status=sideways"); status != http.StatusBadRequest {
		t.Fatalf("invalid status = %d, want 400", status)
	}
}

func TestListOrdersAsAdmin(t *testing.T) {
	ts, db := newTestServer(t)
	seedMenu(t, db, "MEAL", 5000)
	admin := seedAdmin(t, ts, db, "BOSS", "admin-password")
	userID, token := registerUser(t, ts, "22CS101", "1111")
	topUp(t, ts, token, userID, 50000)
	placeOrder(t, ts, token, userID, []map[string]any{{"code": "MEAL", "quantity": 1}})

	byUser, status := getOrders(t, ts, admin, fmt.Sprintf("?user_id=%d", userID))
	if status != http.StatusOK || len(byUser.Orders) != 1 {
		t.Fatalf("admin by user = %+v (status %d)", byUser, status)
	}

	byStatus, status := getOrders(t, ts, admin, "?status=pending")
	if status != http.StatusOK || len(byStatus.Orders) != 1 {
		t.Fatalf("admin by status = %+v (status %d)", byStatus, status)
	}

	// With neither selector the admin has no implicit "all", so it is a 400.
	if _, status := getOrders(t, ts, admin, ""); status != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", status)
	}
}

func TestGetOrderDetailAndForbidden(t *testing.T) {
	ts, db := newTestServer(t)
	seedMenu(t, db, "MEAL", 5000)
	ownerID, ownerToken := registerUser(t, ts, "22CS102", "1111")
	_, otherToken := registerUser(t, ts, "22CS103", "1111")
	topUp(t, ts, ownerToken, ownerID, 50000)

	order := placeOrder(t, ts, ownerToken, ownerID, []map[string]any{{"code": "MEAL", "quantity": 2}})

	resp, data := doJSON(t, http.MethodGet, fmt.Sprintf("%s/orders/%d", ts.URL, order.ID), nil, ownerToken)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("detail status = %d body %s", resp.StatusCode, data)
	}
	detail := decodeInto[orderDetailResponse](t, data)
	if detail.ID != order.ID || len(detail.Items) != 1 || detail.Items[0].Quantity != 2 {
		t.Fatalf("detail = %+v", detail)
	}

	resp, _ = doJSON(t, http.MethodGet, fmt.Sprintf("%s/orders/%d", ts.URL, order.ID), nil, otherToken)
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("other customer status = %d, want 403", resp.StatusCode)
	}

	resp, _ = doJSON(t, http.MethodGet, ts.URL+"/orders/9999", nil, ownerToken)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("missing order status = %d, want 404", resp.StatusCode)
	}
}

func TestAdminBootstrapIsIdempotent(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()

	if err := BootstrapAdmin(ctx, db, "boss", "first-password"); err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	if err := BootstrapAdmin(ctx, db, "BOSS", "second-password"); err != nil {
		t.Fatalf("re-bootstrap: %v", err)
	}

	cred, err := GetCredentialByPrincipal(ctx, db, "boss")
	if err != nil {
		t.Fatalf("get credential: %v", err)
	}
	if cred.Role != RoleAdmin || cred.UserID.Valid {
		t.Fatalf("credential = %+v, want admin with no user", cred)
	}
	if CheckPassword(cred.PasswordHash, "first-password") {
		t.Fatal("old password should no longer match")
	}
	if !CheckPassword(cred.PasswordHash, "second-password") {
		t.Fatal("new password should match")
	}
}

func TestAdminLoginReportsRole(t *testing.T) {
	ts, db := newTestServer(t)
	admin := seedAdmin(t, ts, db, "BOSS", "admin-password")

	resp, data := doJSON(t, http.MethodGet, ts.URL+"/auth/me", nil, admin)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("me status = %d body %s", resp.StatusCode, data)
	}
	me := decodeInto[map[string]any](t, data)
	if me["role"] != string(RoleAdmin) {
		t.Fatalf("role = %v, want admin", me["role"])
	}
}
