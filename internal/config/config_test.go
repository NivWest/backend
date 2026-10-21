package config

import (
	"testing"
	"time"
)

func TestLoad_ReadsEnvironmentOverrides(t *testing.T) {
	t.Setenv("SERVER_PORT", "9090")
	t.Setenv("DB_MAX_OPEN_CONNS", "7")
	t.Setenv("AUTH_SESSION_TTL", "2h")
	t.Setenv("AUTH_COOKIE_SECURE", "true")

	cfg, err := Load("")
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.Server.Port != "9090" {
		t.Errorf("Server.Port = %q, want 9090", cfg.Server.Port)
	}
	if cfg.Database.MaxOpenConns != 7 {
		t.Errorf("Database.MaxOpenConns = %d, want 7", cfg.Database.MaxOpenConns)
	}
	if cfg.Auth.SessionTTL != 2*time.Hour {
		t.Errorf("Auth.SessionTTL = %v, want 2h", cfg.Auth.SessionTTL)
	}
	if !cfg.Auth.CookieSecure {
		t.Error("Auth.CookieSecure = false, want true")
	}
}

func TestLoad_FallsBackToDefaultsOnInvalidValues(t *testing.T) {
	t.Setenv("DB_MAX_IDLE_CONNS", "not-a-number")
	t.Setenv("SERVER_READ_TIMEOUT", "soon")
	t.Setenv("AUTH_COOKIE_SECURE", "maybe")
	t.Setenv("AUTH_OAUTH_STATE_TTL", "")

	cfg, err := Load("")
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.Database.MaxIdleConns != 10 {
		t.Errorf("MaxIdleConns = %d, want default 10", cfg.Database.MaxIdleConns)
	}
	if cfg.Server.ReadTimeout != 10*time.Second {
		t.Errorf("ReadTimeout = %v, want default 10s", cfg.Server.ReadTimeout)
	}
	if cfg.Auth.CookieSecure {
		t.Error("CookieSecure = true, want default false")
	}
	if cfg.Auth.OAuthStateTTL != 15*time.Minute {
		t.Errorf("OAuthStateTTL = %v, want default 15m", cfg.Auth.OAuthStateTTL)
	}
}
