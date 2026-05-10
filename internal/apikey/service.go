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

	plain, keyHash, err := generateKey()
	if err != nil {
		return nil, fmt.Errorf("generate key: %w", err)
	}

	k := &APIKey{
		ID:        uuid.New(),
		ProjectID: projectID,
		Name:      req.Name,
		CreatedBy: createdBy,
		ExpiresAt: req.ExpiresAt,
		CreatedAt: time.Now().UTC(),
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
