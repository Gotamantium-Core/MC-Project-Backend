package server

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

func listMenu(t *testing.T, ts *httptest.Server, token, query string) (menuListResponse, int) {
	t.Helper()

	resp, data := doJSON(t, http.MethodGet, ts.URL+"/menu"+query, nil, token)
	return decodeInto[menuListResponse](t, data), resp.StatusCode
}

func createMenu(t *testing.T, ts *httptest.Server, token string, req createMenuRequest) int64 {
	t.Helper()

	resp, data := doJSON(t, http.MethodPost, ts.URL+"/menu", req, token)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create menu: status %d body %s", resp.StatusCode, data)
	}
	return decodeInto[map[string]int64](t, data)["id"]
}

func TestMenuCreateRequiresAdmin(t *testing.T) {
	ts, _ := newTestServer(t)
	_, customer := registerUser(t, ts, "22CS080", "1111")

	resp, data := doJSON(t, http.MethodPost, ts.URL+"/menu",
		createMenuRequest{ItemName: "Meal", Code: "MEAL", PricePaise: 5000}, customer)
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 (body %s)", resp.StatusCode, data)
	}
}

func TestMenuCreateRequiresToken(t *testing.T) {
	ts, _ := newTestServer(t)

	resp, _ := doJSON(t, http.MethodPost, ts.URL+"/menu",
		createMenuRequest{ItemName: "Meal", Code: "MEAL", PricePaise: 5000}, "")
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", resp.StatusCode)
	}
}

func TestMenuCRUDAsAdmin(t *testing.T) {
	ts, db := newTestServer(t)
	admin := seedAdmin(t, ts, db, "BOSS", "admin-password")
	_, customer := registerUser(t, ts, "22CS081", "1111")

	mealID := createMenu(t, ts, admin, createMenuRequest{ItemName: "Meal", Code: "MEAL", PricePaise: 5000})
	unavailable := false
	porID := createMenu(t, ts, admin, createMenuRequest{
		ItemName: "Porotta", Code: "POR", PricePaise: 3000, IsAvailable: &unavailable,
	})

	// A customer sees only the available item.
	menu, status := listMenu(t, ts, customer, "")
	if status != http.StatusOK || len(menu.Items) != 1 || menu.Items[0].Code != "MEAL" {
		t.Fatalf("customer menu = %+v (status %d), want only MEAL", menu, status)
	}

	// ?all=1 is admin-only.
	if _, status := listMenu(t, ts, customer, "?all=1"); status != http.StatusForbidden {
		t.Fatalf("customer ?all=1 status = %d, want 403", status)
	}

	all, status := listMenu(t, ts, admin, "?all=1")
	if status != http.StatusOK || len(all.Items) != 2 {
		t.Fatalf("admin ?all=1 = %+v (status %d), want 2 items", all, status)
	}

	// Rename/reprice and flip availability.
	newPrice := int64(6000)
	resp, data := doJSON(t, http.MethodPut, fmt.Sprintf("%s/menu/%d", ts.URL, mealID),
		updateMenuRequest{PricePaise: &newPrice}, admin)
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("put menu: status %d body %s", resp.StatusCode, data)
	}

	resp, data = doJSON(t, http.MethodPatch, fmt.Sprintf("%s/menu/%d/availability", ts.URL, porID),
		menuAvailabilityRequest{IsAvailable: true}, admin)
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("patch availability: status %d body %s", resp.StatusCode, data)
	}

	menu, _ = listMenu(t, ts, customer, "")
	if len(menu.Items) != 2 {
		t.Fatalf("customer menu after restore = %+v, want 2 items", menu)
	}
	for _, item := range menu.Items {
		if item.ID == mealID && item.PricePaise != 6000 {
			t.Fatalf("price = %d, want 6000", item.PricePaise)
		}
	}

	// Delete, then a repeat delete is 404.
	resp, data = doJSON(t, http.MethodDelete, fmt.Sprintf("%s/menu/%d", ts.URL, porID), nil, admin)
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("delete menu: status %d body %s", resp.StatusCode, data)
	}
	resp, _ = doJSON(t, http.MethodDelete, fmt.Sprintf("%s/menu/%d", ts.URL, porID), nil, admin)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("second delete status = %d, want 404", resp.StatusCode)
	}
}

func TestUpdateUnknownMenuIsNotFound(t *testing.T) {
	ts, db := newTestServer(t)
	admin := seedAdmin(t, ts, db, "BOSS", "admin-password")

	resp, data := doJSON(t, http.MethodPut, ts.URL+"/menu/9999",
		updateMenuRequest{ItemName: "Ghost"}, admin)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 (body %s)", resp.StatusCode, data)
	}
}

func TestDeleteOrderedMenuIsConflict(t *testing.T) {
	ts, db := newTestServer(t)
	admin := seedAdmin(t, ts, db, "BOSS", "admin-password")
	userID, token := registerUser(t, ts, "22CS082", "1111")
	topUp(t, ts, token, userID, 50000)

	mealID := createMenu(t, ts, admin, createMenuRequest{ItemName: "Meal", Code: "MEAL", PricePaise: 5000})
	placeOrder(t, ts, token, userID, []map[string]any{{"code": "MEAL", "quantity": 1}})

	resp, data := doJSON(t, http.MethodDelete, fmt.Sprintf("%s/menu/%d", ts.URL, mealID), nil, admin)
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("status = %d, want 409 (body %s)", resp.StatusCode, data)
	}
}
