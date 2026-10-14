package server

import (
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestHealthz(t *testing.T) {
	ts, _ := newTestServer(t)

	resp, data := doJSON(t, http.MethodGet, ts.URL+"/healthz", nil, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %s)", resp.StatusCode, data)
	}
}

func TestLoginAndMe(t *testing.T) {
	ts, _ := newTestServer(t)

	resp, data := doJSON(t, http.MethodPost, ts.URL+"/users",
		map[string]any{"roll_no": "22cs001", "name": "Asha", "phone": "900", "pin": "4321"}, "")
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create user: status %d body %s", resp.StatusCode, data)
	}
	userID := decodeInto[createUserResponse](t, data).ID

	// Login is case-insensitive on the principal.
	token, status := login(t, ts, " 22cs001 ", "4321")
	if status != http.StatusOK {
		t.Fatalf("login status = %d, want 200", status)
	}

	resp, data = doJSON(t, http.MethodGet, ts.URL+"/auth/me", nil, token)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("me: status %d body %s", resp.StatusCode, data)
	}
	me := decodeInto[meResponse](t, data)
	if me.ID != userID || me.RollNo != "22CS001" || me.Role != string(RoleCustomer) {
		t.Fatalf("unexpected me response: %+v", me)
	}
}

func TestLoginWrongPassword(t *testing.T) {
	ts, _ := newTestServer(t)
	registerUser(t, ts, "22CS002", "1111")

	if _, status := login(t, ts, "22CS002", "wrong"); status != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", status)
	}
}

func TestLoginUnknownPrincipal(t *testing.T) {
	ts, _ := newTestServer(t)

	if _, status := login(t, ts, "NOPE", "1111"); status != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", status)
	}
}

func TestLoginBadBody(t *testing.T) {
	ts, _ := newTestServer(t)

	req, err := http.NewRequest(http.MethodPost, ts.URL+"/auth/login", strings.NewReader("{"))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
}

func TestProtectedRouteRequiresToken(t *testing.T) {
	ts, _ := newTestServer(t)

	resp, _ := doJSON(t, http.MethodGet, ts.URL+"/auth/me", nil, "")
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", resp.StatusCode)
	}
}

func TestProtectedRouteRejectsBadToken(t *testing.T) {
	ts, _ := newTestServer(t)

	resp, _ := doJSON(t, http.MethodGet, ts.URL+"/auth/me", nil, "not-a-jwt")
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", resp.StatusCode)
	}
}

func TestProtectedRouteRejectsExpiredToken(t *testing.T) {
	ts, _ := newTestServer(t)

	expired := NewAuth(testConfig().JWTSecret, -time.Minute)
	token, err := expired.IssueToken("22CS003", RoleCustomer, 1)
	if err != nil {
		t.Fatalf("issue expired token: %v", err)
	}

	resp, _ := doJSON(t, http.MethodGet, ts.URL+"/auth/me", nil, token)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", resp.StatusCode)
	}
}

func TestVerifyRejectsWrongSecret(t *testing.T) {
	issuer := NewAuth([]byte("secret-one-0123456789"), time.Hour)
	token, err := issuer.IssueToken("22CS004", RoleCustomer, 1)
	if err != nil {
		t.Fatalf("issue token: %v", err)
	}

	verifier := NewAuth([]byte("secret-two-9876543210"), time.Hour)
	if _, err := verifier.Verify(token); err == nil {
		t.Fatal("expected verify to fail with a different secret")
	}
}

func TestCreateUserDuplicateRollNo(t *testing.T) {
	ts, _ := newTestServer(t)
	registerUser(t, ts, "22CS005", "1111")

	resp, data := doJSON(t, http.MethodPost, ts.URL+"/users",
		map[string]any{"roll_no": "22CS005", "name": "Dup", "phone": "900", "pin": "2222"}, "")
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("status = %d, want 409 (body %s)", resp.StatusCode, data)
	}
}

func TestCreateUserShortPIN(t *testing.T) {
	ts, _ := newTestServer(t)

	resp, data := doJSON(t, http.MethodPost, ts.URL+"/users",
		map[string]any{"roll_no": "22CS006", "name": "Short", "phone": "900", "pin": "1"}, "")
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (body %s)", resp.StatusCode, data)
	}
}

func TestCreateUserWithoutPINHasNoLogin(t *testing.T) {
	ts, _ := newTestServer(t)

	resp, data := doJSON(t, http.MethodPost, ts.URL+"/users",
		map[string]any{"roll_no": "22CS007", "name": "NoPin", "phone": "900"}, "")
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("status = %d, want 201 (body %s)", resp.StatusCode, data)
	}

	if _, status := login(t, ts, "22CS007", "anything"); status != http.StatusUnauthorized {
		t.Fatalf("login status = %d, want 401", status)
	}
}
