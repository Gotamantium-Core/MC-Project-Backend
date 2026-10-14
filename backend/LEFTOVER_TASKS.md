# Leftover Tasks — Backend Server + Auth

Everything the "backend server and auth" role still owes, after the main
implementation is done. Companion to CONTEXT.md (context/decisions) and
MODIFICATIONS.md (what changed).

Status legend: [ ] pending · [x] done.

## 1. Coordination with other teammates (do before handoff)

[ ] **Freeze the MQTT bridge interface (with the MQTT teammate).**
    The mounting points are already in place and tested; only the contract
    needs agreeing.
    - kitchen `canteen/kitchen/done`  -> `SetOrderReady`   (`server/store.go`)
    - counter-b `canteen/counter-b/collected`, counter-a `canteen/counter-a/served`
        -> `database.CompleteOrder`  (`database/orders.go`)
    - Topics/payloads are the MQTT teammate's spec (CONTEXT.md section 1).
    - Decide: bridge runs in-process or as a separate process. `ready_orders`
      is a table, so either works — the separate process would import
      `food-kiosk/database` (+ its own copy of the ready helpers).

[ ] **Freeze the API contract with the frontend teammate.**
    The route table in `README.md` ("HTTP server and auth") is the draft.
    Frontend is a browser app -> set `CORS_ORIGINS` on the server.

## 2. Backend + auth — hardening (optional, low priority)

[ ] **Clear `ready_orders` on collect/cancel.**
    Cosmetic only: `ListReadyOrderIDs` (`server/store.go`) already filters on
    `orders.status='pending'`, so a completed/cancelled order drops out of the
    `ready` lane even if its row lingers. A collected/cancelled order that
    cleans up after itself keeps the table tidy.

[ ] **Per-IP login limiting / account lockout.**
    Done today: per-principal fixed-window limiter (`LOGIN_MAX_ATTEMPTS` per
    `LOGIN_WINDOW`, reset on success, `429`), min PIN length (>= 4). Not done:
    per-IP throttle, escalating lockout after repeated failures.

[ ] **Snapshot the lane at order time.**
    Currently derived at read time by joining `order_items -> menu.code`
    (`ListPendingOrderIDsByLane`). If a menu code is ever renamed, old orders
    re-attribute. Needs an `orders.lane` column (additive on create, but a
    non-additive change for an existing DB) + coordination with the DB owner.

[ ] **Integration tests against a running broker.**
    End-to-end across the MQTT seam once the bridge exists (needs section 1's
    interface decided first).

## 3. Not this role (for the record)

[ ] MQTT bridge binary + broker topics/LWT + dedup logic — MQTT teammate.
[ ] Reports & docs — reports role (the README/CONTEXT/MODIFICATIONS trio here
    are this role's own working notes, maintained during development).

## Housekeeping

[ ] **Commit and push.** Nothing has been committed: all of `food-kiosk/server/`,
    the `main.go`/`schema.sql` rewrites, and the root docs are either modified
    or untracked (`git status`). Review, stage the intended files, push the
    branch, and flag the `database/schema.sql` additive tables to the DB owner
    before merge (CONTEXT.md section 8).