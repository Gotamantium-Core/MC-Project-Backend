package server

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"golang.org/x/crypto/bcrypt"
)

// Role is the coarse authorisation role carried in a token. Customer is the
// only role in use now; admin exists so adding it later needs no schema or
// format change (rows with role='admin' and a requireRole guard).
type Role string

const (
	RoleCustomer Role = "customer"
	RoleAdmin    Role = "admin"
)

// Claims is the JWT payload. Subject carries the principal (roll_no or
// username); UserID is the users.id a customer authenticates as, and is 0 for
// a principal that is not a customer (e.g. an admin with no user row).
type Claims struct {
	Role string `json:"role"`
	UID  int64  `json:"uid,omitempty"`
	jwt.RegisteredClaims
}

// Identity is the authenticated caller, placed in the request context by
// requireAuth.
type Identity struct {
	Principal string
	Role      Role
	UserID    int64
}

func (i *Identity) IsAdmin() bool { return i.Role == RoleAdmin }

type ctxKey int

const identityKey ctxKey = iota

// identityFrom returns the caller identity, or false when the request is
// unauthenticated (which can happen when AUTH_REQUIRED=false).
func identityFrom(r *http.Request) (*Identity, bool) {
	id, ok := r.Context().Value(identityKey).(*Identity)
	return id, ok
}

// Auth issues and verifies bearer tokens.
type Auth struct {
	secret []byte
	ttl    time.Duration
}

func NewAuth(secret []byte, ttl time.Duration) *Auth {
	return &Auth{secret: secret, ttl: ttl}
}

// IssueToken signs an HS256 token for the principal. ttl is applied as the exp
// claim; the role and user id ride along so handlers need no extra lookup.
func (a *Auth) IssueToken(principal string, role Role, userID int64) (string, error) {
	now := time.Now()
	claims := Claims{
		Role: string(role),
		UID:  userID,
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   principal,
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(a.ttl)),
		},
	}

	token, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString(a.secret)
	if err != nil {
		return "", fmt.Errorf("sign token: %w", err)
	}
	return token, nil
}

// Verify parses and validates a token, pinning the algorithm to HMAC so a
// token cannot ask to be verified with "none" or an asymmetric key.
func (a *Auth) Verify(tokenString string) (*Identity, error) {
	claims := &Claims{}
	_, err := jwt.ParseWithClaims(tokenString, claims, func(t *jwt.Token) (any, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method %q", t.Header["alg"])
		}
		return a.secret, nil
	}, jwt.WithValidMethods([]string{jwt.SigningMethodHS256.Alg()}))
	if err != nil {
		return nil, fmt.Errorf("verify token: %w", err)
	}

	return &Identity{
		Principal: claims.Subject,
		Role:      Role(claims.Role),
		UserID:    claims.UID,
	}, nil
}

// bearerToken pulls the credential out of an Authorization header.
func bearerToken(r *http.Request) (string, bool) {
	h := r.Header.Get("Authorization")
	const prefix = "Bearer "
	if len(h) <= len(prefix) || !strings.EqualFold(h[:len(prefix)], prefix) {
		return "", false
	}
	token := strings.TrimSpace(h[len(prefix):])
	return token, token != ""
}

// requireAuth rejects a request without a valid bearer token, then puts the
// resulting identity in the context for the handler.
func (a *Auth) requireAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		tokenString, ok := bearerToken(r)
		if !ok {
			writeError(w, http.StatusUnauthorized, "authentication required")
			return
		}

		id, err := a.Verify(tokenString)
		if err != nil {
			writeError(w, http.StatusUnauthorized, "invalid or expired token")
			return
		}

		next(w, r.WithContext(context.WithValue(r.Context(), identityKey, id)))
	}
}

// requireRole guards a route to a specific role. Unused today beyond customer
// routes, but it is the hook admin routes will use.
func requireRole(role Role, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, ok := identityFrom(r)
		if !ok || id.Role != role {
			writeError(w, http.StatusForbidden, "forbidden")
			return
		}
		next(w, r)
	}
}

// HashPassword returns a bcrypt hash suitable for the credentials table.
func HashPassword(pw string) (string, error) {
	hash, err := bcrypt.GenerateFromPassword([]byte(pw), bcrypt.DefaultCost)
	if err != nil {
		return "", fmt.Errorf("hash password: %w", err)
	}
	return string(hash), nil
}

// CheckPassword reports whether pw matches the stored bcrypt hash.
func CheckPassword(hash, pw string) bool {
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(pw)) == nil
}

func parseID(s string) (int64, error) {
	id, err := strconv.ParseInt(s, 10, 64)
	if err != nil || id <= 0 {
		return 0, errors.New("invalid id")
	}
	return id, nil
}
