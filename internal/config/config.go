package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/joho/godotenv"
)

type Config struct {
	Server    Server
	Database  Database
	Redis     Redis
	Avanza    Avanza
	Market    Market
	RateLimit RateLimit
	Auth      Auth
	LLM       LLM
}

type Server struct {
	Host         string
	Port         string
	ReadTimeout  time.Duration
	WriteTimeout time.Duration
	IdleTimeout  time.Duration
}

type Database struct {
	Host         string
	User         string
	Password     string
	DBName       string
	Port         string
	SSLMode      string
	MaxIdleConns int
	MaxOpenConns int
}

type Redis struct {
	Addr     string
	Password string
	DB       int
}

type Avanza struct {
	BaseURL string
	Timeout time.Duration
}

// Market data providers selectable via MARKET_DATA_PROVIDER.
const (
	MarketProviderAvanza    = "avanza"
	MarketProviderSimulated = "simulated"
)

type Market struct {
	Provider          string
	SimControlEnabled bool
}

// LLM providers selectable via LLM_PROVIDER (story NW-14).
const (
	LLMProviderAnthropic = "anthropic"
	LLMProviderFake      = "fake"
)

// DefaultLLMModel is the model when LLM_MODEL is unset.
const DefaultLLMModel = "claude-opus-5-5"

// LLM configures the AI gateway and its provider (story NW-14, docs: platform docs/ai.md).
type LLM struct {
	// Provider is "anthropic" (default) or "fake" (deterministic, dev/test only).
	Provider string
	// Model is the Claude model every feature uses (LLM_MODEL).
	Model string
	// Timeout bounds one model call, retries included (LLM_TIMEOUT).
	Timeout time.Duration
	// DailyTokenBudget is the per-user token budget per UTC day (AI_DAILY_TOKEN_BUDGET).
	DailyTokenBudget int64
	// AnthropicAPIKey is ANTHROPIC_API_KEY; never logged. AnthropicBaseURL overrides the API URL (tests, proxies).
	AnthropicAPIKey  string
	AnthropicBaseURL string
	// Prices is the per-model price table used for the ledger's cost estimate.
	Prices map[string]ModelPrice
}

// ModelPrice is a model's list price in USD per million tokens.
type ModelPrice struct {
	InputPerMTok      float64
	OutputPerMTok     float64
	CacheReadPerMTok  float64
	CacheWritePerMTok float64
}

// DefaultModelPrices is the price table for the ledger's cost estimate: Anthropic list prices in USD per
// million tokens (cache writes at the 5-minute rate, 1.25x input). Update it when prices change; a model
// missing here is recorded with cost 0 and a startup warning. The fake provider is free.
func DefaultModelPrices() map[string]ModelPrice {
	return map[string]ModelPrice{
		"claude-opus-5-5": {InputPerMTok: 4, OutputPerMTok: 20, CacheReadPerMTok: 0.20, CacheWritePerMTok: 5},
		"claude-opus-5":   {InputPerMTok: 5, OutputPerMTok: 25, CacheReadPerMTok: 0.50, CacheWritePerMTok: 6.25},
		"claude-opus-4-8": {InputPerMTok: 5, OutputPerMTok: 25, CacheReadPerMTok: 0.50, CacheWritePerMTok: 6.25},
		"fake":            {},
	}
}

type RateLimit struct {
	RequestsPerSecond int
	Burst             int
}

type Auth struct {
	GoogleClientID     string
	GoogleClientSecret string
	GoogleRedirectURL  string
	FrontendURL        string
	SessionTTL         time.Duration
	OAuthStateTTL      time.Duration
	CookieSecure       bool

	GuestEnabled            bool
	GuestSessionTTL         time.Duration
	GuestRateLimitPerMinute int
}

