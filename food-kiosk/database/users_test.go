package database

import (
	"context"
	"errors"
	"testing"
)

// TestRollNoIsNormalized covers the canonicalisation that makes the UNIQUE index
// mean one person per account.
//
// SQLite compares TEXT with BINARY collation, so "TVE24CS001", "tve24cs001" and
// "TVE24CS001 " are three distinct values and the UNIQUE index accepts all three.
// Without normalisation, a student who typed their roll number in the wrong case
// at the kiosk would silently get a second account with a zero balance, and their
// existing credit would appear to have vanished.
func TestRollNoIsNormalized(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)

	// Deliberately messy input: surrounding space, lower case, mixed case.
	id, err := CreateUser(ctx, db, "  tve24Cs001  ", "Asha Rao", "")
	if err != nil {
		t.Fatalf("CreateUser() error = %v", err)
	}

	// What lands in the table is the canonical form, not what was typed.
	user, err := GetUserByID(ctx, db, id)
	if err != nil {
		t.Fatalf("GetUserByID() error = %v", err)
	}
	if user.RollNo != "TVE24CS001" {
		t.Fatalf("stored roll_no = %q, want TVE24CS001", user.RollNo)
	}

	// Every way of typing the same identifier finds the same person.
	for _, query := range []string{"TVE24CS001", "tve24cs001", "Tve24cs001", "  tve24cs001  "} {
		found, err := GetUserByRollNo(ctx, db, query)
		if err != nil {
			t.Fatalf("GetUserByRollNo(%q) error = %v, want the same user", query, err)
		}
		if found.ID != id {
			t.Fatalf("GetUserByRollNo(%q) ID = %d, want %d", query, found.ID, id)
		}
	}

	// And a re-registration in any casing is the same person, not a new account.
	for _, dup := range []string{"TVE24CS001", "tve24cs001", "TVE24CS001 "} {
		if _, err := CreateUser(ctx, db, dup, "Asha Again", ""); !errors.Is(err, ErrConflict) {
			t.Fatalf("CreateUser(%q) error = %v, want ErrConflict", dup, err)
		}
	}

	users, err := ListUsers(ctx, db)
	if err != nil {
		t.Fatalf("ListUsers() error = %v", err)
	}
	if len(users) != 1 {
		t.Fatalf("ListUsers() length = %d, want 1 (one person, one account)", len(users))
	}
}

// TestUpdateUserNormalizesRollNo covers the other way a differently-cased
// identifier could enter the table. UpdateUser is a full overwrite, so it needs
// the same canonicalisation as the insert path.
func TestUpdateUserNormalizesRollNo(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)

	id, err := CreateUser(ctx, db, "TVE24CS010", "Ravi Kumar", "")
	if err != nil {
		t.Fatalf("CreateUser() error = %v", err)
	}
	other, err := CreateUser(ctx, db, "TVE24CS011", "Meera Shah", "")
	if err != nil {
		t.Fatalf("CreateUser() error = %v", err)
	}

	// Renaming onto another user's identifier is a conflict whichever way it is
	// typed, and the rejected rename must not apply.
	err = UpdateUser(ctx, db, User{ID: other, RollNo: " tve24cs010 ", Name: "Meera Shah"})
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("UpdateUser() onto a taken roll number error = %v, want ErrConflict", err)
	}
	user, err := GetUserByID(ctx, db, other)
	if err != nil {
		t.Fatalf("GetUserByID() error = %v", err)
	}
	if user.RollNo != "TVE24CS011" {
		t.Fatalf("roll_no = %q, want TVE24CS011 (the rejected rename must not apply)", user.RollNo)
	}

	// A rename to a differently-cased identifier of its own row is accepted and
	// stored canonically.
	err = UpdateUser(ctx, db, User{ID: id, RollNo: " tve24cs010 ", Name: "Ravi Kumar"})
	if err != nil {
		t.Fatalf("UpdateUser() error = %v", err)
	}
	user, err = GetUserByID(ctx, db, id)
	if err != nil {
		t.Fatalf("GetUserByID() error = %v", err)
	}
	if user.RollNo != "TVE24CS010" {
		t.Fatalf("stored roll_no = %q, want TVE24CS010", user.RollNo)
	}

	// And it is still findable by the casing that was typed.
	found, err := GetUserByRollNo(ctx, db, "tve24cs010")
	if err != nil {
		t.Fatalf("GetUserByRollNo() error = %v", err)
	}
	if found.ID != id {
		t.Fatalf("GetUserByRollNo() ID = %d, want %d", found.ID, id)
	}
}

