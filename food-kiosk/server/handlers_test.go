package server

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"food-kiosk/database"
)

func adminToken(t *testing.T) string {
	t.Helper()

	a := NewAuth(testConfig().JWTSecret, time.Hour)
	token, err := a.IssueToken("ADMIN", RoleAdmin, 0)
	if err != nil {
		t.Fatalf("issue admin token: %v", err)
	}
	return token
}

func placeOrder(t *testing.T, ts *httptest.Server, token string, userID int64, lines []map[string]any) createOrderResponse {
	t.Helper()

	resp, data := doJSON(t, http.MethodPost, ts.URL+"/orders",
		map[string]any{"user_id": userID, "lines": lines}, token)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create order: status %d body %s", resp.StatusCode, data)
	}
	return decodeInto[createOrderResponse](t, data)
}

func getQueue(t *testing.T, ts *httptest.Server, token, lane string) (queueResponse, int) {
	t.Helper()

	resp, data := doJSON(t, http.MethodGet, ts.URL+"/queue?lane="+lane, nil, token)
	return decodeInto[queueResponse](t, data), resp.StatusCode
}

func TestTopUpUpdatesBalance(t *testing.T) {
	ts, _ := newTestServer(t)
	userID, token := registerUser(t, ts, "22CS010", "1111")

	if got := topUp(t, ts, token, userID, 5000); got != 5000 {
		t.Fatalf("balance = %d, want 5000", got)
	}
	if got := topUp(t, ts, token, userID, 2500); got != 7500 {
		t.Fatalf("balance = %d, want 7500", got)
	}
}

func TestTopUpForbiddenForAnotherUser(t *testing.T) {
	ts, _ := newTestServer(t)
	_, tokenA := registerUser(t, ts, "22CS011", "1111")
	idB, _ := registerUser(t, ts, "22CS012", "1111")

	resp, data := doJSON(t, http.MethodPost, fmt.Sprintf("%s/users/%d/topup", ts.URL, idB),
		map[string]any{"amount_paise": 1000}, tokenA)
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 (body %s)", resp.StatusCode, data)
	}
}

func TestTopUpUnknownUserAsAdmin(t *testing.T) {
	ts, _ := newTestServer(t)

	resp, data := doJSON(t, http.MethodPost, ts.URL+"/users/99999/topup",
		map[string]any{"amount_paise": 1000}, adminToken(t))
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 (body %s)", resp.StatusCode, data)
	}
}

func TestQueueLaneRouting(t *testing.T) {
	ts, db := newTestServer(t)
	mealID := seedMenu(t, db, "MEAL", 5000)
	porID := seedMenu(t, db, "POR", 3000)
	_ = porID

	userID, token := registerUser(t, ts, "22CS020", "1111")
	topUp(t, ts, token, userID, 50000)

	mealOnly := placeOrder(t, ts, token, userID, []map[string]any{{"code": "MEAL", "quantity": 1}})
	porOnly := placeOrder(t, ts, token, userID, []map[string]any{{"code": "POR", "quantity": 1}})
	mixed := placeOrder(t, ts, token, userID, []map[string]any{
		{"code": "MEAL", "quantity": 1},
		{"code": "POR", "quantity": 1},
	})
	_ = mealID

	// Counter A holds only all-MEAL carts, so just mealOnly.
	a, _ := getQueue(t, ts, token, "a")
	if a.Head == nil || *a.Head != mealOnly.ID {
		t.Fatalf("lane a head = %v, want %d", a.Head, mealOnly.ID)
	}
	if a.Depth != 1 || len(a.Items) != 1 {
		t.Fatalf("lane a = %+v, want depth 1", a)
	}

	// Kitchen holds everything with a non-MEAL line: porOnly and mixed.
	k, _ := getQueue(t, ts, token, "kitchen")
	if k.Depth != 2 {
		t.Fatalf("lane kitchen depth = %d, want 2", k.Depth)
	}
	want := map[int64]bool{porOnly.ID: true, mixed.ID: true}
	for _, id := range k.Items {
		if !want[id] {
			t.Fatalf("lane kitchen unexpected id %d in %v", id, k.Items)
		}
	}
}

func TestQueueEmptyLane(t *testing.T) {
	ts, _ := newTestServer(t)
	_, token := registerUser(t, ts, "22CS030", "1111")

	q, status := getQueue(t, ts, token, "a")
	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200", status)
	}
	if q.Head != nil || q.Depth != 0 {
		t.Fatalf("empty lane = %+v, want head nil depth 0", q)
	}
	if q.Items == nil || len(q.Items) != 0 {
		t.Fatalf("items = %v, want non-nil empty slice", q.Items)
	}
}

func TestQueueReadyLane(t *testing.T) {
	ts, db := newTestServer(t)
	seedMenu(t, db, "POR", 3000)
	userID, token := registerUser(t, ts, "22CS031", "1111")
	topUp(t, ts, token, userID, 50000)

	order := placeOrder(t, ts, token, userID, []map[string]any{{"code": "POR", "quantity": 1}})

	if err := SetOrderReady(context.Background(), db, order.ID); err != nil {
		t.Fatalf("set order ready: %v", err)
	}

	q, status := getQueue(t, ts, token, "ready")
	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200", status)
	}
	if q.Head == nil || *q.Head != order.ID || q.Depth != 1 {
		t.Fatalf("ready lane = %+v, want head %d depth 1", q, order.ID)
	}
}

