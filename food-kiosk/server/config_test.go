package server

import (
	"testing"
	"time"
)

// setBaseEnv pins the environment LoadConfig reads so a stray variable in the
// developer's shell cannot change a result.
func setBaseEnv(t *testing.T) {
	t.Helper()
	for _, key := range []string{
		"ADDR", "DB_PATH", "JWT_SECRET", "JWT_TTL", "AUTH_REQUIRED",
		"CORS_ORIGINS", "ADMIN_PRINCIPAL", "ADMIN_PASSWORD",
		"LOGIN_MAX_ATTEMPTS", "LOGIN_WINDOW",
	} {
		t.Setenv(key, "")
	}
}

func TestLoadConfigDefaults(t *testing.T) {
	setBaseEnv(t)
	t.Setenv("JWT_SECRET", "a-sufficiently-long-secret")

	cfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}

	if cfg.Addr != ":8080" || cfg.DBPath != "food-kiosk.db" {
		t.Fatalf("unexpected defaults: %+v", cfg)
	}
	if cfg.JWTTTL != 24*time.Hour || !cfg.AuthRequired {
		t.Fatalf("unexpected auth defaults: %+v", cfg)
	}
	if cfg.LoginMaxAttempts != 10 || cfg.LoginWindow != time.Minute {
		t.Fatalf("unexpected login defaults: %+v", cfg)
	}
}

func TestLoadConfigAuthDisabledWithoutSecret(t *testing.T) {
	setBaseEnv(t)
	t.Setenv("AUTH_REQUIRED", "false")

	cfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if cfg.AuthRequired {
		t.Fatal("AuthRequired should be false")
	}
}

func TestLoadConfigRejectsShortSecret(t *testing.T) {
	setBaseEnv(t)
	t.Setenv("JWT_SECRET", "short")

	if _, err := LoadConfig(); err == nil {
		t.Fatal("expected an error for a short JWT_SECRET")
	}
}

func TestLoadConfigAdminPairing(t *testing.T) {
	setBaseEnv(t)
	t.Setenv("JWT_SECRET", "a-sufficiently-long-secret")

	t.Setenv("ADMIN_PRINCIPAL", "BOSS")
	if _, err := LoadConfig(); err == nil {
		t.Fatal("expected an error when only ADMIN_PRINCIPAL is set")
	}

	t.Setenv("ADMIN_PRINCIPAL", "")
	t.Setenv("ADMIN_PASSWORD", "password")
	if _, err := LoadConfig(); err == nil {
		t.Fatal("expected an error when only ADMIN_PASSWORD is set")
	}

	t.Setenv("ADMIN_PRINCIPAL", "BOSS")
	t.Setenv("ADMIN_PASSWORD", "short")
	if _, err := LoadConfig(); err == nil {
		t.Fatal("expected an error for a short ADMIN_PASSWORD")
	}

	t.Setenv("ADMIN_PASSWORD", "long-enough-password")
	cfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if cfg.AdminPrincipal != "BOSS" {
		t.Fatalf("AdminPrincipal = %q, want BOSS", cfg.AdminPrincipal)
	}
}

func TestLoadConfigRejectsBadValues(t *testing.T) {
	setBaseEnv(t)
	t.Setenv("JWT_SECRET", "a-sufficiently-long-secret")

	t.Setenv("JWT_TTL", "not-a-duration")
	if _, err := LoadConfig(); err == nil {
		t.Fatal("expected an error for a bad JWT_TTL")
	}
	t.Setenv("JWT_TTL", "")

	t.Setenv("LOGIN_WINDOW", "not-a-duration")
	if _, err := LoadConfig(); err == nil {
		t.Fatal("expected an error for a bad LOGIN_WINDOW")
	}
	t.Setenv("LOGIN_WINDOW", "")

	t.Setenv("LOGIN_MAX_ATTEMPTS", "-1")
	if _, err := LoadConfig(); err == nil {
		t.Fatal("expected an error for a negative LOGIN_MAX_ATTEMPTS")
	}
}

func TestSplitCSV(t *testing.T) {
	got := splitCSV(" http://a.com , http://b.com ,, ")
	if len(got) != 2 || got[0] != "http://a.com" || got[1] != "http://b.com" {
		t.Fatalf("splitCSV = %v", got)
	}
	if splitCSV("") != nil {
		t.Fatal("splitCSV of empty should be nil")
	}
}
