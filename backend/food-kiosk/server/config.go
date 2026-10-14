package server

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

// Config is the runtime configuration, read from the environment.
type Config struct {
	Addr         string
	DBPath       string
	JWTSecret    []byte
	JWTTTL       time.Duration
	AuthRequired bool
	CORSOrigins  []string

	// AdminPrincipal/AdminPassword bootstrap an admin credential on startup
	// when both are set. Empty means "do not touch the credentials table".
	AdminPrincipal string
	AdminPassword  string

	// LoginMaxAttempts/LoginWindow throttle POST /auth/login per principal.
	// A max of 0 disables the limiter.
	LoginMaxAttempts int
	LoginWindow      time.Duration
}

// minSecretLen is a floor on the HMAC key. A short shared secret is the easiest
// way to make every issued token forgeable, so refuse to start with one when
// auth is on.
const minSecretLen = 16

// minAdminPasswordLen is a floor on the bootstrapped admin password. An admin
// account can edit the menu and see every order, so it is held to a higher bar
// than a customer PIN.
const minAdminPasswordLen = 8

// LoadConfig reads configuration from the environment, applying development
// defaults. JWT_SECRET is mandatory unless AUTH_REQUIRED=false, which exists
// only so the routes can be exercised with curl during a demo.
func LoadConfig() (Config, error) {
	ttl, err := time.ParseDuration(Getenv("JWT_TTL", "24h"))
	if err != nil {
		return Config{}, fmt.Errorf("parse JWT_TTL: %w", err)
	}

	authRequired, err := parseBool(Getenv("AUTH_REQUIRED", "true"))
	if err != nil {
		return Config{}, fmt.Errorf("parse AUTH_REQUIRED: %w", err)
	}

	loginWindow, err := time.ParseDuration(Getenv("LOGIN_WINDOW", "1m"))
	if err != nil {
		return Config{}, fmt.Errorf("parse LOGIN_WINDOW: %w", err)
	}

	loginMax, err := strconv.Atoi(Getenv("LOGIN_MAX_ATTEMPTS", "10"))
	if err != nil || loginMax < 0 {
		return Config{}, fmt.Errorf("LOGIN_MAX_ATTEMPTS must be a non-negative integer")
	}

	cfg := Config{
		Addr:             Getenv("ADDR", ":8080"),
		DBPath:           Getenv("DB_PATH", "food-kiosk.db"),
		JWTSecret:        []byte(os.Getenv("JWT_SECRET")),
		JWTTTL:           ttl,
		AuthRequired:     authRequired,
		CORSOrigins:      splitCSV(os.Getenv("CORS_ORIGINS")),
		AdminPrincipal:   Getenv("ADMIN_PRINCIPAL", ""),
		AdminPassword:    os.Getenv("ADMIN_PASSWORD"),
		LoginMaxAttempts: loginMax,
		LoginWindow:      loginWindow,
	}

	if (cfg.AdminPrincipal == "") != (cfg.AdminPassword == "") {
		return Config{}, fmt.Errorf("ADMIN_PRINCIPAL and ADMIN_PASSWORD must be set together")
	}
	if cfg.AdminPassword != "" && len(cfg.AdminPassword) < minAdminPasswordLen {
		return Config{}, fmt.Errorf("ADMIN_PASSWORD must be at least %d bytes", minAdminPasswordLen)
	}

	if authRequired {
		if len(cfg.JWTSecret) == 0 {
			return Config{}, fmt.Errorf("JWT_SECRET is required when AUTH_REQUIRED is true")
		}
		if len(cfg.JWTSecret) < minSecretLen {
			return Config{}, fmt.Errorf("JWT_SECRET must be at least %d bytes", minSecretLen)
		}
	}

	return cfg, nil
}

func Getenv(key, fallback string) string {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		return v
	}
	return fallback
}

func parseBool(s string) (bool, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "1", "true", "yes", "on":
		return true, nil
	case "0", "false", "no", "off":
		return false, nil
	default:
		return false, fmt.Errorf("not a boolean: %q", s)
	}
}

func splitCSV(s string) []string {
	var out []string
	for _, part := range strings.Split(s, ",") {
		if p := strings.TrimSpace(part); p != "" {
			out = append(out, p)
		}
	}
	return out
}
