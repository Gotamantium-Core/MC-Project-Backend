# Food Kiosk — Backend

Prepaid-credit food kiosk backend. Customers top up a balance, order items by
their menu code, and the kiosk serves them from a pending queue.

Go 1.27, SQLite via `modernc.org/sqlite` (pure Go, no cgo). No `.db` file is
committed; the schema is embedded and created on first run.

```
go run .                                        # creates ./food-kiosk.db
go test ./...                                   # 60 tests
go test ./... -coverprofile=cover.out           # ~85% of statements
CGO_ENABLED=1 go test -race ./...               # needs a C toolchain
```

## Conventions you need to know

**All money is in paise (`int64`), never rupees and never a float.** 1 rupee =
100 paise. The column names all end in `_paise` to make that unavoidable. Divide
by 100 only at the display layer, and format the result rather than printing the
raw quotient.

**Amounts are `int64` everywhere.** `quantity` is `int64` too, so the struct
fields, the SQL columns, and the arithmetic all agree.

**Snapshot pricing.** `order_items` stores its own copy of `item_name` and
`unit_price_paise` at the moment the order is placed. Editing or deleting a menu
item later never changes what a customer was charged.

**Errors are sentinels.** Match with `errors.Is`, never by string:

| Sentinel | Meaning | Suggested HTTP |
| --- | --- | --- |
| `ErrNotFound` | no such row | 404 |
| `ErrInvalidInput` | rejected before hitting the DB; nothing was written | 400 |
| `ErrConflict` | row exists but is in the wrong state for this change | 409 |
| `ErrInsufficientBalance` | prepaid credit will not cover the order | 402 |

`ErrConflict` covers three sources. Two are state checks in Go (completing an
already cancelled order). The third is a schema constraint the caller could not
have pre-empted, because it depends on rows they did not send: a `UNIQUE`
violation such as a duplicate `users.roll_no` or `menu.code`, or a `FOREIGN KEY`
restriction such as deleting a user who still has history. `asConflict`
(`errors.go`) translates the driver's error into `ErrConflict` while keeping the
original in the chain, so a duplicate is a 409 rather than an opaque 500. It
only fires on a genuine constraint failure — a locked or unreadable database is
returned untouched rather than dressed up as a conflict.

**`DB` is an interface, not `*sql.DB`.** Both `*sql.DB` and `*sql.Tx` satisfy it,
which is what lets the order helpers run inside a transaction. All read helpers
take `DB`; only `CreateOrder` and `DeleteOrder` take a concrete `*sql.DB`,
because each needs to open a transaction — `DeleteOrder` because its refund must
commit atomically with the delete.

## Database schema

```
users
  ├── id               [PK]
  ├── roll_no          [UNIQUE] — the person's identifier, canonicalised
  │                     (trimmed, upper-cased) and otherwise unconstrained
  ├── name             [non-blank]
  ├── phone            (nullable)
  └── created_at

menu
  ├── id               [PK]
  ├── item_name        [NOT NULL, non-blank]
  ├── code             [UNIQUE, non-blank] — how a customer orders
  ├── price_paise      [>= 0]
  ├── is_available     [0 | 1, default 1]
  └── created_at

orders
  ├── id               [PK]
  ├── user_id          [FK -> users.id, ON DELETE RESTRICT]
  ├── status           [pending | completed | cancelled, default pending]
  ├── total_paise      [>= 0] — must equal the sum of its line totals
  ├── created_at
  └── completed_at     (nullable)

order_items
  ├── id               [PK]
  ├── order_id         [FK -> orders.id, ON DELETE CASCADE]
  ├── menu_id          [FK -> menu.id, ON DELETE RESTRICT]
  ├── item_name        — snapshot of the name
  ├── unit_price_paise — snapshot of the price
  ├── quantity         [> 0]
  └── line_total_paise [= unit_price_paise * quantity]

transactions
  ├── id               [PK]
  ├── user_id          [FK -> users.id, ON DELETE RESTRICT]
  ├── order_id         [FK -> orders.id, ON DELETE SET NULL, nullable]
  ├── amount_paise     [> 0] — never 0; a free order writes no row
  ├── transaction_type [credit | debit]
  └── created_at
```

A user's **balance** is `SUM(credits) - SUM(debits)` over their transactions.
`credit` adds (top-up, refund), `debit` takes (paying for an order).

### Integrity rules worth knowing

- **An ordered menu item cannot be deleted.** `order_items.menu_id` is
  `ON DELETE RESTRICT`, so deleting an item that appears in any order fails with
  `ErrConflict`. Retire it with `SetMenuItemAvailability(ctx, db, id, 0)`
  instead — that hides it from the kiosk while leaving history intact, and is
  reversible, so the item can come back when it returns.
