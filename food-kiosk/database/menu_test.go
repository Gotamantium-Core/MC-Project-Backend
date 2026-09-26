package database

import (
	"context"
	"errors"
	"testing"
)

func TestCreateAndGetMenuItem(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)

	id, err := CreateMenuItem(ctx, db, "Masala Chai", "CHAI", 2000, 1)
	if err != nil {
		t.Fatalf("CreateMenuItem() error = %v", err)
	}

	item, err := GetMenuItemByID(ctx, db, id)
	if err != nil {
		t.Fatalf("GetMenuItemByID() error = %v", err)
	}
	if item.ItemName != "Masala Chai" || item.Code != "CHAI" {
		t.Fatalf("GetMenuItemByID() item = %+v", item)
	}
	if item.PricePaise != 2000 {
		t.Fatalf("GetMenuItemByID() price = %d, want 2000", item.PricePaise)
	}
	if item.IsAvailable != 1 {
		t.Fatalf("GetMenuItemByID() isAvailable = %d, want 1", item.IsAvailable)
	}
	if item.CreatedAt == "" {
		t.Fatal("GetMenuItemByID() createdAt is empty")
	}

	byCode, err := GetMenuItemByCode(ctx, db, "CHAI")
	if err != nil {
		t.Fatalf("GetMenuItemByCode() error = %v", err)
	}
	if byCode.ID != id {
		t.Fatalf("GetMenuItemByCode() ID = %d, want %d", byCode.ID, id)
	}
}

func TestCreateMenuItemDefaultsAvailability(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)

	// An explicit 0 must be honoured rather than replaced by the column default.
	id, err := CreateMenuItem(ctx, db, "Samosa", "SAM", 1500, 0)
	if err != nil {
		t.Fatalf("CreateMenuItem() error = %v", err)
	}

	item, err := GetMenuItemByID(ctx, db, id)
	if err != nil {
		t.Fatalf("GetMenuItemByID() error = %v", err)
	}
	if item.IsAvailable != 0 {
		t.Fatalf("GetMenuItemByID() isAvailable = %d, want 0", item.IsAvailable)
	}
}

func TestCreateMenuItemValidation(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)

	tests := []struct {
		name        string
		itemName    string
		code        string
		pricePaise  int64
		isAvailable int64
	}{
		{name: "empty name", itemName: "  ", code: "CHAI", pricePaise: 1000, isAvailable: 1},
		{name: "empty code", itemName: "Chai", code: "   ", pricePaise: 1000, isAvailable: 1},
		{name: "negative price", itemName: "Chai", code: "CHAI", pricePaise: -1, isAvailable: 1},
		{name: "availability out of range", itemName: "Chai", code: "CHAI", pricePaise: 1000, isAvailable: 5},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := CreateMenuItem(ctx, db, tt.itemName, tt.code, tt.pricePaise, tt.isAvailable)
			if !errors.Is(err, ErrInvalidInput) {
				t.Fatalf("CreateMenuItem() error = %v, want ErrInvalidInput", err)
			}
		})
	}

	// Nothing invalid should have reached the database.
	items, err := ListMenuItems(ctx, db)
	if err != nil {
		t.Fatalf("ListMenuItems() error = %v", err)
	}
	if len(items) != 0 {
		t.Fatalf("ListMenuItems() length = %d, want 0", len(items))
	}
}

