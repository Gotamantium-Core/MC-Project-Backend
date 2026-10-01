package database

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
)

type User struct {
	ID        int64
	RollNo    string
	Name      string
	Phone     *string
	CreatedAt string
}

// Create a new user and insert into the users table.
func CreateUser(ctx context.Context, db DB, rollNo string, name string, phone string) (int64, error) {
	rollNo, err := normalizeRollNo(rollNo)
	if err != nil {
		return 0, err
	}
	if err := validateUser(name); err != nil {
		return 0, err
	}

	result, err := db.ExecContext(ctx, `INSERT INTO users (roll_no, name, phone) VALUES (?, ?, ?)`, rollNo, name, phone)
	if err != nil {
		// A duplicate roll_no is a conflict, not a 500: the UNIQUE index is the
		// only thing standing between two rows claiming the same student.
		return 0, fmt.Errorf("insert user: %w", asConflict(err))
	}

	id, err := result.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("get inserted user ID: %w", err)
	}

	return id, nil
}

// normalizeRollNo canonicalises a person's identifier before it is stored or
// looked up: surrounding whitespace is trimmed and the value is upper-cased.
//
// This is what makes the UNIQUE index on roll_no mean one person per account.
// SQLite compares TEXT with BINARY collation, so without it "TVE24CS001",
// "tve24cs001" and "TVE24CS001 " are three distinct values and all three are
// accepted. Someone typing a roll number at the kiosk in the wrong case would
// silently get a second account with a zero balance, and their existing credit
// would look like it had vanished. That failure is invisible until the kiosk
// operator tries to reconcile it.
//
// Upper-casing rather than lower-casing keeps identifiers legible in the table
// and matches how they are printed on an ID card.
//
// Normalising here rather than with COLLATE NOCASE keeps this out of the schema,
// so user_version does not have to move and no kiosk in the field needs a
// migration. It covers every write that goes through this package. It does not
// help rows written before this change, or by hand: those need a dedupe pass
// before uniqueness can be trusted.
func normalizeRollNo(rollNo string) (string, error) {
	normalized := strings.ToUpper(strings.TrimSpace(rollNo))
	if normalized == "" {
		return "", fmt.Errorf("%w: roll number is empty", ErrInvalidInput)
	}
	return normalized, nil
}

// validateUser rejects a blank name before the write.
//
// The users table has NOT NULL on both columns, but that only rejects the empty
// string, not whitespace, and a row of "  " is unusable: the kiosk looks people
// up by this identifier, so it would shadow a real person. Menu items validate
// the same way for the same reason.
//
// The roll number has already been canonicalised and rejected-if-blank by
// normalizeRollNo, which every caller must run first.
func validateUser(name string) error {
	if strings.TrimSpace(name) == "" {
		return fmt.Errorf("%w: name is empty", ErrInvalidInput)
	}
	return nil
}

// Search for a user by their ID
func GetUserByID(ctx context.Context, db DB, id int64) (*User, error) {
	var user User
	err := db.QueryRowContext(ctx, `SELECT id, roll_no, name, phone, created_at FROM users WHERE id = ?`, id).Scan(
		&user.ID,
		&user.RollNo,
		&user.Name,
		&user.Phone,
		&user.CreatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("%w: user ID %d", ErrNotFound, id)
	}
	if err != nil {
		return nil, fmt.Errorf("get user by ID: %w", err)
	}

	return &user, nil
}

// Search for a user by their roll number (TVE__XX___)
//
// The argument is canonicalised the same way CreateUser canonicalises what it
// stores, so a lookup succeeds however the caller typed it. Without that, a
// student who registered as "TVE24CS001" would not be found by "tve24cs001",
// which is the same person asking a kiosk to find their own balance.
func GetUserByRollNo(ctx context.Context, db DB, rollNo string) (*User, error) {
	normalized, err := normalizeRollNo(rollNo)
	if err != nil {
		return nil, err
	}

	var user User
	err = db.QueryRowContext(ctx, `SELECT id, roll_no, name, phone, created_at FROM users WHERE roll_no = ?`, normalized).Scan(
		&user.ID,
		&user.RollNo,
		&user.Name,
		&user.Phone,
		&user.CreatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("%w: roll number %q", ErrNotFound, normalized)
	}
	if err != nil {
		return nil, fmt.Errorf("get user by roll number: %w", err)
	}

	return &user, nil
}

// Updates user roll_no, name, and phone of a given user struct.
// returns nil if successful
func UpdateUser(ctx context.Context, db DB, user User) error {
	// Normalising here matters as much as on create: it is the other way a
	// differently-cased identifier could enter the table, and UpdateUser is a
	// full overwrite rather than a partial patch.
	rollNo, err := normalizeRollNo(user.RollNo)
	if err != nil {
		return err
	}
	if err := validateUser(user.Name); err != nil {
		return err
	}

	result, err := db.ExecContext(ctx, `UPDATE users SET roll_no = ?, name = ?, phone = ? WHERE id = ?`, rollNo, user.Name, user.Phone, user.ID)
	if err != nil {
		// Renaming a user onto another user's roll_no violates UNIQUE.
		return fmt.Errorf("update user: %w", asConflict(err))
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("check updated user rows: %w", err)
	}
	if rowsAffected == 0 {
		return fmt.Errorf("%w: user ID %d", ErrNotFound, user.ID)
	}

	return nil
}

// DeleteUser permanently removes a user.
//
// This fails with ErrConflict if the user has any orders or ledger rows, because
// both orders.user_id and transactions.user_id are ON DELETE RESTRICT. That is
// deliberate: the whole point of the prepaid balance is that money movements
// stay attributable to a person, so a user who has ever transacted cannot be
// erased. There is no force-delete here on purpose — anonymising a user (blank
// the name and phone, keep the row) preserves the ledger without losing the
// history that the balance depends on.
func DeleteUser(ctx context.Context, db DB, id int64) error {
	result, err := db.ExecContext(ctx, `DELETE FROM users WHERE id = ?`, id)
	if err != nil {
		// The foreign key restriction is the expected rejection here, so it is
		// surfaced as ErrConflict rather than a raw driver error.
		return fmt.Errorf("delete user: %w", asConflict(err))
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("check deleted user rows: %w", err)
	}
	if rowsAffected == 0 {
		return fmt.Errorf("%w: user ID %d", ErrNotFound, id)
	}

	return nil
}

// returns a list of all users in the users table
func ListUsers(ctx context.Context, db DB) ([]User, error) {
	rows, err := db.QueryContext(ctx, `SELECT id, roll_no, name, phone, created_at FROM users ORDER BY id`)
	if err != nil {
		return nil, fmt.Errorf("list users: %w", err)
	}
	defer rows.Close()

	var users []User
	for rows.Next() {
		var user User
		if err := rows.Scan(
			&user.ID,
			&user.RollNo,
			&user.Name,
			&user.Phone,
			&user.CreatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan user: %w", err)
		}
		users = append(users, user)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate users: %w", err)
	}

	return users, nil
}
