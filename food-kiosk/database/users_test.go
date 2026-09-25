package database

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"
)

func openUserTestDB(t *testing.T) *sql.DB {
	t.Helper()

	db, err := Open(filepath.Join(t.TempDir(), "users.db"))
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

func TestCreateAndGetUser(t *testing.T) {
	ctx := context.Background()
	db := openUserTestDB(t)

	id, err := CreateUser(ctx, db, "TVE24CS001", "Asha Rao", "9876543210")
	if err != nil {
		t.Fatalf("CreateUser() error = %v", err)
	}

	user, err := GetUserByID(ctx, db, id)
	if err != nil {
		t.Fatalf("GetUserByID() error = %v", err)
	}
	if user.RollNo != "TVE24CS001" || user.Name != "Asha Rao" {
		t.Fatalf("GetUserByID() user = %+v", user)
	}
	if user.Phone == nil || *user.Phone != "9876543210" {
		t.Fatalf("GetUserByID() phone = %v", user.Phone)
	}

	byRollNo, err := GetUserByRollNo(ctx, db, "TVE24CS001")
	if err != nil {
		t.Fatalf("GetUserByRollNo() error = %v", err)
	}
	if byRollNo.ID != id {
		t.Fatalf("GetUserByRollNo() ID = %d, want %d", byRollNo.ID, id)
	}

	users, err := ListUsers(ctx, db)
	if err != nil {
		t.Fatalf("ListUsers() error = %v", err)
	}
	if len(users) != 1 {
		t.Fatalf("ListUsers() length = %d, want 1", len(users))
	}
}

func TestGetUserWithNullPhone(t *testing.T) {
	ctx := context.Background()
	db := openUserTestDB(t)

	result, err := db.ExecContext(ctx, `INSERT INTO users (roll_no, name, phone) VALUES (?, ?, NULL)`, "TVE24CS002", "Ravi Kumar")
	if err != nil {
		t.Fatalf("insert user: %v", err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		t.Fatalf("LastInsertId() error = %v", err)
	}

	user, err := GetUserByID(ctx, db, id)
	if err != nil {
		t.Fatalf("GetUserByID() error = %v", err)
	}
	if user.Phone != nil {
		t.Fatalf("GetUserByID() phone = %q, want nil", *user.Phone)
	}
}

func TestUpdateUser(t *testing.T) {
	ctx := context.Background()
	db := openUserTestDB(t)

	id, err := CreateUser(ctx, db, "TVE24CS003", "Meera Shah", "")
	if err != nil {
		t.Fatalf("CreateUser() error = %v", err)
	}

	phone := "9123456780"
	err = UpdateUser(ctx, db, User{
		ID:     id,
		RollNo: "TVE24CS004",
		Name:   "Meera Iyer",
		Phone:  &phone,
	})
	if err != nil {
		t.Fatalf("UpdateUser() error = %v", err)
	}

	user, err := GetUserByID(ctx, db, id)
	if err != nil {
		t.Fatalf("GetUserByID() error = %v", err)
	}
	if user.RollNo != "TVE24CS004" || user.Name != "Meera Iyer" {
		t.Fatalf("GetUserByID() user = %+v", user)
	}
	if user.Phone == nil || *user.Phone != phone {
		t.Fatalf("GetUserByID() phone = %v", user.Phone)
	}
}

func TestUserErrors(t *testing.T) {
	ctx := context.Background()
	db := openUserTestDB(t)

	_, err := CreateUser(ctx, db, "TVE24CS005", "Karan Singh", "")
	if err != nil {
		t.Fatalf("CreateUser() error = %v", err)
	}
	if _, err := CreateUser(ctx, db, "TVE24CS005", "Another User", ""); err == nil {
		t.Fatal("CreateUser() duplicate error = nil")
	}

	_, err = GetUserByID(ctx, db, 999)
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("GetUserByID() error = %v, want ErrNotFound", err)
	}

	err = UpdateUser(ctx, db, User{ID: 999, RollNo: "TVE24CS006", Name: "Missing User"})
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("UpdateUser() error = %v, want ErrNotFound", err)
	}
}
