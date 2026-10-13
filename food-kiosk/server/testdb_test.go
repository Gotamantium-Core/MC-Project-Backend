package server

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"food-kiosk/database"
)

// openTestDB gives each test an initialized throwaway database.
func openTestDB(t *testing.T) *sql.DB {
	t.Helper()

	db, err := database.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	if err := database.Initialize(db); err != nil {
		t.Fatalf("initialize test db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func testConfig() Config {
	return Config{
		Addr:         ":0",
		JWTSecret:    []byte("test-secret-0123456789"),
		JWTTTL:       time.Hour,
		AuthRequired: true,
	}
}

// newTestServer starts an httptest server over a fresh database.
func newTestServer(t *testing.T) (*httptest.Server, *sql.DB) {
	t.Helper()
	return newTestServerWithConfig(t, testConfig())
}

// newTestServerWithConfig is the configurable variant, for tests that need the
// rate limiter or CORS turned on.
func newTestServerWithConfig(t *testing.T, cfg Config) (*httptest.Server, *sql.DB) {
	t.Helper()

	db := openTestDB(t)
	ts := httptest.NewServer(NewRouter(cfg, db))
	t.Cleanup(ts.Close)
	return ts, db
}

// newTestServerNoAuth is the AUTH_REQUIRED=false variant used by the curl-mode
// test.
func newTestServerNoAuth(t *testing.T) (*httptest.Server, *sql.DB) {
	t.Helper()

	cfg := testConfig()
	cfg.AuthRequired = false

	db := openTestDB(t)
	ts := httptest.NewServer(NewRouter(cfg, db))
	t.Cleanup(ts.Close)
	return ts, db
}

// seedAdmin bootstraps an admin credential directly and returns a signed-in
// token, so admin-only routes can be exercised without going through config.
func seedAdmin(t *testing.T, ts *httptest.Server, db *sql.DB, principal, password string) string {
	t.Helper()

	if err := BootstrapAdmin(context.Background(), db, principal, password); err != nil {
		t.Fatalf("bootstrap admin: %v", err)
	}
	token, status := login(t, ts, principal, password)
	if status != http.StatusOK {
		t.Fatalf("admin login: status %d", status)
	}
	return token
}

func seedMenu(t *testing.T, db *sql.DB, code string, pricePaise int64) int64 {
	t.Helper()

	id, err := database.CreateMenuItem(context.Background(), db, "Item "+code, code, pricePaise, 1)
	if err != nil {
		t.Fatalf("seed menu %q: %v", code, err)
	}
	return id
}

// doJSON sends an optional JSON body with an optional bearer token and returns
// the response and its body.
func doJSON(t *testing.T, method, url string, body any, token string) (*http.Response, []byte) {
	t.Helper()

	var reader io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal request: %v", err)
		}
		reader = bytes.NewReader(encoded)
	}

	req, err := http.NewRequest(method, url, reader)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do request: %v", err)
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read response: %v", err)
	}
	return resp, data
}

func decodeInto[T any](t *testing.T, data []byte) T {
	t.Helper()

	var v T
	if err := json.Unmarshal(data, &v); err != nil {
		t.Fatalf("unmarshal response %q: %v", data, err)
	}
	return v
}

func login(t *testing.T, ts *httptest.Server, principal, password string) (string, int) {
	t.Helper()

	resp, data := doJSON(t, http.MethodPost, ts.URL+"/auth/login",
		map[string]any{"principal": principal, "password": password}, "")
	if resp.StatusCode != http.StatusOK {
		return "", resp.StatusCode
	}
	return decodeInto[loginResponse](t, data).Token, resp.StatusCode
}

// registerUser creates a customer with a PIN and returns its id and a token.
func registerUser(t *testing.T, ts *httptest.Server, rollNo, pin string) (int64, string) {
	t.Helper()

	resp, data := doJSON(t, http.MethodPost, ts.URL+"/users",
		map[string]any{"roll_no": rollNo, "name": "Test " + rollNo, "phone": "9000000000", "pin": pin}, "")
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("register %s: status %d body %s", rollNo, resp.StatusCode, data)
	}
	id := decodeInto[createUserResponse](t, data).ID

	token, status := login(t, ts, rollNo, pin)
	if status != http.StatusOK {
		t.Fatalf("login %s: status %d", rollNo, status)
	}
	return id, token
}

func topUp(t *testing.T, ts *httptest.Server, token string, userID, amountPaise int64) int64 {
	t.Helper()

	resp, data := doJSON(t, http.MethodPost, fmt.Sprintf("%s/users/%d/topup", ts.URL, userID),
		map[string]any{"amount_paise": amountPaise}, token)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("topup: status %d body %s", resp.StatusCode, data)
	}
	return decodeInto[balanceResponse](t, data).BalancePaise
}