func TestQueueInvalidLane(t *testing.T) {
	ts, _ := newTestServer(t)
	_, token := registerUser(t, ts, "22CS032", "1111")

	resp, data := doJSON(t, http.MethodGet, ts.URL+"/queue?lane=sideways", nil, token)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (body %s)", resp.StatusCode, data)
	}
}

func TestCreateOrderUsesTokenUserNotBody(t *testing.T) {
	ts, db := newTestServer(t)
	seedMenu(t, db, "POR", 3000)
	userA, tokenA := registerUser(t, ts, "22CS040", "1111")
	userB, _ := registerUser(t, ts, "22CS041", "1111")
	topUp(t, ts, tokenA, userA, 50000)

	order := placeOrder(t, ts, tokenA, userB, []map[string]any{{"code": "POR", "quantity": 1}})

	stored, err := database.GetOrderByID(context.Background(), db, order.ID)
	if err != nil {
		t.Fatalf("get order: %v", err)
	}
	if stored.UserID != userA {
		t.Fatalf("order user = %d, want %d (token owner)", stored.UserID, userA)
	}
}

func TestCreateOrderReturnsTotal(t *testing.T) {
	ts, db := newTestServer(t)
	seedMenu(t, db, "MEAL", 5000)
	userID, token := registerUser(t, ts, "22CS042", "1111")
	topUp(t, ts, token, userID, 50000)

	order := placeOrder(t, ts, token, userID, []map[string]any{{"code": "MEAL", "quantity": 3}})
	if order.TotalPaise != 15000 {
		t.Fatalf("total = %d, want 15000", order.TotalPaise)
	}
}

func TestCreateOrderInsufficientBalance(t *testing.T) {
	ts, db := newTestServer(t)
	seedMenu(t, db, "MEAL", 5000)
	userID, token := registerUser(t, ts, "22CS043", "1111")

	resp, data := doJSON(t, http.MethodPost, ts.URL+"/orders",
		map[string]any{"user_id": userID, "lines": []map[string]any{{"code": "MEAL", "quantity": 1}}}, token)
	if resp.StatusCode != http.StatusPaymentRequired {
		t.Fatalf("status = %d, want 402 (body %s)", resp.StatusCode, data)
	}
}

func TestCreateOrderUnknownCode(t *testing.T) {
	ts, _ := newTestServer(t)
	userID, token := registerUser(t, ts, "22CS044", "1111")
	topUp(t, ts, token, userID, 50000)

	resp, data := doJSON(t, http.MethodPost, ts.URL+"/orders",
		map[string]any{"user_id": userID, "lines": []map[string]any{{"code": "NOPE", "quantity": 1}}}, token)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 (body %s)", resp.StatusCode, data)
	}
}

func TestCancelOrderRefunds(t *testing.T) {
	ts, db := newTestServer(t)
	seedMenu(t, db, "MEAL", 5000)
	userID, token := registerUser(t, ts, "22CS050", "1111")
	topUp(t, ts, token, userID, 10000)

	order := placeOrder(t, ts, token, userID, []map[string]any{{"code": "MEAL", "quantity": 1}})

	resp, data := doJSON(t, http.MethodPost, fmt.Sprintf("%s/orders/%d/cancel", ts.URL, order.ID), nil, token)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("cancel: status %d body %s", resp.StatusCode, data)
	}
	if got := decodeInto[balanceResponse](t, data).BalancePaise; got != 10000 {
		t.Fatalf("balance after cancel = %d, want 10000", got)
	}
}

func TestCancelOrderForbiddenForAnotherUser(t *testing.T) {
	ts, db := newTestServer(t)
	seedMenu(t, db, "MEAL", 5000)
	owner, ownerToken := registerUser(t, ts, "22CS051", "1111")
	_, otherToken := registerUser(t, ts, "22CS052", "1111")
	topUp(t, ts, ownerToken, owner, 10000)

	order := placeOrder(t, ts, ownerToken, owner, []map[string]any{{"code": "MEAL", "quantity": 1}})

	resp, data := doJSON(t, http.MethodPost, fmt.Sprintf("%s/orders/%d/cancel", ts.URL, order.ID), nil, otherToken)
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 (body %s)", resp.StatusCode, data)
	}
}

func TestAuthDisabledTrustsBodyUserID(t *testing.T) {
	ts, db := newTestServerNoAuth(t)
	seedMenu(t, db, "MEAL", 5000)

	resp, data := doJSON(t, http.MethodPost, ts.URL+"/users",
		map[string]any{"roll_no": "22CS060", "name": "Curl", "phone": "900"}, "")
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create user: status %d body %s", resp.StatusCode, data)
	}
	userID := decodeInto[createUserResponse](t, data).ID

	// No token anywhere: the body names the user and the money.
	if resp, data := doJSON(t, http.MethodPost, ts.URL+"/users/"+fmt.Sprint(userID)+"/topup",
		map[string]any{"amount_paise": 50000}, ""); resp.StatusCode != http.StatusOK {
		t.Fatalf("topup: status %d body %s", resp.StatusCode, data)
	}

	order := placeOrder(t, ts, "", userID, []map[string]any{{"code": "MEAL", "quantity": 1}})
	if order.TotalPaise != 5000 {
		t.Fatalf("total = %d, want 5000", order.TotalPaise)
	}
}
