package server

import (
	"net/http"

	"food-kiosk/database"
)

type menuItemResponse struct {
	ID          int64  `json:"id"`
	ItemName    string `json:"item_name"`
	Code        string `json:"code"`
	PricePaise  int64  `json:"price_paise"`
	IsAvailable bool   `json:"is_available"`
}

func menuToResponse(m database.Menu) menuItemResponse {
	return menuItemResponse{
		ID:          m.ID,
		ItemName:    m.ItemName,
		Code:        m.Code,
		PricePaise:  m.PricePaise,
		IsAvailable: m.IsAvailable == 1,
	}
}

type menuListResponse struct {
	Items []menuItemResponse `json:"items"`
}

// handleListMenu returns the customer-facing menu: available items only. An
// admin may pass ?all=1 to see retired items too.
func (s *server) handleListMenu(w http.ResponseWriter, r *http.Request) {
	all := r.URL.Query().Get("all")
	wantAll := all == "1" || all == "true"

	var (
		items []database.Menu
		err   error
	)
	if wantAll {
		if !s.isAdmin(r) {
			writeError(w, http.StatusForbidden, "admin role required")
			return
		}
		items, err = database.ListMenuItems(r.Context(), s.db)
	} else {
		items, err = database.ListAvailableMenuItems(r.Context(), s.db)
	}
	if err != nil {
		writeDBError(w, err)
		return
	}

	resp := menuListResponse{Items: make([]menuItemResponse, len(items))}
	for i, item := range items {
		resp.Items[i] = menuToResponse(item)
	}
	writeJSON(w, http.StatusOK, resp)
}

type createMenuRequest struct {
	ItemName    string `json:"item_name"`
	Code        string `json:"code"`
	PricePaise  int64  `json:"price_paise"`
	IsAvailable *bool  `json:"is_available"`
}

func (s *server) handleCreateMenu(w http.ResponseWriter, r *http.Request) {
	var req createMenuRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	available := int64(1)
	if req.IsAvailable != nil && !*req.IsAvailable {
		available = 0
	}

	id, err := database.CreateMenuItem(r.Context(), s.db, req.ItemName, req.Code, req.PricePaise, available)
	if err != nil {
		writeDBError(w, err)
		return
	}

	writeJSON(w, http.StatusCreated, map[string]int64{"id": id})
}

type updateMenuRequest struct {
	ItemName    string `json:"item_name"`
	Code        string `json:"code"`
	PricePaise  *int64 `json:"price_paise"`
	IsAvailable *bool  `json:"is_available"`
}

// handleUpdateMenu is a full overwrite, but any field left out keeps its current
// value, so a caller can rename without having to resend the price. The item is
// read first because UpdateMenuItem replaces every column.
func (s *server) handleUpdateMenu(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid menu item id")
		return
	}

	var req updateMenuRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	current, err := database.GetMenuItemByID(r.Context(), s.db, id)
	if err != nil {
		writeDBError(w, err)
		return
	}

	if req.ItemName != "" {
		current.ItemName = req.ItemName
	}
	if req.Code != "" {
		current.Code = req.Code
	}
	if req.PricePaise != nil {
		current.PricePaise = *req.PricePaise
	}
	if req.IsAvailable != nil {
		if *req.IsAvailable {
			current.IsAvailable = 1
		} else {
			current.IsAvailable = 0
		}
	}

	if err := database.UpdateMenuItem(r.Context(), s.db, *current); err != nil {
		writeDBError(w, err)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

type menuAvailabilityRequest struct {
	IsAvailable bool `json:"is_available"`
}

func (s *server) handleSetMenuAvailability(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid menu item id")
		return
	}

	var req menuAvailabilityRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	available := int64(0)
	if req.IsAvailable {
		available = 1
	}

	if err := database.SetMenuItemAvailability(r.Context(), s.db, id, available); err != nil {
		writeDBError(w, err)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

func (s *server) handleDeleteMenu(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid menu item id")
		return
	}

	if err := database.DeleteMenuItem(r.Context(), s.db, id); err != nil {
		writeDBError(w, err)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// isAdmin reports whether the caller holds the admin role. With auth disabled
// everything is treated as admin, so the curl/dev mode can reach admin routes.
func (s *server) isAdmin(r *http.Request) bool {
	if !s.cfg.AuthRequired {
		return true
	}
	ident, ok := identityFrom(r)
	return ok && ident.IsAdmin()
}
