package apikey

import (
	"time"

	"github.com/google/uuid"
)

// APIKey is the persisted record (key_hash is never returned via API).
type APIKey struct {
	ID          uuid.UUID  `json:"id"`
	ProjectID   uuid.UUID  `json:"project_id"`
	Name        string     `json:"name"`
	CreatedBy   string     `json:"created_by"`
	ExpiresAt   *time.Time `json:"expires_at,omitempty"`
	LastUsedAt  *time.Time `json:"last_used_at,omitempty"`
	CreatedAt   time.Time  `json:"created_at"`
}

// CreateResult is returned once on key creation; PlainKey is never stored.
type CreateResult struct {
	APIKey
	PlainKey string `json:"key"`
}

type CreateReq struct {
	Name      string     `json:"name"`
	ExpiresAt *time.Time `json:"expires_at,omitempty"`
}
