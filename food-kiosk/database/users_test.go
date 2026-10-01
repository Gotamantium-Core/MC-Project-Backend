package database

import (
	"context"
	"errors"
	"testing"
)

func TestCreateAndGetUser(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)

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
	db := openTestDB(t)

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
	db := openTestDB(t)

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
	db := openTestDB(t)

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

func TestGetUserByRollNoNotFound(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)

	if _, err := GetUserByRollNo(ctx, db, "TVE24CS999"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("GetUserByRollNo() error = %v, want ErrNotFound", err)
	}

	// An untouched database lists no users, and nil is the contract the other
	// list helpers share.
	users, err := ListUsers(ctx, db)
	if err != nil {
		t.Fatalf("ListUsers() error = %v", err)
	}
	if users != nil {
		t.Fatalf("ListUsers() on an empty database = %+v, want nil", users)
	}
}

func TestDeleteUser(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)

	id, err := CreateUser(ctx, db, "TVE24CS120", "Nikhil Bose", "")
	if err != nil {
		t.Fatalf("CreateUser() error = %v", err)
	}

	if err := DeleteUser(ctx, db, id); err != nil {
		t.Fatalf("DeleteUser() error = %v", err)
	}
	if _, err := GetUserByID(ctx, db, id); !errors.Is(err, ErrNotFound) {
		t.Fatalf("GetUserByID() after delete error = %v, want ErrNotFound", err)
	}

	// Deleting nothing is a 404, not a silent success.
	if err := DeleteUser(ctx, db, id); !errors.Is(err, ErrNotFound) {
		t.Fatalf("DeleteUser() second call error = %v, want ErrNotFound", err)
	}
}

// TestDeleteUserWithHistoryIsRestricted pins the ON DELETE RESTRICT that keeps
// the prepaid ledger attributable to a real person. Either an order or a bare
// top-up is enough to block the delete.
func TestDeleteUserWithHistoryIsRestricted(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)

	seedMenu(t, db, "Vada", "VADA", 3000)

	ordered := seedUser(t, db, "TVE24CS121", 10000)
	if _, err := CreateOrder(ctx, db, ordered, []OrderLine{{Code: "VADA", Quantity: 1}}); err != nil {
		t.Fatalf("CreateOrder() error = %v", err)
	}
	if err := DeleteUser(ctx, db, ordered); err == nil {
		t.Fatal("DeleteUser() on a user with an order succeeded; ON DELETE RESTRICT is not in effect")
	}
	if _, err := GetUserByID(ctx, db, ordered); err != nil {
		t.Fatalf("the restricted user was removed anyway: %v", err)
	}

	// A top-up alone is still a money movement, so it restricts too.
	toppedUp := seedUser(t, db, "TVE24CS122", 5000)
	if err := DeleteUser(ctx, db, toppedUp); err == nil {
		t.Fatal("DeleteUser() on a user with a top-up succeeded; ON DELETE RESTRICT is not in effect")
	}

	// A user who never transacted has nothing holding them back.
	untouched, err := CreateUser(ctx, db, "TVE24CS123", "Priya Nair", "")
	if err != nil {
		t.Fatalf("CreateUser() error = %v", err)
	}
	if err := DeleteUser(ctx, db, untouched); err != nil {
		t.Fatalf("DeleteUser() on a user with no history error = %v", err)
	}
}
