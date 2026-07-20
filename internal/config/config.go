package config

import (
	"fmt"
	"log/slog"
	"net/netip"
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	App      AppConfig
	Auth     AuthConfig
	Dev      DevConfig
	HTTP     HTTPConfig
	Log      LogConfig
	Postgres PostgresConfig
	Qdrant   QdrantConfig
	OpenAI   OpenAIConfig
	Scoring  ScoringConfig
}

type AppConfig struct {
	Name string
	Env  string
}

type AuthConfig struct {
	Enabled bool
	Realm   string
	Tokens  []AuthTokenConfig
}

type AuthTokenConfig struct {
	Subject string
	Token   string
}

type DevConfig struct {
	UserExternalID string
	UserEmail      string
	UserName       string
}

type HTTPConfig struct {
	Addr            string
	ShutdownTimeout time.Duration
	RateLimitRPM    int
	MetricsEnabled  bool
	MetricsPath     string
	TrustedProxies  []netip.Prefix
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

type ScoringConfig struct {
	RetrievalWeight     float64
	RecencyWeight       float64
	ImportanceWeight    float64
	UtilityWeight       float64
	GoalRelevanceWeight float64
	BeliefImpactWeight  float64
	ConfidenceWeight    float64
	RecencyHalfLifeDays float64
	RedundancyThreshold float64
}

func Load() (Config, error) {
	logLevel, err := parseLogLevel(getEnv("LOG_LEVEL", "info"))
	if err != nil {
		return Config{}, err
	}

	authEnabled, err := parseBool("AUTH_ENABLED", false)
	if err != nil {
		return Config{}, err
	}

	metricsEnabled, err := parseBool("HTTP_METRICS_ENABLED", true)
	if err != nil {
		return Config{}, err
	}

	postgresEnabled, err := parseBool("POSTGRES_ENABLED", false)
	if err != nil {
		return Config{}, err
	}

	shutdownTimeout, err := parseDuration("HTTP_SHUTDOWN_TIMEOUT", "10s")
	if err != nil {
		return Config{}, err
	}

	authTokens, err := parseAuthTokens(os.Getenv("AUTH_BEARER_TOKENS"))
	if err != nil {
		return Config{}, err
	}
	if authEnabled && len(authTokens) == 0 {
		return Config{}, fmt.Errorf("AUTH_ENABLED requires at least one AUTH_BEARER_TOKENS entry")
	}

	rateLimitRPM, err := parseInt("HTTP_RATE_LIMIT_REQUESTS_PER_MINUTE", 60)
	if err != nil {
		return Config{}, err
	}

	trustedProxies, err := parseTrustedProxyCIDRs(os.Getenv("HTTP_TRUSTED_PROXY_CIDRS"))
	if err != nil {
		return Config{}, err
	}

	metricsPath := getEnv("HTTP_METRICS_PATH", "/metrics")
	if !strings.HasPrefix(metricsPath, "/") {
		return Config{}, fmt.Errorf("HTTP_METRICS_PATH must start with '/'")
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

	retrievalWeight, err := parseFloat64("SCORING_RETRIEVAL_WEIGHT", 0.35)
	if err != nil {
		return Config{}, err
	}
	recencyWeight, err := parseFloat64("SCORING_RECENCY_WEIGHT", 0.15)
	if err != nil {
		return Config{}, err
	}
	importanceWeight, err := parseFloat64("SCORING_IMPORTANCE_WEIGHT", 0.15)
	if err != nil {
		return Config{}, err
	}
	utilityWeight, err := parseFloat64("SCORING_UTILITY_WEIGHT", 0.15)
	if err != nil {
		return Config{}, err
	}
	goalRelevanceWeight, err := parseFloat64("SCORING_GOAL_RELEVANCE_WEIGHT", 0.10)
	if err != nil {
		return Config{}, err
	}
	beliefImpactWeight, err := parseFloat64("SCORING_BELIEF_IMPACT_WEIGHT", 0.05)
	if err != nil {
		return Config{}, err
	}
	confidenceWeight, err := parseFloat64("SCORING_CONFIDENCE_WEIGHT", 0.05)
	if err != nil {
		return Config{}, err
	}
	recencyHalfLifeDays, err := parseFloat64("SCORING_RECENCY_HALF_LIFE_DAYS", 30)
	if err != nil {
		return Config{}, err
	}
	redundancyThreshold, err := parseFloat64("SCORING_REDUNDANCY_THRESHOLD", 0.82)
	if err != nil {
		return Config{}, err
	}

	return Config{
		App: AppConfig{
			Name: getEnv("APP_NAME", "second-context"),
			Env:  getEnv("APP_ENV", "development"),
		},
		Auth: AuthConfig{
			Enabled: authEnabled,
			Realm:   getEnv("AUTH_REALM", "second-context"),
			Tokens:  authTokens,
		},
		Dev: DevConfig{
			UserExternalID: getEnv("DEV_USER_EXTERNAL_ID", "dev-user"),
			UserEmail:      getEnv("DEV_USER_EMAIL", "dev@secondcontext.local"),
			UserName:       getEnv("DEV_USER_NAME", "Development User"),
		},
		HTTP: HTTPConfig{
			Addr:            getEnv("HTTP_ADDR", ":8080"),
			ShutdownTimeout: shutdownTimeout,
			RateLimitRPM:    rateLimitRPM,
			MetricsEnabled:  metricsEnabled,
			MetricsPath:     metricsPath,
			TrustedProxies:  trustedProxies,
		},
		Log: LogConfig{Level: logLevel},
		Postgres: PostgresConfig{
			Enabled:  postgresEnabled,
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
		Scoring: ScoringConfig{
			RetrievalWeight:     retrievalWeight,
			RecencyWeight:       recencyWeight,
			ImportanceWeight:    importanceWeight,
			UtilityWeight:       utilityWeight,
			GoalRelevanceWeight: goalRelevanceWeight,
			BeliefImpactWeight:  beliefImpactWeight,
			ConfidenceWeight:    confidenceWeight,
			RecencyHalfLifeDays: recencyHalfLifeDays,
			RedundancyThreshold: redundancyThreshold,
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

func parseBool(key string, fallback bool) (bool, error) {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback, nil
	}

	parsed, err := strconv.ParseBool(value)
	if err != nil {
		return false, fmt.Errorf("parse %s: %w", key, err)
	}

	return parsed, nil
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

func parseFloat64(key string, fallback float64) (float64, error) {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback, nil
	}

	parsed, err := strconv.ParseFloat(value, 64)
	if err != nil {
		return 0, fmt.Errorf("parse %s: %w", key, err)
	}

	return parsed, nil
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

func parseAuthTokens(value string) ([]AuthTokenConfig, error) {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return nil, nil
	}

	entries := strings.Split(trimmed, ",")
	tokens := make([]AuthTokenConfig, 0, len(entries))
	subjects := make(map[string]struct{}, len(entries))
	tokenValues := make(map[string]struct{}, len(entries))
	for index, entry := range entries {
		item := strings.TrimSpace(entry)
		if item == "" {
			return nil, fmt.Errorf("invalid AUTH_BEARER_TOKENS entry %d: expected subject=token", index+1)
		}

		subject, token, hasSubject := strings.Cut(item, "=")
		subject = strings.TrimSpace(subject)
		token = strings.TrimSpace(token)
		if !hasSubject || subject == "" || token == "" {
			return nil, fmt.Errorf("invalid AUTH_BEARER_TOKENS entry %d: expected non-empty subject=token", index+1)
		}
		if _, exists := subjects[subject]; exists {
			return nil, fmt.Errorf("duplicate AUTH_BEARER_TOKENS subject at entry %d", index+1)
		}
		if _, exists := tokenValues[token]; exists {
			return nil, fmt.Errorf("duplicate AUTH_BEARER_TOKENS token at entry %d", index+1)
		}

		subjects[subject] = struct{}{}
		tokenValues[token] = struct{}{}
		tokens = append(tokens, AuthTokenConfig{Subject: subject, Token: token})
	}

	return tokens, nil
}

func parseTrustedProxyCIDRs(value string) ([]netip.Prefix, error) {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return nil, nil
	}

	entries := strings.Split(trimmed, ",")
	prefixes := make([]netip.Prefix, 0, len(entries))
	for index, entry := range entries {
		item := strings.TrimSpace(entry)
		prefix, err := netip.ParsePrefix(item)
		if err != nil {
			return nil, fmt.Errorf("parse HTTP_TRUSTED_PROXY_CIDRS entry %d: %w", index+1, err)
		}
		if prefix.Addr().Is4In6() && prefix.Bits() < 96 {
			return nil, fmt.Errorf("parse HTTP_TRUSTED_PROXY_CIDRS entry %d: IPv4-mapped prefix must be at least /96", index+1)
		}
		prefixes = append(prefixes, normalizeIPPrefix(prefix))
	}
	return prefixes, nil
}

func normalizeIPPrefix(prefix netip.Prefix) netip.Prefix {
	address := prefix.Addr()
	if !address.Is4In6() {
		return prefix.Masked()
	}
	return netip.PrefixFrom(address.Unmap(), prefix.Bits()-96).Masked()
}
