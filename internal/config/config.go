package config

import (
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	App      AppConfig
	Dev      DevConfig
	HTTP     HTTPConfig
	Log      LogConfig
	Postgres PostgresConfig
	Qdrant   QdrantConfig
	OpenAI   OpenAIConfig
}

type AppConfig struct {
	Name string
	Env  string
}

type DevConfig struct {
	UserExternalID string
	UserEmail      string
	UserName       string
}

type HTTPConfig struct {
	Addr            string
	ShutdownTimeout time.Duration
}

type LogConfig struct {
	Level slog.Level
}

type PostgresConfig struct {
	Enabled  bool
	DSN      string
	Host     string
	Port     int
	User     string
	Password string
	Database string
	SSLMode  string
	MaxConns int32
	MinConns int32
}

type QdrantConfig struct {
	URL          string
	APIKey       string
	Collection   string
	VectorSize   int
	DenseVector  string
	SparseVector string
}

type OpenAIConfig struct {
	BaseURL        string
	APIKey         string
	ChatModel      string
	EmbeddingModel string
	RequestTimeout time.Duration
}

func Load() (Config, error) {
	logLevel, err := parseLogLevel(getEnv("LOG_LEVEL", "info"))
	if err != nil {
		return Config{}, err
	}

	shutdownTimeout, err := parseDuration("HTTP_SHUTDOWN_TIMEOUT", "10s")
	if err != nil {
		return Config{}, err
	}

	postgresPort, err := parseInt("POSTGRES_PORT", 5432)
	if err != nil {
		return Config{}, err
	}

	postgresMaxConns, err := parseInt32("POSTGRES_MAX_CONNS", 10)
	if err != nil {
		return Config{}, err
	}

	postgresMinConns, err := parseInt32("POSTGRES_MIN_CONNS", 1)
	if err != nil {
		return Config{}, err
	}

	requestTimeout, err := parseDuration("OPENAI_REQUEST_TIMEOUT", "30s")
	if err != nil {
		return Config{}, err
	}

	qdrantVectorSize, err := parseInt("QDRANT_VECTOR_SIZE", 1536)
	if err != nil {
		return Config{}, err
	}

	return Config{
		App: AppConfig{
			Name: getEnv("APP_NAME", "second-context"),
			Env:  getEnv("APP_ENV", "development"),
		},
		Dev: DevConfig{
			UserExternalID: getEnv("DEV_USER_EXTERNAL_ID", "dev-user"),
			UserEmail:      getEnv("DEV_USER_EMAIL", "dev@secondcontext.local"),
			UserName:       getEnv("DEV_USER_NAME", "Development User"),
		},
		HTTP: HTTPConfig{
			Addr:            getEnv("HTTP_ADDR", ":8080"),
			ShutdownTimeout: shutdownTimeout,
		},
		Log: LogConfig{Level: logLevel},
		Postgres: PostgresConfig{
			Enabled:  getEnvBool("POSTGRES_ENABLED", false),
			DSN:      os.Getenv("POSTGRES_DSN"),
			Host:     getEnv("POSTGRES_HOST", "localhost"),
			Port:     postgresPort,
			User:     getEnv("POSTGRES_USER", "postgres"),
			Password: getEnv("POSTGRES_PASSWORD", "postgres"),
			Database: getEnv("POSTGRES_DB", "second_context"),
			SSLMode:  getEnv("POSTGRES_SSLMODE", "disable"),
			MaxConns: postgresMaxConns,
			MinConns: postgresMinConns,
		},
		Qdrant: QdrantConfig{
			URL:          getEnv("QDRANT_URL", "http://localhost:6333"),
			APIKey:       os.Getenv("QDRANT_API_KEY"),
			Collection:   getEnv("QDRANT_COLLECTION", "memory_items"),
			VectorSize:   qdrantVectorSize,
			DenseVector:  getEnv("QDRANT_DENSE_VECTOR_NAME", "dense"),
			SparseVector: getEnv("QDRANT_SPARSE_VECTOR_NAME", "sparse"),
		},
		OpenAI: OpenAIConfig{
			BaseURL:        getEnv("OPENAI_BASE_URL", "https://api.openai.com/v1"),
			APIKey:         os.Getenv("OPENAI_API_KEY"),
			ChatModel:      getEnv("OPENAI_CHAT_MODEL", "gpt-4.1-mini"),
			EmbeddingModel: getEnv("OPENAI_EMBEDDING_MODEL", "text-embedding-3-small"),
			RequestTimeout: requestTimeout,
		},
	}, nil
}

func (c PostgresConfig) ConnectionString() string {
	if strings.TrimSpace(c.DSN) != "" {
		return c.DSN
	}

	return fmt.Sprintf(
		"postgres://%s:%s@%s:%d/%s?sslmode=%s",
		c.User,
		c.Password,
		c.Host,
		c.Port,
		c.Database,
		c.SSLMode,
	)
}

func getEnv(key, fallback string) string {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}

	return value
}

func getEnvBool(key string, fallback bool) bool {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}

	parsed, err := strconv.ParseBool(value)
	if err != nil {
		return fallback
	}

	return parsed
}

func parseInt(key string, fallback int) (int, error) {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback, nil
	}

	parsed, err := strconv.Atoi(value)
	if err != nil {
		return 0, fmt.Errorf("parse %s: %w", key, err)
	}

	return parsed, nil
}

func parseInt32(key string, fallback int32) (int32, error) {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback, nil
	}

	parsed, err := strconv.ParseInt(value, 10, 32)
	if err != nil {
		return 0, fmt.Errorf("parse %s: %w", key, err)
	}

	return int32(parsed), nil
}

func parseDuration(key, fallback string) (time.Duration, error) {
	value := getEnv(key, fallback)
	parsed, err := time.ParseDuration(value)
	if err != nil {
		return 0, fmt.Errorf("parse %s: %w", key, err)
	}

	return parsed, nil
}

func parseLogLevel(value string) (slog.Level, error) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "debug":
		return slog.LevelDebug, nil
	case "info":
		return slog.LevelInfo, nil
	case "warn":
		return slog.LevelWarn, nil
	case "error":
		return slog.LevelError, nil
	default:
		return 0, fmt.Errorf("unsupported LOG_LEVEL %q", value)
	}
}