- **`is_available` is a display flag, not access control.** It removes an item
  from `ListAvailableMenuItems` so the kiosk stops offering it, but
  `CreateOrder` does **not** check it: someone who already has the code can still
  order a retired item, and it is priced and recorded like any other. That is
  deliberate — the flag exists to hide a sold-out item from the screen, not to
  refuse a customer. `TestRetiredItemHidesButStaysOrderable` pins this; if you
  want orders refused instead, filter on `is_available` in
  `GetMenuItemsByCodes` and invert that test.
- **A user with order history cannot be deleted** (`ON DELETE RESTRICT`), which
  surfaces as `ErrConflict`. `DeleteUser` reports it rather than forcing it. A
  top-up alone is enough to block the delete, since the ledger is what the
  balance is computed from.
- **`line_total_paise` is CHECK-constrained** to equal
  `unit_price_paise * quantity`, and `is_available` to `0` or `1`. These are the
  last line of defence if a future code path skips the Go validation.
- **FK columns are indexed explicitly.** SQLite does not index them automatically
  the way MySQL does.

### Before this is deployed

Two naming decisions are cheap now and expensive later. Neither blocks work today,
but both get materially harder once a real kiosk database exists, because a rename
is a non-additive schema change that `CREATE TABLE IF NOT EXISTS` will not retrofit
— it needs `user_version` bumped together with `schemaVersion`, plus a real
migration for anything already in the field.

- **`users.roll_no` is misnamed for what it now holds.** It is the unique
  identifier for any person, and faculty identifiers are not roll numbers. A
  neutral `identifier` (or `member_id`) would fit both. It touches the column, the
  `User.RollNo` field, `GetUserByRollNo`, and every `roll_no` query.
- **Upper-casing assumes a single case convention.** It is right for roll numbers
  and probably for a faculty employee ID, but if an identifier system is genuinely
  case-sensitive then normalising merges two real people into one account. The fix
  is to drop the `ToUpper` and keep the trim.

### Order lifecycle

```
pending ──CompleteOrder──> completed
   │                          │
   └────CancelOrder───────────┴──> cancelled   (refunds if it was paid)
```

`cancelled` is terminal. Cancelling an order that was paid writes the matching
`credit` in the same transaction, so the status and the balance can never
disagree.

```
                     ┌──CancelOrder──> cancelled
pending ─────────────┤
                     └──CompleteOrder─> completed ──┐
                                                    ├──DeleteOrder──> (gone)
(cancelled) ─────────────────────────────────────────┘   refunds what is
                                                        still outstanding
```

`DeleteOrder` sits outside the status machine: it accepts any state, purges the
order, and nets the ledger so it cannot pay out twice. Prefer `CancelOrder` for
ordinary mistakes — it keeps the order and its ledger visible.

Note the current model: **`pending` means paid but not yet handed over.**
`CreateOrder` debits the balance as part of the same atomic write. If the kiosk
should instead take payment at fulfilment, remove the debit from `CreateOrder`
and split out a separate payment step.

### Two SQLite settings worth mentioning

Both live in the DSN built by `dsn()` in `db.go`, and both are load-bearing:

- **`_pragma=foreign_keys(1)`** — SQLite scopes `PRAGMA foreign_keys` to a single
  connection. Issuing it once against the pool protects only whichever connection
  happened to be created first; any later connection would silently run with
  enforcement **off**, which is exactly the condition order history depends on.
  The DSN applies it to every connection the pool opens. It is deliberately
  absent from `schema.sql`, which cannot set connection state.
  `TestForeignKeysAreEnforcedOnEveryConnection` covers this.
- **`_txlock=immediate`** — takes the write lock at `BEGIN` instead of upgrading
  from a read lock partway through. Without it, a transaction can fail halfway
  with `SQLITE_BUSY` when it tries to upgrade, and the prepaid balance check
  could interleave between two concurrent orders. This is what keeps
  `TestConcurrentOrdersCannotOverdraw` correct even if `MaxOpenConns` is raised
  above 1.

`Open` also pins `MaxOpenConns(1)`, which is appropriate for a single kiosk.

### Schema versioning

`schema.sql` stamps `PRAGMA user_version = 1`, and `Initialize` refuses to run
against a database stamped higher than the binary supports. This matters because
`CREATE TABLE IF NOT EXISTS` silently skips tables that already exist — without
the guard, opening an old kiosk database with a newer binary would report success
while leaving the old, un-hardened tables in place.