func Load(envFile string) (*Config, error) {
	if envFile != "" {
		_ = godotenv.Load(envFile)
	}

	redisDB := getInt("REDIS_DB", 0)

	marketProvider, err := getMarketProvider(
		"MARKET_DATA_PROVIDER",
		MarketProviderAvanza,
	)
	if err != nil {
		return nil, err
	}

	llmProvider, err := getChoice("LLM_PROVIDER", LLMProviderAnthropic, LLMProviderAnthropic, LLMProviderFake)
	if err != nil {
		return nil, err
	}

	return &Config{
		Server: Server{
			Host: getEnv("SERVER_HOST", "0.0.0.0"),
			Port: getEnv("SERVER_PORT", "8081"),

			ReadTimeout: getDuration(
				"SERVER_READ_TIMEOUT",
				10*time.Second,
			),

			WriteTimeout: getDuration(
				"SERVER_WRITE_TIMEOUT",
				10*time.Second,
			),

			IdleTimeout: getDuration(
				"SERVER_IDLE_TIMEOUT",
				60*time.Second,
			),
		},

		Database: Database{
			Host:         getEnv("DB_HOST", "localhost"),
			User:         getEnv("DB_USER", "postgres"),
			Password:     getEnv("DB_PASSWORD", "postgres"),
			DBName:       getEnv("DB_NAME", "avanza"),
			Port:         getEnv("DB_PORT", "5432"),
			SSLMode:      getEnv("DB_SSLMODE", "disable"),
			MaxIdleConns: getInt("DB_MAX_IDLE_CONNS", 10),
			MaxOpenConns: getInt("DB_MAX_OPEN_CONNS", 100),
		},

		Redis: Redis{
			Addr:     getEnv("REDIS_ADDR", "localhost:6379"),
			Password: getEnv("REDIS_PASSWORD", ""),
			DB:       redisDB,
		},

		Avanza: Avanza{
			BaseURL: getEnv(
				"AVANZA_BASE_URL",
				"https://www.avanza.se",
			),
			Timeout: getDuration(
				"AVANZA_TIMEOUT",
				10*time.Second,
			),
		},

		Market: Market{
			Provider: marketProvider,
			SimControlEnabled: getBool(
				"MARKET_SIM_CONTROL_ENABLED",
				false,
			),
		},

		RateLimit: RateLimit{
			RequestsPerSecond: getInt(
				"RATE_LIMIT_REQUESTS_PER_SECOND",
				10,
			),
			Burst: getInt(
				"RATE_LIMIT_BURST",
				20,
			),
		},

		Auth: Auth{
			GoogleClientID:     getEnv("GOOGLE_CLIENT_ID", ""),
			GoogleClientSecret: getEnv("GOOGLE_CLIENT_SECRET", ""),
			GoogleRedirectURL:  getEnv("GOOGLE_REDIRECT_URL", "http://localhost:8080/auth/callback"),
			FrontendURL:        getEnv("FRONTEND_URL", "http://localhost:3000"),
			SessionTTL:         getDuration("AUTH_SESSION_TTL", 24*time.Hour),
			OAuthStateTTL:      getDuration("AUTH_OAUTH_STATE_TTL", 15*time.Minute),
			CookieSecure:       getBool("AUTH_COOKIE_SECURE", false),

			GuestEnabled:            getBool("AUTH_GUEST_ENABLED", false),
			GuestSessionTTL:         getDuration("AUTH_GUEST_SESSION_TTL", 24*time.Hour),
			GuestRateLimitPerMinute: getInt("AUTH_GUEST_RATE_LIMIT_PER_MINUTE", 10),
		},

		LLM: LLM{
			Provider:         llmProvider,
			Model:            getNonBlank("LLM_MODEL", DefaultLLMModel),
			Timeout:          getDuration("LLM_TIMEOUT", 60*time.Second),
			DailyTokenBudget: int64(getInt("AI_DAILY_TOKEN_BUDGET", 200000)),
			AnthropicAPIKey:  getEnv("ANTHROPIC_API_KEY", ""),
			AnthropicBaseURL: strings.TrimSpace(getEnv("ANTHROPIC_BASE_URL", "")),
			Prices:           DefaultModelPrices(),
		},
	}, nil
}

// getNonBlank is getEnv, but a value that is blank after trimming also yields defaultValue
// (compose passes unset variables as empty strings).
func getNonBlank(key, defaultValue string) string {
	if value := strings.TrimSpace(getEnv(key, "")); value != "" {
		return value
	}
	return defaultValue
}

// getChoice returns the lower-cased, trimmed value of key, defaultValue when it is unset or blank, or an
// error when it is not one of allowed.
func getChoice(key, defaultValue string, allowed ...string) (string, error) {
	value := strings.ToLower(strings.TrimSpace(getEnv(key, "")))
	if value == "" {
		return defaultValue, nil
	}
	for _, a := range allowed {
		if value == a {
			return value, nil
		}
	}
	return "", fmt.Errorf("invalid %s %q: allowed values are %q", key, value, allowed)
}

func getEnv(key, defaultValue string) string {
	if value, exists := os.LookupEnv(key); exists {
		return value
	}

	return defaultValue
}

func getMarketProvider(key, defaultValue string) (string, error) {
	value := strings.ToLower(strings.TrimSpace(getEnv(key, "")))
	if value == "" {
		return defaultValue, nil
	}

	switch value {
	case MarketProviderAvanza, MarketProviderSimulated:
		return value, nil
	default:
		return "", fmt.Errorf(
			"invalid %s %q: allowed values are %q, %q",
			key,
			value,
			MarketProviderAvanza,
			MarketProviderSimulated,
		)
	}
}

func getInt(key string, defaultValue int) int {
	value := getEnv(key, "")

	if value == "" {
		return defaultValue
	}

	result, err := strconv.Atoi(value)
	if err != nil {
		return defaultValue
	}

	return result
}

func getDuration(
	key string,
	defaultValue time.Duration,
) time.Duration {
	value := getEnv(key, "")

	if value == "" {
		return defaultValue
	}

	result, err := time.ParseDuration(value)
	if err != nil {
		return defaultValue
	}

	return result
}

func getBool(key string, defaultValue bool) bool {
	value := getEnv(key, "")
	if value == "" {
		return defaultValue
	}

	result, err := strconv.ParseBool(value)
	if err != nil {
		return defaultValue
	}
	return result
}
