# Modifications — Backend Server + Auth

Owner: backend server + auth role. Scope: HTTP only (no MQTT).
All schema changes are additive; no `user_version` bump, no migration.

## Added files

### food-kiosk/server/config.go
Loads config from env with dev defaults.
  ADDR           default ":8080"
  DB_PATH        default "food-kiosk.db"
  JWT_SECRET     required unless AUTH_REQUIRED=false
  JWT_TTL        default "24h"
  AUTH_REQUIRED  default "true"; "false" allows unauthenticated curl testing
  CORS_ORIGINS   comma list; empty disables CORS middleware

### food-kiosk/server/router.go
http.ServeMux (Go 1.22 method+path patterns), middleware chain
(recover, logging, CORS, auth), route table for the 5 routes + /auth/* + /healthz.

### food-kiosk/server/errors.go
writeJSON/writeError helpers and writeDBError(w, err):
  ErrNotFound            -> 404
  ErrInvalidInput        -> 400
  ErrConflict            -> 409
  ErrInsufficientBalance -> 402
  else                   -> 500 (message not leaked)

### food-kiosk/server/auth.go
JWT HS256 sign/verify (claims sub, role, exp); bcrypt hash/compare;
requireAuth and requireRole middleware; token identity injected into request context.

### food-kiosk/server/store.go
Queries for the new tables + a few server-side reads, using database.DB:
  CreateCredential, GetCredentialByPrincipal
  UpsertAdminCredential, BootstrapAdmin (env-provisioned admin login)
  SetOrderReady, ClearOrderReady, ListReadyOrderIDs
  ListPendingOrderIDsByLane (single join on order_items -> menu.code)

### food-kiosk/server/handlers_auth.go
POST /auth/login (bcrypt check -> JWT, per-principal rate limit), GET /auth/me.

### food-kiosk/server/handlers_users.go
POST /users, POST /users/{id}/topup, GET /wallet (self; admin ?user_id=).

### food-kiosk/server/handlers_orders.go
POST /orders, GET /orders (self; admin ?user_id=/status), GET /orders/{id}, POST /orders/{id}/cancel.

### food-kiosk/server/handlers_menu.go
GET /menu (admin ?all=1), POST/PUT/PATCH/DELETE menu (admin-only writes).

### food-kiosk/server/handlers_queue.go
GET /queue?lane=a|kitchen|ready.

### food-kiosk/server/ratelimit.go
In-memory fixed-window login limiter keyed by canonical principal (fallback: IP).

### food-kiosk/server/testdb_test.go
newTestServer(t): temp DB (openTestDB) + Initialize + httptest.Server; newTestServerNoAuth for AUTH_REQUIRED=false; newTestServerWithConfig; seedAdmin helper.

### food-kiosk/server/auth_test.go
healthz, login/me, wrong password, bad body, missing/expired/wrong-secret token, duplicate roll no, short PIN, no-PIN has no login.

### food-kiosk/server/handlers_test.go
topup (balance, 403 for another user, 404 as admin); queue lane routing (a vs kitchen vs ready), empty and invalid lane; order total, token-vs-body user id, 402 insufficient, 404 unknown code; cancel refunds + 403 for another user; AUTH_REQUIRED=false curl mode.

### food-kiosk/server/handlers_menu_test.go
Menu CRUD: 403/401 guards, customer sees only available, admin ?all=1, PUT/PATCH overlays, delete + 409 on ordered item, 404 on unknown id.

### food-kiosk/server/handlers_read_test.go
Wallet (self, 403 for another, admin by ?user_id), orders list (scope + status filter, admin views), order detail + 403, admin bootstrap idempotency, admin role in /auth/me.

### food-kiosk/server/ratelimit_test.go
Limiter window/reset/disable unit tests + login 429 integration.

### food-kiosk/server/config_test.go
LoadConfig defaults, auth-disabled, short secret, admin pair/short-password validation, bad durations, splitCSV.

### food-kiosk/server/middleware_test.go
CORS allow/deny/preflight, recover middleware 500.

## Modified files

### food-kiosk/main.go
Replace stub with: load config -> database.Open(DB_PATH) -> database.Initialize ->
optional admin bootstrap -> build router -> http.Server (read/write/idle timeouts)
-> graceful shutdown on SIGINT/SIGTERM.

### food-kiosk/server/config.go
+ ADMIN_PRINCIPAL/ADMIN_PASSWORD (pair, min 8 bytes), LOGIN_MAX_ATTEMPTS/LOGIN_WINDOW.

### food-kiosk/server/router.go
+ GET /wallet, menu CRUD, GET /orders (+/orders/{id}); adminOnly wrapper; limiter wiring.

### food-kiosk/database/schema.sql
Append two additive tables (credentials, ready_orders) + FK index.

### food-kiosk/go.mod, go.sum
Add github.com/golang-jwt/jwt/v5, golang.org/x/crypto.

### MC-Project-Backend/README.md
New "HTTP server and auth" section: route table, env vars, sentinel->status map,
auth model, curl walkthrough, test commands.

### MC-Project-Backend/MODIFICATIONS.md
This file.

### MC-Project-Backend/CONTEXT.md
Companion context/decision log.

## Schema additions (in database/schema.sql)

CREATE TABLE IF NOT EXISTS credentials (
    id            INTEGER PRIMARY KEY AUTOINCREMENT,
    principal     TEXT NOT NULL UNIQUE,
    password_hash TEXT NOT NULL,
    role          TEXT NOT NULL DEFAULT 'customer' CHECK (role IN ('customer','admin')),
    user_id       INTEGER,
    created_at    TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
    FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE
);
CREATE INDEX IF NOT EXISTS idx_credentials_user_id ON credentials(user_id);

CREATE TABLE IF NOT EXISTS ready_orders (
    order_id INTEGER PRIMARY KEY,
    ready_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
    FOREIGN KEY (order_id) REFERENCES orders(id) ON DELETE CASCADE
);

## Untouched
food-kiosk/database/*.go (all of it): db.go, errors.go, users.go, menu.go, orders.go,
schema.sql is the only exception (additive append only).

## Not in this change (deferred)
- MQTT bridge (mqtt.go) — separate owner.
- Admin UI beyond the HTTP role guards (frontend work).
- Persisting lane at order time (snapshot) — deferred: needs a non-additive
  orders.lane column and coordination with the DB owner.
- PIN policy hardening (login min length + rate limiting are in; per-IP
  limiting, account lockout are not).