// TestRollNoLookupRejectsBlank keeps the lookup consistent with the write path: a
// blank identifier is invalid input rather than a 404, so a caller cannot mistake
// "you typed nothing" for "no such student".
func TestRollNoLookupRejectsBlank(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)

	for _, blank := range []string{"", "   ", "\t\n"} {
		if _, err := GetUserByRollNo(ctx, db, blank); !errors.Is(err, ErrInvalidInput) {
			t.Fatalf("GetUserByRollNo(%q) error = %v, want ErrInvalidInput", blank, err)
		}
	}
}

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
	// A duplicate roll number is the one failure Go cannot pre-empt, so the
	// UNIQUE index has to arrive as a sentinel the caller can match.
	if _, err := CreateUser(ctx, db, "TVE24CS005", "Another User", ""); !errors.Is(err, ErrConflict) {
		t.Fatalf("CreateUser() duplicate error = %v, want ErrConflict", err)
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

// TestCreateUserRejectsBlankIdentity guards the validation that keeps a row of
// whitespace out of a table the kiosk looks people up in. NOT NULL does not
// catch "  ", so without this a user could exist that can never be found by a
// real roll number.
func TestCreateUserRejectsBlankIdentity(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)

	tests := []struct {
		name   string
		rollNo string
		userNm string
	}{
		{name: "empty roll number", rollNo: "", userNm: "Asha Rao"},
		{name: "blank roll number", rollNo: "   ", userNm: "Asha Rao"},
		{name: "empty name", rollNo: "TVE24CS140", userNm: ""},
		{name: "blank name", rollNo: "TVE24CS141", userNm: "\t\n "},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := CreateUser(ctx, db, tt.rollNo, tt.userNm, ""); !errors.Is(err, ErrInvalidInput) {
				t.Fatalf("CreateUser() error = %v, want ErrInvalidInput", err)
			}
		})
	}

	// UpdateUser overwrites both fields, so it must not be a way around it.
	err := UpdateUser(ctx, db, User{ID: 1, RollNo: "TVE24CS142", Name: "  "})
	if !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("UpdateUser() with a blank name error = %v, want ErrInvalidInput", err)
	}

	users, err := ListUsers(ctx, db)
	if err != nil {
		t.Fatalf("ListUsers() error = %v", err)
	}
	if users != nil {
		t.Fatalf("ListUsers() = %+v, want nil (no blank user may be stored)", users)
	}
}

// TestUpdateUserOntoTakenRollNoIsConflict covers the other UNIQUE path: renaming
// one user onto another's roll number is a conflict, and the rename must not
// apply.
func TestUpdateUserOntoTakenRollNoIsConflict(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)

	if _, err := CreateUser(ctx, db, "TVE24CS143", "Asha Rao", ""); err != nil {
		t.Fatalf("CreateUser() error = %v", err)
	}
	second, err := CreateUser(ctx, db, "TVE24CS144", "Ravi Kumar", "")
	if err != nil {
		t.Fatalf("CreateUser() error = %v", err)
	}

	err = UpdateUser(ctx, db, User{ID: second, RollNo: "TVE24CS143", Name: "Ravi Kumar"})
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("UpdateUser() onto a taken roll number error = %v, want ErrConflict", err)
	}

	// And the loser keeps its own roll number.
	user, err := GetUserByID(ctx, db, second)
	if err != nil {
		t.Fatalf("GetUserByID() error = %v", err)
	}
	if user.RollNo != "TVE24CS144" {
		t.Fatalf("roll_no = %q, want TVE24CS144 (the rejected rename must not apply)", user.RollNo)
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
// top-up is enough to block the delete, and the refusal must arrive as
// ErrConflict so a caller can turn it into a 409.
func TestDeleteUserWithHistoryIsRestricted(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)

	seedMenu(t, db, "Vada", "VADA", 3000)

	ordered := seedUser(t, db, "TVE24CS121", 10000)
	if _, err := CreateOrder(ctx, db, ordered, []OrderLine{{Code: "VADA", Quantity: 1}}); err != nil {
		t.Fatalf("CreateOrder() error = %v", err)
	}
	if err := DeleteUser(ctx, db, ordered); !errors.Is(err, ErrConflict) {
		t.Fatalf("DeleteUser() on a user with an order error = %v, want ErrConflict", err)
	}
	if _, err := GetUserByID(ctx, db, ordered); err != nil {
		t.Fatalf("the restricted user was removed anyway: %v", err)
	}

	// A top-up alone is still a money movement, so it restricts too.
	toppedUp := seedUser(t, db, "TVE24CS122", 5000)
	if err := DeleteUser(ctx, db, toppedUp); !errors.Is(err, ErrConflict) {
		t.Fatalf("DeleteUser() on a user with a top-up error = %v, want ErrConflict", err)
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
