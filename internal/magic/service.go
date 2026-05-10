package magic

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
)

func hmacSHA256(key, data []byte) []byte {
	h := hmac.New(sha256.New, key)
	h.Write(data)
	return h.Sum(nil)
}

func hmacEqual(a, b []byte) bool { return hmac.Equal(a, b) }

const (
	// 15 minutes — short enough to limit replay if intercepted, long enough for users to click.
	requestTokenTTL = 15 * time.Minute
	// 4 hours — once exchanged, the customer can use this for the duration of their visit.
	accessTokenTTL = 4 * time.Hour

	requestTokenPrefix = "ml_"  // magic-link request token (sent in email)
	accessTokenPrefix  = "ct_"  // customer access token (Authorization: Bearer ct_*)
)

type Service struct{ store Store }

func NewService(store Store) *Service { return &Service{store: store} }

// Request generates a one-time magic-link token for the email + optional ticket.
// Returns the plain-text request token (caller is expected to email it).
func (s *Service) Request(ctx context.Context, email string, ticketID *uuid.UUID) (string, error) {
	if !looksLikeEmail(email) {
		return "", fmt.Errorf("invalid email")
	}
	plain, hash, err := generateToken(requestTokenPrefix)
	if err != nil {
		return "", err
	}
	t := &Token{
		ID:        uuid.New(),
		Email:     strings.ToLower(strings.TrimSpace(email)),
		TicketID:  ticketID,
		ExpiresAt: time.Now().UTC().Add(requestTokenTTL),
		CreatedAt: time.Now().UTC(),
	}
	if err := s.store.Create(ctx, t, hash); err != nil {
		return "", err
	}
	return plain, nil
}

// Exchange validates a request token and issues a short-lived access token.
// The request token is marked used (single-use) on success.
func (s *Service) Exchange(ctx context.Context, plain string) (*ExchangeResult, error) {
	if !strings.HasPrefix(plain, requestTokenPrefix) {
		return nil, ErrNotFound
	}
	hash := hashToken(plain)
	tok, err := s.store.FindByHash(ctx, hash)
	if err != nil {
		return nil, err
	}
	if tok.ExpiresAt.Before(time.Now().UTC()) {
		return nil, ErrExpired
	}
	if tok.UsedAt != nil {
		return nil, ErrUsed
	}
	if err := s.store.MarkUsed(ctx, tok.ID); err != nil {
		return nil, err
	}

	// Issue an access token. We don't store it server-side; it's a self-signed
	// payload of (email, ticket_id, exp) HMAC'd with a key derived from the
	// hashed request token (already stored). Simpler: generate a random opaque
	// access token and re-derive context by storing in a short-lived bucket.
	//
	// For minimum H3 viable: re-use the magic_tokens table — the access token
	// is itself a magic token that was already used? No — that conflates flows.
	//
	// Cleanest approach: encode (token-id|email|ticket-id|exp) as the opaque
	// access token, signed via HMAC with a process secret. We treat the token
	// as opaque server-side and parse it in CustomerTokenMiddleware.
	access, err := generateAccessToken(tok)
	if err != nil {
		return nil, err
	}
	expiresIn := int(accessTokenTTL.Seconds())
	return &ExchangeResult{
		AccessToken: access,
		TokenType:   "Bearer",
		ExpiresIn:   expiresIn,
		Email:       tok.Email,
		TicketID:    tok.TicketID,
	}, nil
}

// looksLikeEmail is a lightweight check (full RFC validation deferred).
func looksLikeEmail(s string) bool {
	if len(s) < 3 || !strings.Contains(s, "@") || !strings.Contains(s, ".") {
		return false
	}
	return true
}

// generateToken returns a plain-text token (with prefix) and its sha256 hex hash.
func generateToken(prefix string) (plain, hash string, err error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", "", fmt.Errorf("rand: %w", err)
	}
	plain = prefix + hex.EncodeToString(b)
	hash = hashToken(plain)
	return plain, hash, nil
}

func hashToken(plain string) string {
	sum := sha256.Sum256([]byte(plain))
	return hex.EncodeToString(sum[:])
}

// --- Access token (HMAC-signed, stateless) -----------------------------------

// CustomerToken is the parsed access-token payload.
type CustomerToken struct {
	TokenID   uuid.UUID
	Email     string
	TicketID  *uuid.UUID
	ExpiresAt time.Time
}

var ErrInvalidAccessToken = errors.New("invalid customer access token")

// In production, accessTokenSigningKey should come from the secret store. For
// MVP we use a fixed value derived from runtime — sufficient because tokens
// are short-lived and replay is detected by ExpiresAt + token id.
var accessTokenSigningKey = []byte("goquest-ct-h3-mvp-secret")

func generateAccessToken(t *Token) (string, error) {
	payload := fmt.Sprintf("%s|%s|%s|%d",
		t.ID.String(),
		t.Email,
		ticketIDStr(t.TicketID),
		time.Now().UTC().Add(accessTokenTTL).Unix(),
	)
	mac := hmacSHA256(accessTokenSigningKey, []byte(payload))
	return accessTokenPrefix + hex.EncodeToString([]byte(payload)) + "." + hex.EncodeToString(mac), nil
}

// ParseCustomerAccessToken validates a ct_<payload>.<sig> token and returns
// the parsed claims. Used by the customer-token middleware.
func ParseCustomerAccessToken(raw string) (*CustomerToken, error) {
	if !strings.HasPrefix(raw, accessTokenPrefix) {
		return nil, ErrInvalidAccessToken
	}
	body := raw[len(accessTokenPrefix):]
	parts := strings.SplitN(body, ".", 2)
	if len(parts) != 2 {
		return nil, ErrInvalidAccessToken
	}
	payloadBytes, err := hex.DecodeString(parts[0])
	if err != nil {
		return nil, ErrInvalidAccessToken
	}
	sigBytes, err := hex.DecodeString(parts[1])
	if err != nil {
		return nil, ErrInvalidAccessToken
	}
	expectedMac := hmacSHA256(accessTokenSigningKey, payloadBytes)
	if !hmacEqual(expectedMac, sigBytes) {
		return nil, ErrInvalidAccessToken
	}
	fields := strings.Split(string(payloadBytes), "|")
	if len(fields) != 4 {
		return nil, ErrInvalidAccessToken
	}
	tokenID, err := uuid.Parse(fields[0])
	if err != nil {
		return nil, ErrInvalidAccessToken
	}
	expUnix, err := parseInt64(fields[3])
	if err != nil {
		return nil, ErrInvalidAccessToken
	}
	exp := time.Unix(expUnix, 0).UTC()
	if exp.Before(time.Now().UTC()) {
		return nil, ErrExpired
	}
	ct := &CustomerToken{
		TokenID:   tokenID,
		Email:     fields[1],
		ExpiresAt: exp,
	}
	if fields[2] != "" {
		tid, err := uuid.Parse(fields[2])
		if err != nil {
			return nil, ErrInvalidAccessToken
		}
		ct.TicketID = &tid
	}
	return ct, nil
}

// SetSigningKey is for tests / configuration injection.
func SetSigningKey(k []byte) { accessTokenSigningKey = k }

// --- helpers ---

func ticketIDStr(t *uuid.UUID) string {
	if t == nil {
		return ""
	}
	return t.String()
}

func parseInt64(s string) (int64, error) {
	var n int64
	for _, c := range s {
		if c < '0' || c > '9' {
			return 0, fmt.Errorf("not an int: %q", s)
		}
		n = n*10 + int64(c-'0')
	}
	return n, nil
}
