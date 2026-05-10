package config

import (
	"os"
	"strconv"
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

	JWKSUrl  string
	SkipAuth bool

	GoquestServiceURL  string
	NuclexEventsStream string

	// SpiceDB
	SpiceDBEndpoint string
	SpiceDBToken    string
	SpiceDBInsecure bool
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

		JWKSUrl:  os.Getenv("JWKS_URL"),
		SkipAuth: parseBool(os.Getenv("SKIP_AUTH"), false),

		GoquestServiceURL:  os.Getenv("GOQUEST_SERVICE_URL"),
		NuclexEventsStream: envOrDefault("NUCLEX_EVENTS_STREAM", "nuclex.events"),

		SpiceDBEndpoint: envOrDefault("SPICEDB_ENDPOINT", "localhost:50051"),
		SpiceDBToken:    os.Getenv("SPICEDB_TOKEN"),
		SpiceDBInsecure: parseBool(os.Getenv("SPICEDB_INSECURE"), true),
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
