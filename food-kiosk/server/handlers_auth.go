package server

import (
	"errors"
	"net/http"

	"food-kiosk/database"
)

type loginRequest struct {
	Principal string `json:"principal"`
	Password  string `json:"password"`
}

type loginResponse struct {
	Token string `json:"token"`
	Role  string `json:"role"`
}

func (s *server) handleLogin(w http.ResponseWriter, r *http.Request) {
	var req loginRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	key := loginKey(r, req.Principal)
	if !s.loginLimiter.allow(key) {
		writeError(w, http.StatusTooManyRequests, "too many login attempts")
		return
	}

	cred, err := GetCredentialByPrincipal(r.Context(), s.db, req.Principal)
	if err != nil {
		if errors.Is(err, database.ErrNotFound) {
			// Same response as a wrong password, so the endpoint does not
			// reveal which principals exist.
			writeError(w, http.StatusUnauthorized, "invalid credentials")
			return
		}
		writeDBError(w, err)
		return
	}

	if !CheckPassword(cred.PasswordHash, req.Password) {
		writeError(w, http.StatusUnauthorized, "invalid credentials")
		return
	}

	var userID int64
	if cred.UserID.Valid {
		userID = cred.UserID.Int64
	}

	token, err := s.auth.IssueToken(cred.Principal, cred.Role, userID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}

	s.loginLimiter.reset(key)
	writeJSON(w, http.StatusOK, loginResponse{Token: token, Role: string(cred.Role)})
}

type meResponse struct {
	ID     int64   `json:"id"`
	RollNo string  `json:"roll_no"`
	Name   string  `json:"name"`
	Phone  *string `json:"phone"`
	Role   string  `json:"role"`
}

func (s *server) handleMe(w http.ResponseWriter, r *http.Request) {
	ident, ok := identityFrom(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "authentication required")
		return
	}

	// An admin principal has no users row, so report the token's own view.
	if ident.Role != RoleCustomer || ident.UserID == 0 {
		writeJSON(w, http.StatusOK, map[string]any{
			"id":      nil,
			"roll_no": ident.Principal,
			"name":    nil,
			"phone":   nil,
			"role":    string(ident.Role),
		})
		return
	}

	user, err := database.GetUserByID(r.Context(), s.db, ident.UserID)
	if err != nil {
		writeDBError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, meResponse{
		ID:     user.ID,
		RollNo: user.RollNo,
		Name:   user.Name,
		Phone:  user.Phone,
		Role:   string(ident.Role),
	})
}