func TestGetMenuItemsByCodes(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)

	chaiID, err := CreateMenuItem(ctx, db, "Masala Chai", "CHAI", 2000, 1)
	if err != nil {
		t.Fatalf("CreateMenuItem() error = %v", err)
	}
	if _, err := CreateMenuItem(ctx, db, "Vada", "VADA", 3000, 1); err != nil {
		t.Fatalf("CreateMenuItem() error = %v", err)
	}
	if _, err := CreateMenuItem(ctx, db, "Hidden Item", "HID", 1000, 0); err != nil {
		t.Fatalf("CreateMenuItem() error = %v", err)
	}

	items, err := GetMenuItemsByCodes(ctx, db, []string{"CHAI", "VADA", "CHAI"})
	if err != nil {
		t.Fatalf("GetMenuItemsByCodes() error = %v", err)
	}
	// Duplicate codes must collapse into a single entry.
	if len(items) != 2 {
		t.Fatalf("GetMenuItemsByCodes() length = %d, want 2", len(items))
	}
	if items["CHAI"].ID != chaiID || items["CHAI"].PricePaise != 2000 {
		t.Fatalf("GetMenuItemsByCodes() CHAI = %+v", items["CHAI"])
	}
	if items["VADA"].PricePaise != 3000 {
		t.Fatalf("GetMenuItemsByCodes() VADA = %+v", items["VADA"])
	}

	empty, err := GetMenuItemsByCodes(ctx, db, nil)
	if err != nil {
		t.Fatalf("GetMenuItemsByCodes(nil) error = %v", err)
	}
	if len(empty) != 0 {
		t.Fatalf("GetMenuItemsByCodes(nil) length = %d, want 0", len(empty))
	}
}

func TestGetMenuItemsByCodesReportsMissing(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)

	if _, err := CreateMenuItem(ctx, db, "Masala Chai", "CHAI", 2000, 1); err != nil {
		t.Fatalf("CreateMenuItem() error = %v", err)
	}

	// A partially valid cart must fail loudly rather than drop the bad code.
	items, err := GetMenuItemsByCodes(ctx, db, []string{"CHAI", "GHOST"})
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("GetMenuItemsByCodes() error = %v, want ErrNotFound", err)
	}
	if items != nil {
		t.Fatalf("GetMenuItemsByCodes() items = %v, want nil", items)
	}
}

func TestListMenuItemsFiltersAvailability(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)

	if _, err := CreateMenuItem(ctx, db, "Masala Chai", "CHAI", 2000, 1); err != nil {
		t.Fatalf("CreateMenuItem() error = %v", err)
	}
	if _, err := CreateMenuItem(ctx, db, "Vada", "VADA", 3000, 1); err != nil {
		t.Fatalf("CreateMenuItem() error = %v", err)
	}
	samosaID, err := CreateMenuItem(ctx, db, "Samosa", "SAM", 1500, 0)
	if err != nil {
		t.Fatalf("CreateMenuItem() error = %v", err)
	}

	all, err := ListMenuItems(ctx, db)
	if err != nil {
		t.Fatalf("ListMenuItems() error = %v", err)
	}
	if len(all) != 3 {
		t.Fatalf("ListMenuItems() length = %d, want 3", len(all))
	}

	available, err := ListAvailableMenuItems(ctx, db)
	if err != nil {
		t.Fatalf("ListAvailableMenuItems() error = %v", err)
	}
	if len(available) != 2 {
		t.Fatalf("ListAvailableMenuItems() length = %d, want 2", len(available))
	}
	for _, item := range available {
		if item.IsAvailable != 1 {
			t.Fatalf("ListAvailableMenuItems() item = %+v, want available", item)
		}
	}

	// An empty table should return an empty slice, not nil rows.
	if items, err := ListMenuItems(ctx, openTestDB(t)); err != nil || items != nil {
		t.Fatalf("ListMenuItems() on empty db = %v, %v", items, err)
	}

	if err := SetMenuItemAvailability(ctx, db, samosaID, 1); err != nil {
		t.Fatalf("SetMenuItemAvailability() error = %v", err)
	}
	available, err = ListAvailableMenuItems(ctx, db)
	if err != nil {
		t.Fatalf("ListAvailableMenuItems() error = %v", err)
	}
	if len(available) != 3 {
		t.Fatalf("ListAvailableMenuItems() length = %d, want 3", len(available))
	}
}

