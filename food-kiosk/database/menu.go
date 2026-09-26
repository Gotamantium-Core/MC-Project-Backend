package database

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
)

type Menu struct {
	ID          int64
	ItemName    string
	Code        string
	PricePaise  int64
	IsAvailable int64 // 0 = no, 1 = yes
	CreatedAt   string
}

// menuColumns is the canonical column order used by every menu query, so that
// scanMenuItem always has matching fields.
const menuColumns = `id, item_name, code, price_paise, is_available, created_at`

func scanMenuItem(s rowScanner) (*Menu, error) {
	var item Menu
	if err := s.Scan(
		&item.ID,
		&item.ItemName,
		&item.Code,
		&item.PricePaise,
		&item.IsAvailable,
		&item.CreatedAt,
	); err != nil {
		return nil, err
	}
	return &item, nil
}

// validateMenuItem rejects values that would be stored inconsistently. The
// schema CHECKs catch negative prices, but nothing constrains is_available, so
// a stray value like 5 would later be read back as "unavailable".
func validateMenuItem(itemName string, code string, pricePaise int64, isAvailable int64) error {
	if strings.TrimSpace(itemName) == "" {
		return fmt.Errorf("%w: item name is empty", ErrInvalidInput)
	}
	if strings.TrimSpace(code) == "" {
		return fmt.Errorf("%w: code is empty", ErrInvalidInput)
	}
	if pricePaise < 0 {
		return fmt.Errorf("%w: price %d paise is negative", ErrInvalidInput, pricePaise)
	}
	if isAvailable != 0 && isAvailable != 1 {
		return fmt.Errorf("%w: is_available must be 0 or 1, got %d", ErrInvalidInput, isAvailable)
	}
	return nil
}

// Create a new menu item and insert into the menu table.
// pricePaise is the price in paise (1 rupee = 100 paise).
func CreateMenuItem(ctx context.Context, db DB, itemName string, code string, pricePaise int64, isAvailable int64) (int64, error) {
	if err := validateMenuItem(itemName, code, pricePaise, isAvailable); err != nil {
		return 0, err
	}

	result, err := db.ExecContext(ctx, `INSERT INTO menu (item_name, code, price_paise, is_available) VALUES (?, ?, ?, ?)`, itemName, code, pricePaise, isAvailable)
	if err != nil {
		return 0, fmt.Errorf("insert menu item: %w", err)
	}

	id, err := result.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("get inserted menu item ID: %w", err)
	}

	return id, nil
}

