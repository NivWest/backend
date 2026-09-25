package config

import (
	"os"
	"strconv"
	"time"

	"github.com/joho/godotenv"
)

type Config struct {
	Server    Server
	Database  Database
	Redis     Redis
	Avanza    Avanza
	RateLimit RateLimit
	Auth      Auth
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
}

func Load(envFile string) (*Config, error) {
	if envFile != "" {
		_ = godotenv.Load(envFile)
	}

	redisDB := getInt("REDIS_DB", 0)

	return &Config{
		Server: Server{
			Host: getEnv("SERVER_HOST", "0.0.0.0"),
			Port: getEnv("SERVER_PORT", "8080"),

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
		},
	}, nil
}

func getEnv(key, defaultValue string) string {
	if value, exists := os.LookupEnv(key); exists {
		return value
	}

	return defaultValue
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
