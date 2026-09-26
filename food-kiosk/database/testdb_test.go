package database

import (
	"database/sql"
	"path/filepath"
	"testing"
)

// openTestDB creates a throwaway database in a temp dir with the schema applied
// and closes it when the test finishes.
func openTestDB(t *testing.T) *sql.DB {
	t.Helper()

	db, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open test database: %v", err)
	}
	if err := Initialize(db); err != nil {
		db.Close()
		t.Fatalf("initialize test database: %v", err)
	}

	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Errorf("close test database: %v", err)
		}
	})

	return db
}