Bump `user_version` in `schema.sql` **and** `schemaVersion` in `db.go` together
whenever the schema changes. Note that `CREATE TABLE IF NOT EXISTS` will not
retrofit a new constraint onto an existing table, so a non-additive change still
needs a real migration for kiosks already in the field.

## Functions

All files are in the `database` directory. All take a `context.Context` first.

### Connection (db.go)

- `Open(path) (*sql.DB, error)` — opens the database, pinned to one connection
- `Initialize(db) error` — checks the schema version, then applies `schema.sql`

### Users (users.go)

- `CreateUser(ctx, db, rollNo, name, phone) (int64, error)` — rejects a blank
  roll number or name; a duplicate `rollNo` returns `ErrConflict`
- `GetUserByID(ctx, db, id) (*User, error)`
- `GetUserByRollNo(ctx, db, rollNo) (*User, error)` — accepts the identifier in
  any casing, with or without surrounding whitespace
- `UpdateUser(ctx, db, user User) error` — overwrites roll number, name, phone.
  Renaming onto a taken roll number returns `ErrConflict`
- `DeleteUser(ctx, db, id) error` — refuses with `ErrConflict` while the user has
  any order or ledger row (`ON DELETE RESTRICT`). There is no force-delete:
  anonymise by blanking the name and phone and keeping the row, which preserves
  the ledger.
- `ListUsers(ctx, db) ([]User, error)`

**`roll_no` is the person's unique identifier, and nothing more.** Its shape is
deliberately not validated: the `TVE24CSXXX` form is a student convention, not a
rule, and a hardcoded check would reject a real person rather than catch a typo.
Faculty will need their own identifier, and uniqueness is the only property that
has to hold for both. So the column holds whatever identifier the kiosk is given,
and `users.roll_no` is a name that will need revisiting before anything is
deployed — see below.

What *is* enforced is that it is non-blank, and that it is **canonicalised**:
`normalizeRollNo` trims surrounding whitespace and upper-cases the value, on
every write and on the lookup. This is what makes `UNIQUE` mean one person per
account. SQLite compares `TEXT` with `BINARY` collation, so without it
`TVE24CS001`, `tve24cs001` and `TVE24CS001 ` are three distinct values that the
index happily accepts — and a student who typed their roll number in the wrong
case at the kiosk silently gets a second account with a zero balance, with their
existing credit appearing to have vanished.

Normalising in Go rather than with `COLLATE NOCASE` keeps this out of the schema,
so `user_version` does not have to move and no kiosk needs a migration. It covers
every write that goes through this package; it does **not** help rows written
before this change or by hand, which would need a dedupe pass before uniqueness
can be trusted. There is no deployed database yet, so there is nothing to dedupe
today — but this is the thing to do first if that ever changes.

### Menu (menu.go)

- `CreateMenuItem(ctx, db, itemName, code, pricePaise, isAvailable) (int64, error)`
  — a duplicate `code` returns `ErrConflict`
- `GetMenuItemByID(ctx, db, id) (*Menu, error)`
- `GetMenuItemByCode(ctx, db, code) (*Menu, error)`
- `GetMenuItemsByCodes(ctx, db, codes) (map[string]Menu, error)` — prices a whole
  cart in one query. Keys the result by code, collapses duplicate codes, and
  returns `ErrNotFound` listing any unknown code rather than silently dropping
  it, so a partially valid cart cannot become a partial order.
- `UpdateMenuItem(ctx, db, item Menu) error` — full overwrite of name, code,
  price, availability. Renaming onto a taken code returns `ErrConflict`
- `SetMenuItemAvailability(ctx, db, id, isAvailable) error` — partial update; use
  this to retire an item rather than a read-modify-write. Reversible
- `DeleteMenuItem(ctx, db, id) error` — returns `ErrConflict` if the item appears
  in any order
- `ListMenuItems(ctx, db) ([]Menu, error)` — everything, for admin screens
- `ListAvailableMenuItems(ctx, db) ([]Menu, error)` — the customer-facing menu

### Orders (orders.go)

- `CreateOrder(ctx, db *sql.DB, userID, lines []OrderLine) (int64, error)` —
  **atomic.** Writes the order, one row per line, and the balance debit together,
  or rolls all of it back. Merges duplicate codes by summing quantities. Returns
  `ErrInsufficientBalance` if the balance will not cover the total.
- `GetOrderByID(ctx, db, id) (*Order, error)` — the only order read that attaches
  `Items`; it also re-checks that the total matches the sum of the lines
