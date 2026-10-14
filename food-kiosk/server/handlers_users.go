package server

import (
	"net/http"

	"food-kiosk/database"
)

type createUserRequest struct {
	RollNo string `json:"roll_no"`
	Name   string `json:"name"`
	Phone  string `json:"phone"`
	// PIN is optional. When present it creates a credentials row so the
	// customer can sign in. Registration without it leaves the user without a
	// login, which is fine for a walk-up account an operator creates.
	PIN string `json:"pin"`
}

type createUserResponse struct {
	ID int64 `json:"id"`
}

// handleCreateUser creates a customer, optionally with a login, in one
// transaction so a half-registered user (row without credentials, or the
// reverse) can never be left behind.
func (s *server) handleCreateUser(w http.ResponseWriter, r *http.Request) {
	var req createUserRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if req.PIN != "" && len(req.PIN) < minPINLength {
		writeError(w, http.StatusBadRequest, "pin is too short")
		return
	}

	tx, err := s.db.BeginTx(r.Context(), nil)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	defer tx.Rollback() //nolint:errcheck // no-op once committed

	id, err := database.CreateUser(r.Context(), tx, req.RollNo, req.Name, req.Phone)
	if err != nil {
		writeDBError(w, err)
		return
	}

	if req.PIN != "" {
		hash, err := HashPassword(req.PIN)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "internal server error")
			return
		}
		if _, err := CreateCredential(r.Context(), tx, req.RollNo, hash, RoleCustomer, &id); err != nil {
			writeDBError(w, err)
			return
		}
	}

	if err := tx.Commit(); err != nil {
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}

	writeJSON(w, http.StatusCreated, createUserResponse{ID: id})
}

const minPINLength = 4

type topUpRequest struct {
	AmountPaise int64 `json:"amount_paise"`
}

type balanceResponse struct {
	BalancePaise int64 `json:"balance_paise"`
}

func (s *server) handleTopUp(w http.ResponseWriter, r *http.Request) {
	userID, err := parseID(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid user id")
		return
	}

	var req topUpRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if !s.mayActOnUser(r, userID) {
		writeError(w, http.StatusForbidden, "forbidden")
		return
	}

	if _, err := database.TopUpUser(r.Context(), s.db, userID, req.AmountPaise); err != nil {
		writeDBError(w, err)
		return
	}

	balance, err := database.GetUserBalancePaise(r.Context(), s.db, userID)
	if err != nil {
		writeDBError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, balanceResponse{BalancePaise: balance})
}

// mayActOnUser reports whether the caller is allowed to touch userID: an admin
// may, a customer may only touch themselves, and when auth is disabled the
// request is allowed through for curl testing.
func (s *server) mayActOnUser(r *http.Request, userID int64) bool {
	ident, ok := identityFrom(r)
	if !ok {
		return !s.cfg.AuthRequired
	}
	return ident.IsAdmin() || ident.UserID == userID
}

// handleWallet returns a user's current balance. A customer reads their own; an
// admin may pass ?user_id= to read anyone's.
func (s *server) handleWallet(w http.ResponseWriter, r *http.Request) {
	var target int64

	if q := r.URL.Query().Get("user_id"); q != "" {
		id, err := parseID(q)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid user id")
			return
		}
		if !s.mayActOnUser(r, id) {
			writeError(w, http.StatusForbidden, "forbidden")
			return
		}
		target = id
	} else if ident, ok := identityFrom(r); ok {
		target = ident.UserID
	}

	if target == 0 {
		writeError(w, http.StatusBadRequest, "user_id is required")
		return
	}

	balance, err := database.GetUserBalancePaise(r.Context(), s.db, target)
	if err != nil {
		writeDBError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, balanceResponse{BalancePaise: balance})
}
