package config

import (
	"strings"
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

func TestLoad_MarketDefaults(t *testing.T) {
	t.Setenv("MARKET_DATA_PROVIDER", "")
	t.Setenv("MARKET_SIM_CONTROL_ENABLED", "")

	cfg, err := Load("")
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.Market.Provider != MarketProviderAvanza {
		t.Errorf("Market.Provider = %q, want %q", cfg.Market.Provider, MarketProviderAvanza)
	}
	if cfg.Market.SimControlEnabled {
		t.Error("Market.SimControlEnabled = true, want default false")
	}
}

func TestLoad_MarketSimulatedWithSimControl(t *testing.T) {
	t.Setenv("MARKET_DATA_PROVIDER", "simulated")
	t.Setenv("MARKET_SIM_CONTROL_ENABLED", "true")

	cfg, err := Load("")
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.Market.Provider != MarketProviderSimulated {
		t.Errorf("Market.Provider = %q, want %q", cfg.Market.Provider, MarketProviderSimulated)
	}
	if !cfg.Market.SimControlEnabled {
		t.Error("Market.SimControlEnabled = false, want true")
	}
}

func TestLoad_MarketProviderIsNormalised(t *testing.T) {
	t.Setenv("MARKET_DATA_PROVIDER", " Simulated ")

	cfg, err := Load("")
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.Market.Provider != MarketProviderSimulated {
		t.Errorf("Market.Provider = %q, want %q", cfg.Market.Provider, MarketProviderSimulated)
	}
}

func TestLoad_InvalidMarketProviderErrors(t *testing.T) {
	t.Setenv("MARKET_DATA_PROVIDER", "bloomberg")

	cfg, err := Load("")
	if err == nil {
		t.Fatalf("Load() = %+v, want error", cfg)
	}
	msg := err.Error()
	for _, want := range []string{"MARKET_DATA_PROVIDER", MarketProviderAvanza, MarketProviderSimulated} {
		if !strings.Contains(msg, want) {
			t.Errorf("error %q does not mention %q", msg, want)
		}
	}
}

func TestLoad_LLM(t *testing.T) {
	cases := []struct {
		name    string
		env     map[string]string
		want    LLM
		wantErr string
	}{
		{
			name: "defaults",
			env:  map[string]string{"LLM_PROVIDER": "", "LLM_MODEL": "", "LLM_TIMEOUT": "", "AI_DAILY_TOKEN_BUDGET": ""},
			want: LLM{Provider: LLMProviderAnthropic, Model: "claude-opus-5-5", Timeout: 60 * time.Second, DailyTokenBudget: 200000},
		},
		{
			name: "fake provider and overrides",
			env:  map[string]string{"LLM_PROVIDER": " Fake ", "LLM_MODEL": "claude-sonnet-5-5", "LLM_TIMEOUT": "5s", "AI_DAILY_TOKEN_BUDGET": "1000"},
			want: LLM{Provider: LLMProviderFake, Model: "claude-sonnet-5-5", Timeout: 5 * time.Second, DailyTokenBudget: 1000},
		},
		{
			name:    "unknown provider",
			env:     map[string]string{"LLM_PROVIDER": "openai"},
			wantErr: "LLM_PROVIDER",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			for k, v := range tc.env {
				t.Setenv(k, v)
			}
			cfg, err := Load("")
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("Load() error = %v, want one mentioning %s", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("Load() error = %v", err)
			}
			got := cfg.LLM
			if got.Provider != tc.want.Provider || got.Model != tc.want.Model || got.Timeout != tc.want.Timeout || got.DailyTokenBudget != tc.want.DailyTokenBudget {
				t.Fatalf("LLM = %+v, want %+v", got, tc.want)
			}
			if p, ok := got.Prices["claude-opus-5-5"]; !ok || p.InputPerMTok != 4 || p.OutputPerMTok != 20 || p.CacheReadPerMTok != 0.20 {
				t.Fatalf("price of claude-opus-5-5 = %+v, want $4/$20/$0.20 per MTok", p)
			}
		})
	}
}

