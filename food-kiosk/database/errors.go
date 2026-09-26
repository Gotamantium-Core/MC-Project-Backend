package database

import "errors"

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
