package apikey

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"time"

	"github.com/google/uuid"
)

type Service struct{ store Store }

func NewService(store Store) *Service { return &Service{store: store} }

// Create generates a new API key for the given project.
// The plain-text key is returned once and never stored.
func (s *Service) Create(ctx context.Context, projectID uuid.UUID, req CreateReq, createdBy string) (*CreateResult, error) {
	if req.Name == "" {
		return nil, fmt.Errorf("name is required")
	}

	scope := req.Scope
	if scope == "" {
		scope = ScopePrivate
	}
	if scope != ScopePrivate && scope != ScopePublic {
		return nil, fmt.Errorf("invalid scope %q", scope)
	}

	caps := req.AllowedCapabilities
	if scope == ScopePrivate && len(caps) == 0 {
		caps = []string{CapabilityAll}
	}
	if scope == ScopePublic {
		// Public keys MUST be limited to ticket.create only.
		// Allow callers to omit it (default), but reject any other set.
		if len(caps) == 0 {
			caps = []string{CapabilityTicketCreate}
		}
		if len(caps) != 1 || caps[0] != CapabilityTicketCreate {
			return nil, fmt.Errorf("public scope keys must have allowed_capabilities = [%q]", CapabilityTicketCreate)
		}
		if len(req.AllowedOrigins) == 0 {
			return nil, fmt.Errorf("public scope keys require at least one allowed_origin")
		}
	}

	rateLimit := req.RateLimitPerMin
	if rateLimit <= 0 {
		rateLimit = 60
	}

	plain, keyHash, err := generateKey()
	if err != nil {
		return nil, fmt.Errorf("generate key: %w", err)
	}

	k := &APIKey{
		ID:                  uuid.New(),
		ProjectID:           projectID,
		Name:                req.Name,
		CreatedBy:           createdBy,
		Scope:               scope,
		AllowedCapabilities: caps,
		AllowedOrigins:      req.AllowedOrigins,
		RateLimitPerMin:     rateLimit,
		DailyQuota:          req.DailyQuota,
		ExpiresAt:           req.ExpiresAt,
		CreatedAt:           time.Now().UTC(),
	}

	if err := s.store.Create(ctx, k, keyHash); err != nil {
		return nil, fmt.Errorf("store.Create: %w", err)
	}

	return &CreateResult{APIKey: *k, PlainKey: plain}, nil
}

func (s *Service) List(ctx context.Context, projectID uuid.UUID) ([]*APIKey, error) {
	return s.store.List(ctx, projectID)
}

func (s *Service) Delete(ctx context.Context, id uuid.UUID) error {
	return s.store.Delete(ctx, id)
}

// Verify validates a plain-text API key and returns the associated record.
// It also updates last_used_at asynchronously.
func (s *Service) Verify(ctx context.Context, plainKey string) (*APIKey, error) {
	h := hashKey(plainKey)
	k, err := s.store.FindByHash(ctx, h)
	if err != nil {
		return nil, err
	}
	go s.store.TouchLastUsed(context.Background(), k.ID) //nolint:errcheck
	return k, nil
}

// VerifyAPIKey satisfies auth.KeyVerifier — returns created_by as the requester ID.
func (s *Service) VerifyAPIKey(ctx context.Context, plainKey string) (string, error) {
	k, err := s.Verify(ctx, plainKey)
	if err != nil {
		return "", err
	}
	return k.CreatedBy, nil
}

// VerifyAPIKeyMeta satisfies auth.RichKeyVerifier — returns the requester id
// plus rich metadata (scope, capabilities, origins, rate limit) used by the
// per-route capability/origin/rate-limit middleware.
//
// Returns (requesterID string, meta *auth.APIKeyMeta, err error). We cannot
// import auth here (would create a cycle), so this method returns a serializable
// MetaInfo struct that the auth layer wraps.
func (s *Service) VerifyAPIKeyMeta(ctx context.Context, plainKey string) (string, *MetaInfo, error) {
	k, err := s.Verify(ctx, plainKey)
	if err != nil {
		return "", nil, err
	}
	return k.CreatedBy, &MetaInfo{
		ID:                  k.ID,
		ProjectID:           k.ProjectID,
		Scope:               string(k.Scope),
		AllowedCapabilities: k.AllowedCapabilities,
		AllowedOrigins:      k.AllowedOrigins,
		RateLimitPerMin:     k.RateLimitPerMin,
	}, nil
}

// MetaInfo mirrors auth.APIKeyMeta but stays in the apikey package to avoid
// the apikey↔auth import cycle. The cmd wiring layer adapts it to auth.APIKeyMeta.
type MetaInfo struct {
	ID                  uuid.UUID
	ProjectID           uuid.UUID
	Scope               string
	AllowedCapabilities []string
	AllowedOrigins      []string
	RateLimitPerMin     int
}

func generateKey() (plain, keyHash string, err error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", "", err
	}
	plain = "gqk_" + hex.EncodeToString(b)
	keyHash = hashKey(plain)
	return plain, keyHash, nil
}

func hashKey(plain string) string {
	sum := sha256.Sum256([]byte(plain))
	return hex.EncodeToString(sum[:])
}