func TestLoad_MarketStream(t *testing.T) {
	tests := []struct {
		name                string
		interval, heartbeat string
		maxPerIP            string
		wantInterval        time.Duration
		wantHeartbeat       time.Duration
		wantMax             int
	}{
		{name: "defaults", wantInterval: 2 * time.Second, wantHeartbeat: 10 * time.Second, wantMax: 5},
		{name: "overrides", interval: "1s", heartbeat: "5s", maxPerIP: "3", wantInterval: time.Second, wantHeartbeat: 5 * time.Second, wantMax: 3},
		{name: "non-positive falls back", interval: "0s", heartbeat: "-1s", maxPerIP: "0", wantInterval: 2 * time.Second, wantHeartbeat: 10 * time.Second, wantMax: 5},
		{name: "invalid falls back", interval: "fast", heartbeat: "x", maxPerIP: "many", wantInterval: 2 * time.Second, wantHeartbeat: 10 * time.Second, wantMax: 5},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("MARKET_STREAM_INTERVAL", tt.interval)
			t.Setenv("MARKET_STREAM_HEARTBEAT", tt.heartbeat)
			t.Setenv("MARKET_STREAM_MAX_PER_IP", tt.maxPerIP)
			cfg, err := Load("")
			if err != nil {
				t.Fatal(err)
			}
			m := cfg.Market
			if m.StreamInterval != tt.wantInterval || m.StreamHeartbeat != tt.wantHeartbeat || m.StreamMaxPerIP != tt.wantMax {
				t.Errorf("stream config = %s %s %d, want %s %s %d", m.StreamInterval, m.StreamHeartbeat, m.StreamMaxPerIP,
					tt.wantInterval, tt.wantHeartbeat, tt.wantMax)
			}
		})
	}
}

func TestLoad_MatchingInterval(t *testing.T) {
	tests := []struct {
		name, env string
		want      time.Duration
	}{
		{name: "unset defaults to 1s", env: "", want: time.Second},
		{name: "compose value", env: "1s", want: time.Second},
		{name: "override", env: "250ms", want: 250 * time.Millisecond},
		{name: "minimum accepted", env: "100ms", want: 100 * time.Millisecond},
		{name: "below minimum falls back", env: "10ms", want: time.Second},
		{name: "zero falls back", env: "0s", want: time.Second},
		{name: "negative falls back", env: "-1s", want: time.Second},
		{name: "garbage falls back", env: "soon", want: time.Second},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("MATCHING_INTERVAL", tt.env)
			cfg, err := Load("")
			if err != nil {
				t.Fatalf("Load() error = %v", err)
			}
			if cfg.Matching.Interval != tt.want {
				t.Errorf("Matching.Interval = %v, want %v", cfg.Matching.Interval, tt.want)
			}
		})
	}
}

func TestLoad_Analytics(t *testing.T) {
	tests := []struct {
		name         string
		interval     string
		riskFree     string
		vaultAPY     string
		wantInterval time.Duration
		wantRiskFree float64
		wantVaultAPY float64
	}{
		{"defaults", "", "", "", time.Hour, 0.051, 0.051},
		{"overrides", "15m", "0.035", "0.045", 15 * time.Minute, 0.035, 0.045},
		{"zero rate is allowed", "1h", "0", "0.051", time.Hour, 0, 0.051},
		{"risk free rate defaults to vault apy", "1h", "", "0.075", time.Hour, 0.075, 0.075},
		{"invalid values fall back", "often", "two percent", "five percent", time.Hour, 0.051, 0.051},
		{"non-positive interval falls back", "-5m", "NaN", "NaN", time.Hour, 0.051, 0.051},
		{"zero interval falls back", "0s", "+Inf", "+Inf", time.Hour, 0.051, 0.051},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("SNAPSHOT_INTERVAL", tt.interval)
			t.Setenv("RISK_FREE_RATE", tt.riskFree)
			t.Setenv("VAULT_APY", tt.vaultAPY)
			cfg, err := Load("")
			if err != nil {
				t.Fatal(err)
			}
			if cfg.Analytics.SnapshotInterval != tt.wantInterval || cfg.Analytics.RiskFreeRate != tt.wantRiskFree {
				t.Errorf("Analytics = %+v, want %v / %v", cfg.Analytics, tt.wantInterval, tt.wantRiskFree)
			}
			if cfg.Vault.APY != tt.wantVaultAPY {
				t.Errorf("Vault.APY = %v, want %v", cfg.Vault.APY, tt.wantVaultAPY)
			}
		})
	}
}
