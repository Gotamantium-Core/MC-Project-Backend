package server

import (
	"database/sql"
	"log"
	"net/http"
	"time"
)

// server holds the shared dependencies for every handler.
type server struct {
	cfg          Config
	db           *sql.DB
	auth         *Auth
	loginLimiter *loginLimiter
}

// NewRouter builds the HTTP handler for the whole API.
//
// AUTH_REQUIRED=false skips token enforcement entirely; handlers then fall back
// to the user_id supplied in the request. That mode exists for curl demos and
// must not be used where the network is untrusted.
func NewRouter(cfg Config, db *sql.DB) http.Handler {
	s := &server{
		cfg:          cfg,
		db:           db,
		auth:         NewAuth(cfg.JWTSecret, cfg.JWTTTL),
		loginLimiter: newLoginLimiter(cfg.LoginMaxAttempts, cfg.LoginWindow),
	}

	mux := http.NewServeMux()

	mux.HandleFunc("GET /healthz", s.handleHealth)
	mux.HandleFunc("POST /auth/login", s.handleLogin)
	mux.HandleFunc("GET /auth/me", s.protect(s.handleMe))

	// Menu reads are open to any signed-in user (the kiosk shows the menu);
	// every write is admin-only.
	mux.HandleFunc("GET /menu", s.protect(s.handleListMenu))
	mux.HandleFunc("POST /menu", s.adminOnly(s.handleCreateMenu))
	mux.HandleFunc("PUT /menu/{id}", s.adminOnly(s.handleUpdateMenu))
	mux.HandleFunc("PATCH /menu/{id}/availability", s.adminOnly(s.handleSetMenuAvailability))
	mux.HandleFunc("DELETE /menu/{id}", s.adminOnly(s.handleDeleteMenu))

	mux.HandleFunc("POST /users", s.handleCreateUser)
	mux.HandleFunc("POST /users/{id}/topup", s.protect(s.handleTopUp))
	mux.HandleFunc("GET /wallet", s.protect(s.handleWallet))
	mux.HandleFunc("POST /orders", s.protect(s.handleCreateOrder))
	mux.HandleFunc("GET /orders", s.protect(s.handleListOrders))
	mux.HandleFunc("GET /orders/{id}", s.protect(s.handleGetOrder))
	mux.HandleFunc("POST /orders/{id}/cancel", s.protect(s.handleCancelOrder))
	mux.HandleFunc("GET /queue", s.protect(s.handleQueue))

	return recoverMiddleware(logMiddleware(corsMiddleware(cfg.CORSOrigins, mux)))
}

// protect wraps a handler with the auth middleware, or passes it through when
// auth is disabled for testing.
func (s *server) protect(next http.HandlerFunc) http.HandlerFunc {
	if !s.cfg.AuthRequired {
		return next
	}
	return s.auth.requireAuth(next)
}

// adminOnly requires an authenticated admin. With auth disabled it passes
// through, matching protect, so curl testing can reach admin routes too.
func (s *server) adminOnly(next http.HandlerFunc) http.HandlerFunc {
	if !s.cfg.AuthRequired {
		return next
	}
	return s.auth.requireAuth(requireRole(RoleAdmin, next))
}

func (s *server) handleHealth(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// recoverMiddleware turns a panic into a 500 rather than dropping the
// connection, so one bad request cannot take the process down.
func recoverMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if p := recover(); p != nil {
				log.Printf("server: panic handling %s %s: %v", r.Method, r.URL.Path, p)
				writeError(w, http.StatusInternalServerError, "internal server error")
			}
		}()
		next.ServeHTTP(w, r)
	})
}

// statusRecorder remembers the status code so the logger can report it.
type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (rec *statusRecorder) WriteHeader(code int) {
	rec.status = code
	rec.ResponseWriter.WriteHeader(code)
}

func (rec *statusRecorder) Write(b []byte) (int, error) {
	if rec.status == 0 {
		rec.status = http.StatusOK
	}
	return rec.ResponseWriter.Write(b)
}

func logMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rec, r)
		log.Printf("%s %s %d %s", r.Method, r.URL.Path, rec.status, time.Since(start).Round(time.Millisecond))
	})
}

// corsMiddleware answers browser preflights and sets the allow-origin header
// only when the request's Origin is configured. An empty origin list disables
// it, which is the right default for a same-origin or non-browser client.
func corsMiddleware(origins []string, next http.Handler) http.Handler {
	if len(origins) == 0 {
		return next
	}

	allowed := make(map[string]bool, len(origins))
	for _, o := range origins {
		allowed[o] = true
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		if origin != "" && (allowed["*"] || allowed[origin]) {
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Add("Vary", "Origin")
			w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type")
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		}

		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}
