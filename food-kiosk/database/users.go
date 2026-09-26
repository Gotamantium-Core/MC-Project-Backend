package database

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
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
	result, err := db.ExecContext(ctx, `INSERT INTO users (roll_no, name, phone) VALUES (?, ?, ?)`, rollNo, name, phone)
	if err != nil {
		return 0, fmt.Errorf("insert user: %w", err)
	}

	id, err := result.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("get inserted user ID: %w", err)
	}

	return id, nil
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
func GetUserByRollNo(ctx context.Context, db DB, rollNo string) (*User, error) {
	var user User
	err := db.QueryRowContext(ctx, `SELECT id, roll_no, name, phone, created_at FROM users WHERE roll_no = ?`, rollNo).Scan(
		&user.ID,
		&user.RollNo,
		&user.Name,
		&user.Phone,
		&user.CreatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("%w: roll number %q", ErrNotFound, rollNo)
	}
	if err != nil {
		return nil, fmt.Errorf("get user by roll number: %w", err)
	}

	return &user, nil
}

// Updates user roll_no, name, and phone of a given user struct.
// returns nil if successful
func UpdateUser(ctx context.Context, db DB, user User) error {
	result, err := db.ExecContext(ctx, `UPDATE users SET roll_no = ?, name = ?, phone = ? WHERE id = ?`, user.RollNo, user.Name, user.Phone, user.ID)
	if err != nil {
		return fmt.Errorf("update user: %w", err)
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
