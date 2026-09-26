package database

import (
	"context"
	"database/sql"
	_ "embed"
	"fmt"
	"path/filepath"
	"strings"

	_ "modernc.org/sqlite"
)

// Embed the schema.sql file into the compiled Go program.
//
//go:embed schema.sql
var schema string

// schemaVersion is the value stamped into schema.sql. Bump both together.
const schemaVersion = 1

// DB is the subset of *sql.DB used by this package. *sql.Tx satisfies it too,
// which is what lets the order helpers run inside a transaction without
// reaching back for the outer pool handle.
type DB interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// rowScanner is satisfied by both *sql.Row and *sql.Rows, so one scan helper
// serves both single-row and multi-row queries.
type rowScanner interface {
	Scan(dest ...any) error
}

// withTx runs fn inside a transaction, rolling back on any error or panic.
//
// The rule that makes this safe: fn must only ever touch the tx it is given,
// never the outer db. Open pins MaxOpenConns(1), so a query issued on the pool
// while a transaction holds the only connection would block forever.
func withTx(ctx context.Context, db *sql.DB, fn func(tx *sql.Tx) error) (err error) {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}

	defer func() {
		if p := recover(); p != nil {
			_ = tx.Rollback()
			panic(p)
		}
		if err != nil {
			_ = tx.Rollback()
		}
	}()

	if err = fn(tx); err != nil {
		return err
	}

	return tx.Commit()
}

func Open(path string) (*sql.DB, error) {
	db, err := sql.Open("sqlite", dsn(path))
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}

	// Appropriate for a small single-kiosk application.
	db.SetMaxOpenConns(1) // can change this in the future when we scale.

	// Check that the database is reachable.
	if err := db.Ping(); err != nil {
		db.Close()
		return nil, fmt.Errorf("ping database: %w", err)
	}

	return db, nil
}

// dsn builds the connection string.
//
// foreign_keys is applied through the DSN rather than a bare PRAGMA on purpose.
// SQLite scopes that pragma to a single connection, so the Exec in an older
// version of this file only protected whichever connection happened to be
// created first. Any later connection would have silently run with foreign key
// enforcement OFF, which is the exact condition this database relies on to keep
// order history intact. Passing it in the DSN applies it to every connection
// the pool opens.
//
// busy_timeout replaces lock contention errors with a wait, and _txlock=immediate
// takes the write lock at BEGIN so a transaction cannot fail halfway through
// with SQLITE_BUSY when it tries to upgrade from a read lock.
func dsn(path string) string {
	abs, err := filepath.Abs(path)
	if err != nil {
		// Not fatal: fall back to the path as given and let SQLite resolve it.
		abs = path
	}

	q := filepath.ToSlash(abs)
	if !strings.HasPrefix(q, "file:") {
		q = "file:" + q
	}

	return q + "?_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)&_txlock=immediate"
}

// Initialize applies the schema to a fresh or existing database.
func Initialize(db *sql.DB) error {
	version, err := readSchemaVersion(db)
	if err != nil {
		return err
	}
	if version > schemaVersion {
		return fmt.Errorf("database schema version %d is newer than supported version %d; upgrade the application", version, schemaVersion)
	}

	if _, err := db.Exec(schema); err != nil {
		return fmt.Errorf("initialize database: %w", err)
	}

	return nil
}

// readSchemaVersion returns the PRAGMA user_version of the open database, or 0
// for a database that has never been initialized.
func readSchemaVersion(db *sql.DB) (int, error) {
	var version int
	if err := db.QueryRow(`PRAGMA user_version`).Scan(&version); err != nil {
		return 0, fmt.Errorf("read schema version: %w", err)
	}
	return version, nil
}
