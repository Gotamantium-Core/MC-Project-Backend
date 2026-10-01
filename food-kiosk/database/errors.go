package database

import (
	"errors"
	"fmt"
)

// Sentinel errors for this package. Callers should match them with errors.Is
// rather than comparing error strings, and map them to transport-level codes:
//
//	ErrNotFound            -> 404
//	ErrInvalidInput        -> 400
//	ErrConflict            -> 409
//	ErrInsufficientBalance -> 402 / 409
var (
	// ErrNotFound means the requested row does not exist.
	ErrNotFound = errors.New("not found")

	// ErrInvalidInput means the arguments were rejected before reaching the
	// database, so no partial write can have occurred.
	ErrInvalidInput = errors.New("invalid input")

	// ErrConflict means the row exists but is not in a state that allows the
	// requested change, e.g. completing an already cancelled order.
	ErrConflict = errors.New("conflicting state")

	// ErrInsufficientBalance means the user's prepaid credit does not cover the
	// order total. No rows were written.
	ErrInsufficientBalance = errors.New("insufficient balance")
)

// sqliteConstraint is SQLite's primary result code for any constraint failure.
// The driver reports extended codes (2067 for UNIQUE, 1811 for a foreign key),
// but those share this primary code in their low byte, so masking on it catches
// every kind without hard-coding each variant.
const sqliteConstraint = 19

// errorCoder is satisfied by the driver's *sqlite.Error. Matching on the
// interface rather than the concrete type keeps this file free of a direct
// dependency on a particular SQLite driver, so swapping the driver in db.go
// does not break the sentinel mapping here.
type errorCoder interface {
	Code() int
}

// asConflict marks a database-level constraint failure as ErrConflict, so a
// caller can reach it with errors.Is instead of matching on the driver's message.
//
// The schema rejects two kinds of write that Go validation cannot pre-empt,
// because they depend on rows the caller did not send:
//
//   - UNIQUE violated, e.g. a duplicate users.roll_no or menu.code
//   - FOREIGN KEY RESTRICT, e.g. deleting a user who still has order or ledger
//     history
//
// Both mean "the row conflicts with what is already stored", which is the 409
// the sentinels document. The original error is kept in the chain so the detail
// is not lost.
//
// A non-constraint error is returned unchanged: this must not manufacture a
// conflict out of an unrelated failure such as a locked database.
func asConflict(err error) error {
	if err == nil {
		return nil
	}

	var coder errorCoder
	if errors.As(err, &coder) && coder.Code()&0xff == sqliteConstraint {
		return fmt.Errorf("%w: %w", ErrConflict, err)
	}

	return err
}
