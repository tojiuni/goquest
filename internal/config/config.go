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
	// NuclexExcludeProjectIDs is a comma-separated list of project UUIDs
	// whose tickets never emit nuclex events (see
	// Service.SetNuclexExcludeProjectIDs) — empty means no exclusion.
	NuclexExcludeProjectIDs string

	// SpiceDB
	SpiceDBEndpoint string
	SpiceDBToken    string
	SpiceDBInsecure bool
	SpiceDBEnforce  bool

	// Lymphhub CLI SSO session verification — empty means disabled.
	LymphhubSessionAddr string

	// OIDC client ids (token aud) of the interactive UI whose logins count as a
	// person for approval decisions (APPROVAL_HUMAN_CLIENT_IDS, comma-separated,
	// e.g. "goquest-ui"). Empty means nobody is human: user_direct approvals
	// are impossible until it is set.
	ApprovalHumanClientIDs []string

	// Lymphhub actor-lookup service client (goq-user-lookup-proxy) — resolves
	// requester/assignee ids to display names. Empty LymphhubOIDCURL disables
	// the /users/lookup proxy (it always returns an empty array).
	//
	// LymphhubOIDCURL is the in-cluster dial address (e.g.
	// http://oidc.iam.svc.cluster.local:8081); lymphhub routes tenants by
	// subdomain, so LymphhubOIDCHost (the public tenant hostname, e.g.
	// toji.idp.toji.homes) must also be set and is sent as the Host header
	// on every request — the dial address alone is not tenant-scoped
	// (who-lymphhub-actor-lookup live-verification finding, 2026-07-22).
	LymphhubOIDCURL             string
	LymphhubOIDCHost            string
	LymphhubServiceClientID     string
	LymphhubServiceClientSecret string

	// brain gRPC (goq-refdoc-goquest-brain-client) — ticket reference-doc
	// lookup mediation. Empty BrainGRPCAddr disables /tickets/{id}/docs
	// (always returns {"docs":[]}).
	BrainGRPCAddr string
	BrainTenant   string

	// cogito gRPC (goq-cogito-subtasks-from-text/-chat) — SplitContent/
	// DecomposeQuestion RPCs backing POST /tickets/{parent_id}/subtasks/
	// from-text and /subtasks/from-chat. Empty CogitoGRPCAddr disables both
	// endpoints (always 502s). Confirmed real in-cluster value via live E2E:
	// "cogito-svc.cogito-svc.svc:50051" (also matches whoami's
	// COGITO_GRPC_ENDPOINT). CogitoBearerToken is the shared secret cogito's
	// gRPC interceptor checks on every call (WHOAMI_TO_COGITO_TOKEN
	// server-side, Vault secret/neunexus/cogito-svc#whoami-token) — without
	// it every call fails UNAUTHENTICATED "invalid bearer token" even with a
	// reachable address.
	CogitoGRPCAddr    string
	CogitoBearerToken string

	// tojihub Connect API (infra-tojihub-goquest-integration) — syncs ticket
	// due dates to a tojihub calendar via POST /connect/v1/{Upsert,Delete}Event.
	// Empty TojihubConnectURL disables calendar sync entirely (SetCalendarClient
	// is never called). TojihubServiceToken is the shared secret tojihub's
	// Connect handlers check via X-Tojihub-Service-Token (Vault
	// secret/neunexus/tojihub#connect_service_token — VSO wiring into this
	// service's k8s deployment is separate infra work, not part of this repo's
	// code changes).
	TojihubConnectURL   string
	TojihubServiceToken string

	// GitHub App (goq-refdoc-goquest-brain-client) — server-side raw content
	// fetch from the private tojiuni/geneso repo for /docs/content. Empty
	// GitHubAppID disables /docs/content (always 404s).
	GitHubAppID             string
	GitHubAppInstallationID string
	GitHubAppPrivateKeyPEM  string
	GenesoRepoOwner         string
	GenesoRepoName          string

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
		NuclexEventsStream:      envOrDefault("NUCLEX_EVENTS_STREAM", "nuclex.events"),
		NuclexExcludeProjectIDs: os.Getenv("NUCLEX_EXCLUDE_PROJECT_IDS"),

		SpiceDBEndpoint: envOrDefault("SPICEDB_ENDPOINT", "localhost:50051"),
		SpiceDBToken:    os.Getenv("SPICEDB_TOKEN"),
		SpiceDBInsecure: parseBool(os.Getenv("SPICEDB_INSECURE"), true),
		SpiceDBEnforce:  parseBool(os.Getenv("SPICEDB_ENFORCE"), false),

		LymphhubSessionAddr: os.Getenv("LYMPHHUB_SESSION_ADDR"),

		ApprovalHumanClientIDs: splitCSV(os.Getenv("APPROVAL_HUMAN_CLIENT_IDS")),

		LymphhubOIDCURL:             os.Getenv("LYMPHHUB_OIDC_URL"),
		LymphhubOIDCHost:            envOrDefault("LYMPHHUB_OIDC_HOST", "toji.idp.toji.homes"),
		LymphhubServiceClientID:     os.Getenv("LYMPHHUB_SERVICE_CLIENT_ID"),
		LymphhubServiceClientSecret: os.Getenv("LYMPHHUB_SERVICE_CLIENT_SECRET"),

		BrainGRPCAddr: os.Getenv("BRAIN_GRPC_ADDR"),
		BrainTenant:   envOrDefault("BRAIN_TENANT", "toji"),

		CogitoGRPCAddr:    os.Getenv("COGITO_GRPC_ADDR"),
		CogitoBearerToken: os.Getenv("COGITO_BEARER_TOKEN"),

		TojihubConnectURL:   os.Getenv("TOJIHUB_CONNECT_URL"),
		TojihubServiceToken: os.Getenv("TOJIHUB_SERVICE_TOKEN"),

		GitHubAppID:             os.Getenv("GITHUB_APP_ID"),
		GitHubAppInstallationID: os.Getenv("GITHUB_APP_INSTALLATION_ID"),
		GitHubAppPrivateKeyPEM:  os.Getenv("GITHUB_APP_PRIVATE_KEY_PEM"),
		GenesoRepoOwner:         envOrDefault("GENESO_REPO_OWNER", "tojiuni"),
		GenesoRepoName:          envOrDefault("GENESO_REPO_NAME", "geneso"),

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

// splitCSV splits a comma-separated list, trimming blanks and dropping empties.
func splitCSV(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}
