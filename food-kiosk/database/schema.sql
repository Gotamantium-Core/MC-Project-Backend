-- Bump this whenever the schema below changes in a way that is not additive.
-- It is read back by Initialize to refuse to run a newer schema against an
-- older database file, which CREATE TABLE IF NOT EXISTS would otherwise do
-- silently (it skips existing tables and leaves them un-hardened).
PRAGMA user_version = 1;

-- foreign_keys is intentionally NOT set here. It is a per-connection setting,
-- and a PRAGMA issued by this script would only apply to whichever single
-- connection happened to run it. Open puts it in the DSN instead so every
-- pooled connection gets it.

CREATE TABLE IF NOT EXISTS users (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    roll_no TEXT NOT NULL UNIQUE,
    name TEXT NOT NULL,
    phone TEXT,
    created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS menu (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    item_name TEXT NOT NULL CHECK (length(trim(item_name)) > 0),
    code TEXT NOT NULL UNIQUE CHECK (length(trim(code)) > 0),
    -- Money is stored in paise (1 rupee = 100 paise) as INTEGER to avoid
    -- floating point rounding. Never divide by 100 for display without
    -- formatting explicitly.
    price_paise INTEGER NOT NULL CHECK (price_paise >= 0),
    is_available INTEGER NOT NULL DEFAULT 1 CHECK (is_available IN (0, 1)),
    created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS orders (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    user_id INTEGER NOT NULL,
    status TEXT NOT NULL DEFAULT 'pending'
        CHECK (status IN ('pending', 'completed', 'cancelled')),
    -- Cross-row invariant: must equal the sum of the order's line totals.
    -- Enforced in Go inside the creating transaction, since CHECK cannot
    -- reference another table.
    total_paise INTEGER NOT NULL DEFAULT 0 CHECK (total_paise >= 0),
    created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
    -- Only meaningful once status = 'completed'. Deliberately not constrained
    -- to that combination: a completed order may later be cancelled and
    -- refunded, which must keep its original completion timestamp.
    completed_at TEXT,

    -- RESTRICT (not CASCADE) so a user with order history cannot be deleted.
    FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE RESTRICT
);

CREATE TABLE IF NOT EXISTS order_items (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    order_id INTEGER NOT NULL,
    -- item_name and unit_price_paise are a deliberate snapshot of the menu row
    -- at order time, so later menu edits never rewrite historical orders.
    menu_id INTEGER NOT NULL,
    item_name TEXT NOT NULL,
    unit_price_paise INTEGER NOT NULL CHECK (unit_price_paise >= 0),
    quantity INTEGER NOT NULL CHECK (quantity > 0),
    line_total_paise INTEGER NOT NULL CHECK (line_total_paise >= 0),

    CHECK (line_total_paise = unit_price_paise * quantity),

    -- Deleting an order removes its items; the ledger keeps the record.
    FOREIGN KEY (order_id) REFERENCES orders(id) ON DELETE CASCADE,
    -- RESTRICT is the point: an item that has been ordered must not be deleted,
    -- or the order history would point at nothing. Retire it instead by
    -- setting is_available = 0.
    FOREIGN KEY (menu_id) REFERENCES menu(id) ON DELETE RESTRICT
);

CREATE TABLE IF NOT EXISTS transactions (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    user_id INTEGER NOT NULL,
    -- NULL for a top-up that is not tied to any order.
    order_id INTEGER,
    amount_paise INTEGER NOT NULL CHECK (amount_paise > 0),
    -- credit adds to the user's balance (top-up, refund), debit takes from it.
    transaction_type TEXT NOT NULL CHECK (transaction_type IN ('credit', 'debit')),
    created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,

    FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE RESTRICT,
    -- SET NULL: the ledger outlives the order it refers to.
    FOREIGN KEY (order_id) REFERENCES orders(id) ON DELETE SET NULL
);

-- SQLite does not index foreign key columns automatically (unlike MySQL), and
-- every kiosk query filters a user, an order, or the pending queue.
CREATE INDEX IF NOT EXISTS idx_orders_user_id ON orders(user_id);
CREATE INDEX IF NOT EXISTS idx_orders_status ON orders(status, id);
CREATE INDEX IF NOT EXISTS idx_order_items_order_id ON order_items(order_id);
CREATE INDEX IF NOT EXISTS idx_order_items_menu_id ON order_items(menu_id);
CREATE INDEX IF NOT EXISTS idx_transactions_user_id ON transactions(user_id, id);
CREATE INDEX IF NOT EXISTS idx_transactions_order_id ON transactions(order_id);