- `ListOrderItems(ctx, db, orderID) ([]OrderItem, error)`
- `GetOrderItemByID(ctx, db, id) (*OrderItem, error)`
- `ListOrderItemsByMenuItem(ctx, db, menuID) ([]OrderItem, error)` — every line
  ever priced from one menu item, oldest first. Reads `idx_order_items_menu_id`
  and answers "what has this sold in" without a table scan. It returns the
  **snapshot**, so a repriced item still reports what customers were charged.
- `ListOrdersByUser(ctx, db, userID) ([]Order, error)` — newest first, no items
- `ListOrdersByStatus(ctx, db, status) ([]Order, error)` — the service queue
- `CompleteOrder(ctx, db, orderID) error` — stamps `completed_at`
- `CancelOrder(ctx, db, orderID) error` — refunds in the same transaction
- `DeleteOrder(ctx, db, orderID) error` — **atomic, and requires a `*sql.DB`**,
  not the `DB` interface, because the refund and the delete share one
  transaction. Refunds only what the order still *owes*, then purges the order
  and cascades its lines away. Prefer `CancelOrder` for ordinary mistakes; this
  is for purging outright.

The list helpers deliberately leave `Items` nil to avoid an N+1 query; call
`GetOrderByID` for detail. Every list helper returns `nil`, not an empty slice,
when nothing matches.

### `DeleteOrder` and the double-refund question

Deleting a paid order would otherwise leave its debit in the ledger with
`order_id` set to `NULL` — the customer's money gone with no order to show for
it. So `DeleteOrder` credits back the **outstanding** amount (debits minus
credits already written for that order) in the same transaction as the delete.
Once it returns `nil` the balance is exactly what it was before the order
existed.

Netting rather than refunding the raw debit total is what makes it safe to call
twice over a lifecycle:

| Order state at delete | Outstanding | Refund written |
| --- | --- | --- |
| `pending` or `completed` | full total | full total |
| `cancelled` (already refunded) | 0 | nothing — no second refund |
| free cart (never charged) | 0 | nothing — the ledger rejects 0 anyway |
| ledger hand-edited to over-credit | negative | nothing — never debit the customer to correct a bookkeeping mistake |

The credit is written while `order_id` still resolves, so `ON DELETE SET NULL`
detaches it and it survives as an ordinary refund on the balance. Order lines
are immutable once written, so nothing ever needs re-pricing: the `DELETE`
cascades them away.

### Balance and ledger (orders.go)

- `TopUpUser(ctx, db, userID, amountPaise) (int64, error)` — the only way a
  balance increases other than a refund
- `GetUserBalancePaise(ctx, db, userID) (int64, error)` — credits minus debits.
  May be negative if the ledger was overridden by hand; treat that as no credit.
- `ListTransactionsByUser(ctx, db, userID) ([]Transaction, error)` — newest first
- `ListTransactionsByOrder(ctx, db, orderID) ([]Transaction, error)` — oldest
  first; the debit and any refund, which is how you check that an order settled
  correctly. Reads `idx_transactions_order_id`.
- `ListTransactions(ctx, db) ([]Transaction, error)` — the whole ledger across
  all users, newest first. Unbounded on purpose: silently truncating an audit
  trail is worse than a long slice.
- `GetTransactionByID(ctx, db, id) (*Transaction, error)`

A transaction with `order_id` NULL is a top-up and belongs to no order, so
`ListTransactionsByOrder` does not return it.

### Input limits

`CreateOrder` rejects a cart that is empty, has a blank code, a quantity `<= 0`,
a quantity above `maxQuantityPerLine` (100), or more than `maxLinesPerOrder`
(50) lines. Line totals and the order total are computed with overflow-checked
arithmetic.

Duplicate codes are merged before any of that is checked, so the 100 cap applies
to the **combined** quantity for a code, not to each line: 60 + 60 of the same
item is rejected even though neither line is over the cap on its own. A price is
only constrained to `>= 0`, so an absurd menu price is caught by the checked
arithmetic rather than by a limit on the price itself.

## Testing

`go test ./...` — 60 tests, no external dependencies. Each test gets a throwaway
database in `t.TempDir()` via `openTestDB` (`testdb_test.go`); `seedUser` and
`seedMenu` (`orders_test.go`) build the usual fixtures.

Tests worth knowing about, because they guard decisions that are easy to undo by
accident:

- `TestForeignKeysAreEnforcedOnEveryConnection` — opens a database *without*
  initializing it, so the only thing that can enable FKs is the DSN. Verified by
  mutation: remove `_pragma=foreign_keys(1)` and it fails.
