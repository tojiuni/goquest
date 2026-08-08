package apikey

import (
	"time"

	"github.com/google/uuid"
)

type Scope string

const (
	ScopePublic  Scope = "public"
	ScopePrivate Scope = "private"
)

// Well-known capability strings (free-form; service layer enforces what's allowed).
const (
	CapabilityTicketCreate    = "ticket.create"
	CapabilityTicketRead      = "ticket.read"
	CapabilityTicketUpdate    = "ticket.update"
	CapabilityCommentList     = "comment.list"
	CapabilityCommentCreate   = "comment.create"
	CapabilityTicketRelations = "ticket.relations.bulk_read" // GET /api/v1/ticket-relations (service-to-service bulk dump, e.g. brain's importer)
	CapabilityAll             = "*"                          // private keys default to wildcard
)

// APIKey is the persisted record (key_hash is never returned via API).
type APIKey struct {
	ID                  uuid.UUID  `json:"id"`
	ProjectID           uuid.UUID  `json:"project_id"`
	Name                string     `json:"name"`
	CreatedBy           string     `json:"created_by"`
	Scope               Scope      `json:"scope"`
	AllowedCapabilities []string   `json:"allowed_capabilities"`
	AllowedOrigins      []string   `json:"allowed_origins,omitempty"`
	RateLimitPerMin     int        `json:"rate_limit_per_min"`
	DailyQuota          *int       `json:"daily_quota,omitempty"`
	ExpiresAt           *time.Time `json:"expires_at,omitempty"`
	LastUsedAt          *time.Time `json:"last_used_at,omitempty"`
	CreatedAt           time.Time  `json:"created_at"`
}

// CreateResult is returned once on key creation; PlainKey is never stored.
type CreateResult struct {
	APIKey
	PlainKey string `json:"key"`
}

type CreateReq struct {
	Name                string     `json:"name"`
	Scope               Scope      `json:"scope,omitempty"`                // default: private
	AllowedCapabilities []string   `json:"allowed_capabilities,omitempty"` // private→default ['*']; public→required {'ticket.create'}
	AllowedOrigins      []string   `json:"allowed_origins,omitempty"`      // required for public
	RateLimitPerMin     int        `json:"rate_limit_per_min,omitempty"`   // default 60
	DailyQuota          *int       `json:"daily_quota,omitempty"`
	ExpiresAt           *time.Time `json:"expires_at,omitempty"`
}

// HasCapability returns true if the key permits the given capability.
// '*' acts as a wildcard.
func (k *APIKey) HasCapability(cap string) bool {
	for _, c := range k.AllowedCapabilities {
		if c == CapabilityAll || c == cap {
			return true
		}
	}
	return false
}

// OriginAllowed returns true when scope=public and origin matches one of the
// configured allowed_origins, OR when scope=private (which doesn't gate by origin).
func (k *APIKey) OriginAllowed(origin string) bool {
	if k.Scope != ScopePublic {
		return true
	}
	for _, o := range k.AllowedOrigins {
		if o == origin {
			return true
		}
	}
	return false
}
