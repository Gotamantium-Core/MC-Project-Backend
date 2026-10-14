package server

import (
	"net/http"
)

type queueResponse struct {
	Head  *int64  `json:"head"`
	Depth int     `json:"depth"`
	Items []int64 `json:"items"`
}

// handleQueue reports the head and depth of one queue lane. The ESP32 nodes do
// not call this — they read MQTT — so it exists for the admin/debug view and
// for the frontend.
func (s *server) handleQueue(w http.ResponseWriter, r *http.Request) {
	lane := r.URL.Query().Get("lane")

	ids, err := s.queueIDs(r, lane)
	if err != nil {
		writeDBError(w, err)
		return
	}

	resp := queueResponse{Depth: len(ids), Items: ids}
	if resp.Items == nil {
		resp.Items = []int64{}
	}
	if len(ids) > 0 {
		head := ids[0]
		resp.Head = &head
	}

	writeJSON(w, http.StatusOK, resp)
}

func (s *server) queueIDs(r *http.Request, lane string) ([]int64, error) {
	switch lane {
	case "a", "kitchen":
		return ListPendingOrderIDsByLane(r.Context(), s.db, lane)
	case "ready":
		return ListReadyOrderIDs(r.Context(), s.db)
	default:
		return nil, invalidLane(lane)
	}
}
