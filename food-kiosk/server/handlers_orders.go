package server

import (
	"net/http"

	"food-kiosk/database"
)

type orderLineRequest struct {
	Code     string `json:"code"`
	Quantity int64  `json:"quantity"`
}

type createOrderRequest struct {
	UserID int64              `json:"user_id"`
	Lines  []orderLineRequest `json:"lines"`
}

type createOrderResponse struct {
	ID         int64 `json:"id"`
	TotalPaise int64 `json:"total_paise"`
}

// handleCreateOrder places an order for the authenticated customer (or, for an
// admin, for the user_id in the body). The database layer does the atomic
// write; this only resolves identity, maps errors, and reports the total.
func (s *server) handleCreateOrder(w http.ResponseWriter, r *http.Request) {
	var req createOrderRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	userID, ok := s.orderUserID(r, req.UserID)
	if !ok {
		writeError(w, http.StatusBadRequest, "user_id is required")
		return
	}

	lines := make([]database.OrderLine, len(req.Lines))
	for i, l := range req.Lines {
		lines[i] = database.OrderLine{Code: l.Code, Quantity: l.Quantity}
	}

	orderID, err := database.CreateOrder(r.Context(), s.db, userID, lines)
	if err != nil {
		writeDBError(w, err)
		return
	}

	order, err := database.GetOrderByID(r.Context(), s.db, orderID)
	if err != nil {
		writeDBError(w, err)
		return
	}

	writeJSON(w, http.StatusCreated, createOrderResponse{ID: orderID, TotalPaise: order.TotalPaise})
}

// orderUserID resolves whose order this is. A customer can only ever order for
// themselves (the token wins over the body); an admin may name a user; with
// auth off, the body is trusted for curl testing.
func (s *server) orderUserID(r *http.Request, bodyUserID int64) (int64, bool) {
	ident, ok := identityFrom(r)
	if !ok {
		return bodyUserID, bodyUserID > 0
	}
	if ident.IsAdmin() {
		return bodyUserID, bodyUserID > 0
	}
	// Customer: token identity overrides the body.
	return ident.UserID, ident.UserID > 0
}

func (s *server) handleCancelOrder(w http.ResponseWriter, r *http.Request) {
	orderID, err := parseID(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid order id")
		return
	}

	order, err := database.GetOrderByID(r.Context(), s.db, orderID)
	if err != nil {
		writeDBError(w, err)
		return
	}

	if !s.mayActOnUser(r, order.UserID) {
		writeError(w, http.StatusForbidden, "forbidden")
		return
	}

	if err := database.CancelOrder(r.Context(), s.db, orderID); err != nil {
		writeDBError(w, err)
		return
	}

	balance, err := database.GetUserBalancePaise(r.Context(), s.db, order.UserID)
	if err != nil {
		writeDBError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, balanceResponse{BalancePaise: balance})
}

type orderSummaryResponse struct {
	ID          int64   `json:"id"`
	UserID      int64   `json:"user_id"`
	Status      string  `json:"status"`
	TotalPaise  int64   `json:"total_paise"`
	CreatedAt   string  `json:"created_at"`
	CompletedAt *string `json:"completed_at"`
}

func orderToSummary(o database.Order) orderSummaryResponse {
	return orderSummaryResponse{
		ID:          o.ID,
		UserID:      o.UserID,
		Status:      string(o.Status),
		TotalPaise:  o.TotalPaise,
		CreatedAt:   o.CreatedAt,
		CompletedAt: o.CompletedAt,
	}
}

type orderItemResponse struct {
	ID             int64  `json:"id"`
	MenuID         int64  `json:"menu_id"`
	ItemName       string `json:"item_name"`
	UnitPricePaise int64  `json:"unit_price_paise"`
	Quantity       int64  `json:"quantity"`
	LineTotalPaise int64  `json:"line_total_paise"`
}

type orderListResponse struct {
	Orders []orderSummaryResponse `json:"orders"`
}

type orderDetailResponse struct {
	orderSummaryResponse
	Items []orderItemResponse `json:"items"`
}

// handleListOrders returns order history. A customer sees their own; an admin
// sees one user's history with ?user_id=, or every order in a state with
// ?status=. `?status=` also filters a customer's own history.
func (s *server) handleListOrders(w http.ResponseWriter, r *http.Request) {
	statusParam := r.URL.Query().Get("status")
	var status database.OrderStatus
	if statusParam != "" {
		status = database.OrderStatus(statusParam)
		if !validOrderStatus(status) {
			writeError(w, http.StatusBadRequest, "invalid status")
			return
		}
	}

	var (
		orders []database.Order
		err    error
	)

	if queryUser := r.URL.Query().Get("user_id"); queryUser != "" {
		id, parseErr := parseID(queryUser)
		if parseErr != nil {
			writeError(w, http.StatusBadRequest, "invalid user id")
			return
		}
		if !s.mayActOnUser(r, id) {
			writeError(w, http.StatusForbidden, "forbidden")
			return
		}
		if orders, err = database.ListOrdersByUser(r.Context(), s.db, id); err != nil {
			writeDBError(w, err)
			return
		}
		orders = filterOrdersByStatus(orders, status)
	} else if ident, ok := identityFrom(r); ok && ident.UserID > 0 {
		if orders, err = database.ListOrdersByUser(r.Context(), s.db, ident.UserID); err != nil {
			writeDBError(w, err)
			return
		}
		orders = filterOrdersByStatus(orders, status)
	} else if status != "" {
		if orders, err = database.ListOrdersByStatus(r.Context(), s.db, status); err != nil {
			writeDBError(w, err)
			return
		}
	} else {
		writeError(w, http.StatusBadRequest, "user_id or status is required")
		return
	}

	resp := orderListResponse{Orders: make([]orderSummaryResponse, len(orders))}
	for i, o := range orders {
		resp.Orders[i] = orderToSummary(o)
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *server) handleGetOrder(w http.ResponseWriter, r *http.Request) {
	orderID, err := parseID(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid order id")
		return
	}

	order, err := database.GetOrderByID(r.Context(), s.db, orderID)
	if err != nil {
		writeDBError(w, err)
		return
	}

	if !s.mayActOnUser(r, order.UserID) {
		writeError(w, http.StatusForbidden, "forbidden")
		return
	}

	resp := orderDetailResponse{
		orderSummaryResponse: orderToSummary(*order),
		Items:                make([]orderItemResponse, len(order.Items)),
	}
	for i, item := range order.Items {
		resp.Items[i] = orderItemResponse{
			ID:             item.ID,
			MenuID:         item.MenuID,
			ItemName:       item.ItemName,
			UnitPricePaise: item.UnitPricePaise,
			Quantity:       item.Quantity,
			LineTotalPaise: item.LineTotalPaise,
		}
	}
	writeJSON(w, http.StatusOK, resp)
}

func validOrderStatus(status database.OrderStatus) bool {
	switch status {
	case database.OrderStatusPending, database.OrderStatusCompleted, database.OrderStatusCancelled:
		return true
	default:
		return false
	}
}

// filterOrdersByStatus returns orders matching status, or all of them when
// status is empty.
func filterOrdersByStatus(orders []database.Order, status database.OrderStatus) []database.Order {
	if status == "" {
		return orders
	}
	filtered := orders[:0:0]
	for _, o := range orders {
		if o.Status == status {
			filtered = append(filtered, o)
		}
	}
	return filtered
}