- `TestCreateOrderInsufficientBalanceRollsBack` / `TestCreateOrderUnknownCodeRollsBack`
  — assert no order, no items, and an unchanged balance after a failure.
- `TestCreateOrderSnapshotsMenuPrices` — repricing a menu item afterwards must not
  change the stored order.
- `TestSchemaRejectsBadMoney` — drives the CHECK constraints directly, since they
  are the backstop if Go validation is ever bypassed.
- `TestConcurrentOrdersCannotOverdraw` — ten parallel orders against credit for
  three; run with `-race` where a C toolchain is available.
- `TestInitializeRejectsNewerSchema` — the `user_version` guard.

The money arithmetic is where the subtle bugs would be, so it is tested from both
ends:

- `TestCreateOrderRejectsOverflowingTotals` — a legal-but-absurd price makes
  `price * quantity`, or the sum across lines, exceed `int64`. Both must return
  `ErrInvalidInput` and write nothing; wrapping would store a negative total.
- `TestCreateOrderRejectsCombinedQuantityOverCap` — the 100-per-code cap applies
  **after** duplicate codes merge, so 60 + 60 is rejected while 100 in one line
  is accepted.
- `TestGetOrderByIDRejectsTotalMismatch` — `orders.total_paise` cannot be
  constrained against its own lines in SQL, so the test tampers with the total
  and asserts the Go-side cross-check catches it.

Deletion and refund accounting, where a double refund would be a real financial
bug:

- `TestDeleteOrderRefundsAndRemovesItems` — the core promise: money back, order
  gone, ledger rows survive detached.
- `TestDeleteCancelledOrderDoesNotRefundTwice` — cancelling already refunded, so
  deleting afterwards must net to zero and write no fourth row.
- `TestDeleteCompletedOrderRefunds` — a handed-over order that turned out wrong
  still owes a refund.
- `TestDeleteFreeOrderWritesNoLedgerRow` — nothing was charged, so nothing is
  credited.
- `TestDeleteOrderWithOverCreditedLedgerTakesNothingBack` — a hand-edited ledger
  that over-credited an order must not lead to a debit against the customer.
- `TestDeleteUserWithHistoryIsRestricted` — either an order or a bare top-up is
  enough to block the delete, and it arrives as `ErrConflict`.

Constraint failures are the errors Go cannot pre-empt, so they are tested as
sentinels rather than as "not nil":

- `TestAsConflictMapsOnlyConstraintFailures` — the mapping itself, against a stub
  error: `UNIQUE`, `FOREIGN KEY`, `CHECK`, `NOT NULL` and `PRIMARY KEY` become
  `ErrConflict`, while `BUSY` and I/O errors are returned untouched so a locked
  database is never reported as a conflict.
- `TestAsConflictSurvivesWrapping` — the driver error is wrapped on its way out
  of the helper, so the code check has to see through that layer.
- `TestUpdateMenuItemOntoTakenCodeIsConflict` / `TestUpdateUserOntoTakenRollNoIsConflict`
  — a rename cannot steal another row's unique identifier, and the rejected
  rename must not partially apply.
- `TestDeleteOrderedMenuItemIsConflict` — the order history is what makes the
  delete illegal, so the refusal is a 409, not an unexpected failure.
- `TestCreateUserRejectsBlankIdentity` — `NOT NULL` does not catch `"  "`, so
  blank roll numbers and names are rejected explicitly, on create and on update.

Identifier uniqueness, where SQLite's `BINARY` collation would otherwise let one
person hold several accounts:

- `TestRollNoIsNormalized` — messy input lands stored as `TVE24CS001`, every
  casing of it finds the same row, and re-registering in any casing is
  `ErrConflict` rather than a second account. Verified by mutation: make
  `normalizeRollNo` return its input and all three normalisation tests fail.
- `TestUpdateUserNormalizesRollNo` — `UpdateUser` is a full overwrite, so it is
  the other way a differently-cased identifier could reach the table.
- `TestRollNoLookupRejectsBlank` — a blank identifier is `ErrInvalidInput`, not
  `ErrNotFound`, so a caller cannot mistake "you typed nothing" for "no such
  student".

`TestRetiredItemHidesButStaysOrderable` pins the availability contract described
under Integrity rules: hidden from the customer menu, still orderable by code,
still restorable with its price and history intact.

Coverage is about 85% of statements. The uncovered remainder is almost entirely
the driver-level error branches (`LastInsertId`, `RowsAffected`, query and scan
failures), which need a mock `sql.DB` to reach, plus the arithmetic paths that
are unreachable in practice (e.g. a negative `checkedAdd`, which no caller
passes).
