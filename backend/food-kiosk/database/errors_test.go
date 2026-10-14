package database

import (
	"errors"
	"testing"
)

// stubCoder stands in for the driver's *sqlite.Error, which is the only thing
// asConflict inspects. Using a stub keeps this test about the mapping itself.
type stubCoder struct {
	code int
	msg  string
}

func (e *stubCoder) Error() string { return e.msg }
func (e *stubCoder) Code() int     { return e.code }

func TestAsConflictMapsOnlyConstraintFailures(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		conflict bool
	}{
		{name: "nil stays nil", err: nil, conflict: false},
		{name: "unique violation", err: &stubCoder{code: 2067, msg: "UNIQUE constraint failed: users.roll_no"}, conflict: true},
		{name: "foreign key violation", err: &stubCoder{code: 1811, msg: "FOREIGN KEY constraint failed"}, conflict: true},
		{name: "check violation", err: &stubCoder{code: 275, msg: "CHECK constraint failed: menu.price_paise"}, conflict: true},
		{name: "not null violation", err: &stubCoder{code: 1299, msg: "NOT NULL constraint failed"}, conflict: true},
		{name: "primary key violation", err: &stubCoder{code: 1555, msg: "PRIMARY KEY constraint failed"}, conflict: true},
		{name: "busy is not a conflict", err: &stubCoder{code: 5, msg: "database is locked"}, conflict: false},
		{name: "io error is not a conflict", err: &stubCoder{code: 778, msg: "disk I/O error"}, conflict: false},
		{name: "plain error is untouched", err: errors.New("something else"), conflict: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := asConflict(tt.err)

			if tt.err == nil {
				if got != nil {
					t.Fatalf("asConflict(nil) = %v, want nil", got)
				}
				return
			}

			if errors.Is(got, ErrConflict) != tt.conflict {
				t.Fatalf("asConflict(%v) ErrConflict = %v, want %v", tt.err, !tt.conflict, tt.conflict)
			}

			// Whichever way it goes, the original cause must survive in the chain
			// so the driver's detail is not lost.
			if !errors.Is(got, tt.err) {
				t.Fatalf("asConflict(%v) = %v, dropped the original error", tt.err, got)
			}
		})
	}
}

// TestAsConflictSurvivesWrapping is the property the write paths depend on: the
// driver error is wrapped with %w on its way out of the helper, so the code
// check has to see through that layer.
func TestAsConflictSurvivesWrapping(t *testing.T) {
	driver := &stubCoder{code: 2067, msg: "UNIQUE constraint failed: users.roll_no"}
	wrapped := errors.Join(errors.New("insert user"), driver)

	if !errors.Is(asConflict(wrapped), ErrConflict) {
		t.Fatal("asConflict() did not see through the wrapping layer")
	}
}
