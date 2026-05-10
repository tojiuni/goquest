package magic

import (
	"errors"
	"time"

	"github.com/google/uuid"
)

type Token struct {
	ID        uuid.UUID  `json:"id"`
	Email     string     `json:"email"`
	TicketID  *uuid.UUID `json:"ticket_id,omitempty"`
	ExpiresAt time.Time  `json:"expires_at"`
	UsedAt    *time.Time `json:"used_at,omitempty"`
	CreatedAt time.Time  `json:"created_at"`
}

// ExchangeResult is what the /auth/magic-link/exchange endpoint returns —
// a short-lived bearer token the SPA stores, then sends as
// Authorization: Bearer ct_<token>
type ExchangeResult struct {
	AccessToken string    `json:"access_token"`
	TokenType   string    `json:"token_type"` // "Bearer"
	ExpiresIn   int       `json:"expires_in"` // seconds
	Email       string    `json:"email"`
	TicketID    *uuid.UUID `json:"ticket_id,omitempty"`
}

var (
	ErrNotFound = errors.New("magic token: not found")
	ErrExpired  = errors.New("magic token: expired")
	ErrUsed     = errors.New("magic token: already used")
)
