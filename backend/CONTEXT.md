# Project Context & Decision Log

Companion to MODIFICATIONS.md. Everything decided to date, for future work.

## 1. What the project is
Low-cost campus-canteen queue system (Kerala). Three ESP32-C3 stations with
16x2 I2C LCD + buzzer + button, one local Mosquitto broker, one central backend,
SQLite. No cloud. Money in paise (int64). Token = orders.id.

Stations / queues:
- Node 1 Kitchen assembly bay  -> kitchen queue
- Node 2 Counter A (meals line)-> counter-a queue
- Node 3 Counter B (pickup)    -> ready list

Routing rule: order with ONLY code MEAL -> Counter A; anything else -> Kitchen
(short code = first non-MEAL menu code, <=7 chars). Mixed carts go to Kitchen.

MQTT topics (owned by the MQTT teammate, NOT this work):
  canteen/counter-a/now        backend->Node2  retained  "<head> Q<depth>|IDLE Q0"
  canteen/counter-a/served     Node2->backend   transient "<id>"
  canteen/kitchen/now          backend->Node1  retained  "<head> <code> Q<depth>|IDLE Q0"
  canteen/kitchen/done         Node1->backend   transient "<id>"
  canteen/counter-b/ready      backend->Node3  retained  "<head> <code> W<n>|NONE W0"
  canteen/counter-b/collected  Node3->backend   transient "<id>"
  canteen/<node>/online        node->all (LWT)  retained
Dedup: duplicate acks -> CompleteOrder returns ErrConflict -> treated as already done.

## 2. Repo state at handoff
- food-kiosk/database/ : complete, tested (~85% cov). users, menu, orders,
  order_items, transactions (prepaid ledger; balance = credits - debits).
- food-kiosk/main.go   : stub (Open + Initialize + log only).
- No HTTP layer, no auth, no MQTT in this repo.
- Frontend: teammate, unfinished/unpushed. No frozen API contract.

## 3. Role split (from Overall_Task_Description.txt)
Hardware / ESP32 / Message broker+MQTT / **Backend server and auth (this work)** /
Backend DB / Frontend+UI / Reports+docs.

## 4. DB integration facts (verified)
- Sentinels: ErrNotFound 404, ErrInvalidInput 400, ErrConflict 409,
  ErrInsufficientBalance 402.
- CreateOrder(ctx,*sql.DB,userID,[]OrderLine) -> (id, err); returns id only.
- TopUpUser / CancelOrder return no balance -> follow with GetUserBalancePaise.
- CreateOrder + CancelOrder need *sql.DB (open transactions); reads take database.DB.
- ListOrdersByStatus(status) exists; statuses only pending|completed|cancelled.
- "ready" and "lane" are NOT in the schema -> handled by this work (below).
- Schema is embedded; Initialize re-runs it each start, so additive
  CREATE TABLE IF NOT EXISTS lands on existing DBs without a version bump.

## 5. Decisions made (this work)
- HTTP only; MQTT is a separate teammate. stdlib net/http.
- Auth: JWT HS256 (claims sub, role, exp) + bcrypt. New `credentials` table with
  `role`. Admin works today: env bootstrap (ADMIN_PRINCIPAL/ADMIN_PASSWORD,
  upserted each start) + requireRole; menu writes and admin read scopes use it.
- Ready state: persistent `ready_orders` table (safe whether MQTT bridge runs
  in the same process or a separate one). Note: ListReadyOrderIDs filters on
  orders.status='pending', so a completed order drops out even if its ready row
  lingers — clearing ready on collect is cosmetic.
- Route protection: wallet/order/menu reads and admin writes require a token;
  token sub overrides body user_id; /auth/*, /users open; AUTH_REQUIRED=false
  enables curl testing (admin routes pass through too).
- Login throttling: fixed-window per-principal limiter (LOGIN_MAX_ATTEMPTS per
  LOGIN_WINDOW), reset on success -> 429.
- Lane derived at read time by joining order_items -> menu.code.

## 6. API contract (this work)
POST /auth/login   {principal, password}          -> {token, role}
GET  /auth/me                                       -> {id, roll_no, name, phone, role}
POST /users        {roll_no, name, phone, pin?}     -> {id}
POST /users/{id}/topup {amount_paise}               -> {balance_paise}
GET  /wallet       (?user_id= admin only)           -> {balance_paise}
GET  /menu         (?all=1 admin only)              -> {items:[...]}
POST/PUT/PATCH/DELETE /menu[/{id}] (admin)          -> {id} | 204 | 409 if ordered
POST /orders       {lines:[{code,quantity}]}        -> {id, total_paise}
GET  /orders       (self; admin ?user_id= or ?status=) -> {orders:[...]}
GET  /orders/{id}  (owner or admin)                  -> order + items
POST /orders/{id}/cancel                            -> {balance_paise}
GET  /queue?lane=a|kitchen|ready                    -> {head, depth, items:[ids]}

Login rate limit: LOGIN_MAX_ATTEMPTS (default 10) per LOGIN_WINDOW (default 1m)
per principal -> 429; a successful login resets the counter.
Admin bootstrap: ADMIN_PRINCIPAL + ADMIN_PASSWORD env, upserted each start.

## 7. Queue read logic
Single indexed query groups pending orders by id and counts non-MEAL lines via
join order_items->menu.code. lane=a => non_meal=0; lane=kitchen => non_meal>0;
lane=ready => ready_orders joined to pending orders, ordered by ready_at.
Empty => {head:null, depth:0, items:[]}.

## 8. Coordination points / risks
- database/schema.sql is the DB teammate's file; the two appended tables are
  additive and should be communicated before merge.
- Lane derivation depends on current menu.code; a later rename re-attributes
  history. Snapshot lane at order time if this matters.
- PIN login is low entropy -> min length enforced (>=4); further per-IP / lockout
  hardening possible later.
- New deps: golang-jwt/jwt/v5, golang.org/x/crypto (bcrypt). First non-modernc deps.
- JWT_SECRET must come from env, never committed.
- CORS needed only if the frontend is a browser app (CORS_ORIGINS).

## 9. Deferred / future work
- MQTT bridge: define the shared interface so kitchen/done can mark ready and
  collected/counter-a served can CompleteOrder. Decide same-process vs separate.
- Clear ready_orders on collect/cancel (cosmetic; pending filter already hides
  completed orders from the ready lane).
- Freeze the API contract with the frontend teammate (browser app => CORS).
- Lane snapshot at order time (needs orders.lane column + DB-owner coordination).
- Integration tests against a running broker.

## 10. Build & test
  cd food-kiosk
  go run .                 # creates ./food-kiosk.db, starts server
  go test ./...            # database tests + new server tests
  CGO_ENABLED=1 go test -race ./...