func TestUpdateMenuItem(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)

	id, err := CreateMenuItem(ctx, db, "Masala Chai", "CHAI", 2000, 1)
	if err != nil {
		t.Fatalf("CreateMenuItem() error = %v", err)
	}

	err = UpdateMenuItem(ctx, db, Menu{
		ID:          id,
		ItemName:    "Masala Chai (Large)",
		Code:        "CHAI_L",
		PricePaise:  3500,
		IsAvailable: 0,
	})
	if err != nil {
		t.Fatalf("UpdateMenuItem() error = %v", err)
	}

	item, err := GetMenuItemByID(ctx, db, id)
	if err != nil {
		t.Fatalf("GetMenuItemByID() error = %v", err)
	}
	if item.ItemName != "Masala Chai (Large)" || item.Code != "CHAI_L" {
		t.Fatalf("GetMenuItemByID() item = %+v", item)
	}
	if item.PricePaise != 3500 {
		t.Fatalf("GetMenuItemByID() price = %d, want 3500", item.PricePaise)
	}
	if item.IsAvailable != 0 {
		t.Fatalf("GetMenuItemByID() isAvailable = %d, want 0", item.IsAvailable)
	}
}

func TestSetMenuItemAvailabilityKeepsOtherFields(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)

	id, err := CreateMenuItem(ctx, db, "Vada", "VADA", 3000, 1)
	if err != nil {
		t.Fatalf("CreateMenuItem() error = %v", err)
	}

	if err := SetMenuItemAvailability(ctx, db, id, 0); err != nil {
		t.Fatalf("SetMenuItemAvailability() error = %v", err)
	}

	item, err := GetMenuItemByID(ctx, db, id)
	if err != nil {
		t.Fatalf("GetMenuItemByID() error = %v", err)
	}
	if item.IsAvailable != 0 {
		t.Fatalf("GetMenuItemByID() isAvailable = %d, want 0", item.IsAvailable)
	}
	if item.ItemName != "Vada" || item.Code != "VADA" || item.PricePaise != 3000 {
		t.Fatalf("SetMenuItemAvailability() changed other fields: %+v", item)
	}

	if err := SetMenuItemAvailability(ctx, db, id, 2); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("SetMenuItemAvailability() error = %v, want ErrInvalidInput", err)
	}
}

func TestDeleteMenuItem(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)

	id, err := CreateMenuItem(ctx, db, "Vada", "VADA", 3000, 1)
	if err != nil {
		t.Fatalf("CreateMenuItem() error = %v", err)
	}

	if err := DeleteMenuItem(ctx, db, id); err != nil {
		t.Fatalf("DeleteMenuItem() error = %v", err)
	}
	if _, err := GetMenuItemByID(ctx, db, id); !errors.Is(err, ErrNotFound) {
		t.Fatalf("GetMenuItemByID() after delete error = %v, want ErrNotFound", err)
	}
	if err := DeleteMenuItem(ctx, db, id); !errors.Is(err, ErrNotFound) {
		t.Fatalf("DeleteMenuItem() second call error = %v, want ErrNotFound", err)
	}
}

func TestMenuItemErrors(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)

	if _, err := CreateMenuItem(ctx, db, "Vada", "VADA", 3000, 1); err != nil {
		t.Fatalf("CreateMenuItem() error = %v", err)
	}
	if _, err := CreateMenuItem(ctx, db, "Other Vada", "VADA", 2500, 1); err == nil {
		t.Fatal("CreateMenuItem() duplicate code error = nil")
	}

	if _, err := GetMenuItemByID(ctx, db, 999); !errors.Is(err, ErrNotFound) {
		t.Fatalf("GetMenuItemByID() error = %v, want ErrNotFound", err)
	}
	if _, err := GetMenuItemByCode(ctx, db, "GHOST"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("GetMenuItemByCode() error = %v, want ErrNotFound", err)
	}

	err := UpdateMenuItem(ctx, db, Menu{ID: 999, ItemName: "Ghost", Code: "GHOST", PricePaise: 100, IsAvailable: 1})
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("UpdateMenuItem() error = %v, want ErrNotFound", err)
	}
	err = SetMenuItemAvailability(ctx, db, 999, 1)
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("SetMenuItemAvailability() error = %v, want ErrNotFound", err)
	}
}
