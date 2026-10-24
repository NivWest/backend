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

func TestLoad_GuestAuthDefaults(t *testing.T) {
	t.Setenv("AUTH_GUEST_ENABLED", "")
	t.Setenv("AUTH_GUEST_SESSION_TTL", "")
	t.Setenv("AUTH_GUEST_RATE_LIMIT_PER_MINUTE", "")

	cfg, err := Load("")
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.Auth.GuestEnabled {
		t.Error("Auth.GuestEnabled = true, want default false")
	}
	if cfg.Auth.GuestSessionTTL != 24*time.Hour {
		t.Errorf("Auth.GuestSessionTTL = %v, want default 24h", cfg.Auth.GuestSessionTTL)
	}
	if cfg.Auth.GuestRateLimitPerMinute != 10 {
		t.Errorf("Auth.GuestRateLimitPerMinute = %d, want default 10", cfg.Auth.GuestRateLimitPerMinute)
	}
}

func TestLoad_GuestAuthOverrides(t *testing.T) {
	t.Setenv("AUTH_GUEST_ENABLED", "true")
	t.Setenv("AUTH_GUEST_SESSION_TTL", "90m")
	t.Setenv("AUTH_GUEST_RATE_LIMIT_PER_MINUTE", "3")

	cfg, err := Load("")
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if !cfg.Auth.GuestEnabled {
		t.Error("Auth.GuestEnabled = false, want true")
	}
	if cfg.Auth.GuestSessionTTL != 90*time.Minute {
		t.Errorf("Auth.GuestSessionTTL = %v, want 90m", cfg.Auth.GuestSessionTTL)
	}
	if cfg.Auth.GuestRateLimitPerMinute != 3 {
		t.Errorf("Auth.GuestRateLimitPerMinute = %d, want 3", cfg.Auth.GuestRateLimitPerMinute)
	}
}
