# Food Kiosk — Backend

Prepaid-credit food kiosk backend. Customers top up a balance, order items by
their menu code, and the kiosk serves them from a pending queue.

Go 1.27, SQLite via `modernc.org/sqlite` (pure Go, no cgo). No `.db` file is
committed; the schema is embedded and created on first run.

```
go run .                                        # creates ./food-kiosk.db
go test ./...                                   # 32 tests
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

**`DB` is an interface, not `*sql.DB`.** Both `*sql.DB` and `*sql.Tx` satisfy it,
which is what lets the order helpers run inside a transaction. All read helpers
take `DB`; only `CreateOrder` takes a concrete `*sql.DB`, because it needs to open
a transaction.

## Database schema

```
users
  ├── id               [PK]
  ├── roll_no          [UNIQUE] (TVE24CSXXX format)
  ├── name
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
  `ON DELETE RESTRICT`, so deleting an item that appears in any order fails.
  Retire it with `SetMenuItemAvailability(ctx, db, id, 0)` instead — that hides
  it from the kiosk while leaving history intact.
- **A user with order history cannot be deleted** (`ON DELETE RESTRICT`).
- **`line_total_paise` is CHECK-constrained** to equal
  `unit_price_paise * quantity`, and `is_available` to `0` or `1`. These are the
  last line of defence if a future code path skips the Go validation.
- **FK columns are indexed explicitly.** SQLite does not index them automatically
  the way MySQL does.

### Order lifecycle

```
pending ──CompleteOrder──> completed
   │                          │
   └────CancelOrder───────────┴──> cancelled   (refunds if it was paid)
```

`cancelled` is terminal. Cancelling an order that was paid writes the matching
`credit` in the same transaction, so the status and the balance can never
disagree.

Note the current model: **`pending` means paid but not yet handed over.**
`CreateOrder` debits the balance as part of the same atomic write. If the kiosk
should instead take payment at fulfilment, remove the debit from `CreateOrder`
and split out a separate payment step.

### Two SQLite settings that are easy to get wrong

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

- `CreateUser(ctx, db, rollNo, name, phone) (int64, error)`
- `GetUserByID(ctx, db, id) (*User, error)`
- `GetUserByRollNo(ctx, db, rollNo) (*User, error)` — `rollNo` is `TVE24CSXXX`
- `UpdateUser(ctx, db, user User) error` — overwrites roll number, name, phone
- `ListUsers(ctx, db) ([]User, error)`

### Menu (menu.go)

- `CreateMenuItem(ctx, db, itemName, code, pricePaise, isAvailable) (int64, error)`
- `GetMenuItemByID(ctx, db, id) (*Menu, error)`
- `GetMenuItemByCode(ctx, db, code) (*Menu, error)`
- `GetMenuItemsByCodes(ctx, db, codes) (map[string]Menu, error)` — prices a whole
  cart in one query. Keys the result by code, collapses duplicate codes, and
  returns `ErrNotFound` listing any unknown code rather than silently dropping
  it, so a partially valid cart cannot become a partial order.
- `UpdateMenuItem(ctx, db, item Menu) error` — full overwrite of name, code,
  price, availability
- `SetMenuItemAvailability(ctx, db, id, isAvailable) error` — partial update; use
  this to retire an item rather than a read-modify-write
- `DeleteMenuItem(ctx, db, id) error` — fails if the item appears in any order
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
- `ListOrdersByUser(ctx, db, userID) ([]Order, error)` — newest first, no items
- `ListOrdersByStatus(ctx, db, status) ([]Order, error)` — the service queue
- `CompleteOrder(ctx, db, orderID) error` — stamps `completed_at`
- `CancelOrder(ctx, db, orderID) error` — refunds in the same transaction

The list helpers deliberately leave `Items` nil to avoid an N+1 query; call
`GetOrderByID` for detail.

### Balance and ledger (orders.go)

- `TopUpUser(ctx, db, userID, amountPaise) (int64, error)` — the only way a
  balance increases other than a refund
- `GetUserBalancePaise(ctx, db, userID) (int64, error)` — credits minus debits.
  May be negative if the ledger was overridden by hand; treat that as no credit.
- `ListTransactionsByUser(ctx, db, userID) ([]Transaction, error)` — newest first

### Input limits

`CreateOrder` rejects a cart that is empty, has a blank code, a quantity `<= 0`,
a quantity above `maxQuantityPerLine` (100), or more than `maxLinesPerOrder`
(50) lines. Line totals and the order total are computed with overflow-checked
arithmetic.

## Testing

`go test ./...` — 32 tests, no external dependencies. Each test gets a throwaway
database in `t.TempDir()` via `openTestDB` (`testdb_test.go`).

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

Uncovered paths are the driver-level error branches (`LastInsertId`,
`RowsAffected`, scan failures), which need a mock `sql.DB` to reach.