// GetMenuItemByID searches for a menu item by its primary key.
func GetMenuItemByID(ctx context.Context, db DB, id int64) (*Menu, error) {
	item, err := scanMenuItem(db.QueryRowContext(ctx, `SELECT `+menuColumns+` FROM menu WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("%w: menu item ID %d", ErrNotFound, id)
	}
	if err != nil {
		return nil, fmt.Errorf("get menu item by ID: %w", err)
	}

	return item, nil
}

// GetMenuItemByCode searches for a menu item by its unique ordering code,
// which is how a customer refers to an item at the kiosk.
func GetMenuItemByCode(ctx context.Context, db DB, code string) (*Menu, error) {
	item, err := scanMenuItem(db.QueryRowContext(ctx, `SELECT `+menuColumns+` FROM menu WHERE code = ?`, code))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("%w: menu item code %q", ErrNotFound, code)
	}
	if err != nil {
		return nil, fmt.Errorf("get menu item by code: %w", err)
	}

	return item, nil
}

// GetMenuItemsByCodes resolves a whole cart in one query and returns the items
// keyed by code, so callers can price a multi-item order without an N+1 loop.
//
// Duplicate codes collapse to a single entry, and an unknown code is reported
// as ErrNotFound rather than silently omitted, so a partially valid cart cannot
// be turned into a partial order. An empty codes slice returns an empty map.
func GetMenuItemsByCodes(ctx context.Context, db DB, codes []string) (map[string]Menu, error) {
	if len(codes) == 0 {
		return map[string]Menu{}, nil
	}

	placeholders := make([]string, len(codes))
	args := make([]any, len(codes))
	for i, code := range codes {
		placeholders[i] = "?"
		args[i] = code
	}

	rows, err := db.QueryContext(ctx, `SELECT `+menuColumns+` FROM menu WHERE code IN (`+strings.Join(placeholders, ", ")+`)`, args...)
	if err != nil {
		return nil, fmt.Errorf("get menu items by codes: %w", err)
	}
	defer rows.Close()

	items := make(map[string]Menu, len(codes))
	for rows.Next() {
		item, err := scanMenuItem(rows)
		if err != nil {
			return nil, fmt.Errorf("scan menu item: %w", err)
		}
		items[item.Code] = *item
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate menu items: %w", err)
	}

	var missing []string
	for _, code := range codes {
		if _, ok := items[code]; !ok {
			missing = append(missing, code)
		}
	}
	if len(missing) > 0 {
		return nil, fmt.Errorf("%w: menu item codes %s", ErrNotFound, strings.Join(missing, ", "))
	}

	return items, nil
}

// UpdateMenuItem overwrites item_name, code, price_paise and is_available of the
// menu item identified by item.ID. Returns ErrNotFound if the ID does not exist.
func UpdateMenuItem(ctx context.Context, db DB, item Menu) error {
	if err := validateMenuItem(item.ItemName, item.Code, item.PricePaise, item.IsAvailable); err != nil {
		return err
	}

	result, err := db.ExecContext(ctx, `UPDATE menu SET item_name = ?, code = ?, price_paise = ?, is_available = ? WHERE id = ?`, item.ItemName, item.Code, item.PricePaise, item.IsAvailable, item.ID)
	if err != nil {
		return fmt.Errorf("update menu item: %w", err)
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("check updated menu item rows: %w", err)
	}
	if rowsAffected == 0 {
		return fmt.Errorf("%w: menu item ID %d", ErrNotFound, item.ID)
	}

	return nil
}

// SetMenuItemAvailability flips only the is_available flag, leaving the name,
// code and price untouched. Prefer this over UpdateMenuItem when the kiosk just
// needs to hide or restore an item.
func SetMenuItemAvailability(ctx context.Context, db DB, id int64, isAvailable int64) error {
	if isAvailable != 0 && isAvailable != 1 {
		return fmt.Errorf("%w: is_available must be 0 or 1, got %d", ErrInvalidInput, isAvailable)
	}

	result, err := db.ExecContext(ctx, `UPDATE menu SET is_available = ? WHERE id = ?`, isAvailable, id)
	if err != nil {
		return fmt.Errorf("set menu item availability: %w", err)
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("check updated menu item rows: %w", err)
	}
	if rowsAffected == 0 {
		return fmt.Errorf("%w: menu item ID %d", ErrNotFound, id)
	}

	return nil
}

// DeleteMenuItem permanently removes a menu item.
//
// This fails with a foreign key error if the item appears in any order, because
// order_items.menu_id has no ON DELETE action. That is deliberate: past orders
// must keep pointing at the item they were priced from, so retire an item with
// SetMenuItemAvailability instead of deleting it.
func DeleteMenuItem(ctx context.Context, db DB, id int64) error {
	result, err := db.ExecContext(ctx, `DELETE FROM menu WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("delete menu item: %w", err)
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("check deleted menu item rows: %w", err)
	}
	if rowsAffected == 0 {
		return fmt.Errorf("%w: menu item ID %d", ErrNotFound, id)
	}

	return nil
}

// ListMenuItems returns every menu item, available or not, ordered by id.
// Use ListAvailableMenuItems for the customer-facing menu.
func ListMenuItems(ctx context.Context, db DB) ([]Menu, error) {
	return listMenuItems(ctx, db, `SELECT `+menuColumns+` FROM menu ORDER BY id`)
}

// ListAvailableMenuItems returns only the items a customer can currently order.
func ListAvailableMenuItems(ctx context.Context, db DB) ([]Menu, error) {
	return listMenuItems(ctx, db, `SELECT `+menuColumns+` FROM menu WHERE is_available = 1 ORDER BY id`)
}

func listMenuItems(ctx context.Context, db DB, query string) ([]Menu, error) {
	rows, err := db.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("list menu items: %w", err)
	}
	defer rows.Close()

	var items []Menu
	for rows.Next() {
		item, err := scanMenuItem(rows)
		if err != nil {
			return nil, fmt.Errorf("scan menu item: %w", err)
		}
		items = append(items, *item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate menu items: %w", err)
	}

	return items, nil
}
