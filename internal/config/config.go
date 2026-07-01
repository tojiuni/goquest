package config

import (
	"os"
	"strconv"
	"strings"
)

type Config struct {
	ServicePort string
	BotPort     string

	PostgresHost     string
	PostgresPort     string
	PostgresUser     string
	PostgresDB       string
	PostgresPassword string

	RedisURL string

	JWKSUrl   string
	SkipAuth  bool
	PublicURL string // SPA public URL, used for magic-link emails

	// SMTP (Phase H4) — empty SMTPHost = log-only dev mode
	SMTPHost     string
	SMTPPort     int
	SMTPUser     string
	SMTPPassword string
	SMTPFrom     string

	// VCS webhook (Task 5)
	VCSGitHubWebhookSecret string

	GoquestServiceURL  string
	GoquestAPIKey      string
	DefaultProjectID   string
	NuclexEventsStream string

	// SpiceDB
	SpiceDBEndpoint string
	SpiceDBToken    string
	SpiceDBInsecure bool
	SpiceDBEnforce  bool

	// CORS
	CORSOrigins []string

	// Contract plane proxy
	ContractPlaneURL string // env: CONTRACT_PLANE_URL
}

func Load() *Config {
	return &Config{
		ServicePort: envOrDefault("SERVICE_PORT", "8080"),
		BotPort:     envOrDefault("BOT_PORT", "8081"),

		PostgresHost:     os.Getenv("POSTGRES_HOST"),
		PostgresPort:     envOrDefault("POSTGRES_PORT", "5432"),
		PostgresUser:     envOrDefault("POSTGRES_USER", "goquest"),
		PostgresDB:       envOrDefault("POSTGRES_DB", "goquest"),
		PostgresPassword: os.Getenv("POSTGRES_APP_PASSWORD"),

		RedisURL: envOrDefault("REDIS_URL", "redis://redis-master.taxon.svc:6379"),

		JWKSUrl:   os.Getenv("JWKS_URL"),
		SkipAuth:  parseBool(os.Getenv("SKIP_AUTH"), false),
		PublicURL: envOrDefault("GOQUEST_PUBLIC_URL", "https://goquest.toji.homes"),

		SMTPHost:     os.Getenv("SMTP_HOST"),
		SMTPPort:     parseInt(os.Getenv("SMTP_PORT"), 587),
		SMTPUser:     os.Getenv("SMTP_USER"),
		SMTPPassword: os.Getenv("SMTP_PASSWORD"),
		SMTPFrom:     envOrDefault("SMTP_FROM", "Goquest <noreply@toji.homes>"),

		VCSGitHubWebhookSecret: os.Getenv("VCS_GITHUB_WEBHOOK_SECRET"),

		GoquestServiceURL:  os.Getenv("GOQUEST_SERVICE_URL"),
		GoquestAPIKey:      os.Getenv("GOQUEST_API_KEY"),
		DefaultProjectID:   os.Getenv("GOQUEST_DEFAULT_PROJECT_ID"),
		NuclexEventsStream: envOrDefault("NUCLEX_EVENTS_STREAM", "nuclex.events"),

		SpiceDBEndpoint: envOrDefault("SPICEDB_ENDPOINT", "localhost:50051"),
		SpiceDBToken:    os.Getenv("SPICEDB_TOKEN"),
		SpiceDBInsecure: parseBool(os.Getenv("SPICEDB_INSECURE"), true),
		SpiceDBEnforce:  parseBool(os.Getenv("SPICEDB_ENFORCE"), false),

		CORSOrigins: parseStringSlice(os.Getenv("CORS_ALLOWED_ORIGINS"), []string{"https://toji.homes", "https://www.toji.homes"}),

		ContractPlaneURL: os.Getenv("CONTRACT_PLANE_URL"),
	}
}

func (c *Config) PostgresDSN() string {
	return "postgres://" + c.PostgresUser + ":" + c.PostgresPassword +
		"@" + c.PostgresHost + ":" + c.PostgresPort + "/" + c.PostgresDB + "?sslmode=disable"
}

func envOrDefault(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func parseInt(s string, def int) int {
	if s == "" {
		return def
	}
	v, err := strconv.Atoi(s)
	if err != nil {
		return def
	}
	return v
}

func parseStringSlice(s string, def []string) []string {
	if s == "" {
		return def
	}
	var out []string
	for _, v := range strings.Split(s, ",") {
		if t := strings.TrimSpace(v); t != "" {
			out = append(out, t)
		}
	}
	if len(out) == 0 {
		return def
	}
	return out
}

func parseBool(s string, def bool) bool {
	if s == "" {
		return def
	}
	v, err := strconv.ParseBool(s)
	if err != nil {
		return def
	}
	return v
}
