package approval

import (
	"context"
	"errors"
	"fmt"
	"path"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

// DelegationRule lets a specific session account decide certain approvals on
// the user's behalf (basis=desk_delegated). Only a person creates or revokes
// one, and the server ceiling (Delegable) applies no matter what a rule says.
type DelegationRule struct {
	ID          uuid.UUID      `json:"id"`
	WorkspaceID uuid.UUID      `json:"workspace_id"`
	Principal   string         `json:"principal"`
	Kinds       []Kind         `json:"kinds"`
	Repos       []string       `json:"repos,omitempty"`
	Conditions  map[string]any `json:"conditions,omitempty"`
	UserRef     string         `json:"user_ref"`
	CreatedBy   string         `json:"created_by"`
	CreatedAt   time.Time      `json:"created_at"`
	ExpiresAt   *time.Time     `json:"expires_at,omitempty"`
	RevokedAt   *time.Time     `json:"revoked_at,omitempty"`
}

var (
	ErrRuleNotFound = errors.New("delegation rule not found")
	ErrInvalidRule  = errors.New("invalid delegation rule")
)

// ValidatePrincipal rejects principals that name a person. A rule may only
// name a session account, whose machine-token identity is a bare actor id
// (never an email); naming a person would let a session holding that person's
// token act on the rule.
func ValidatePrincipal(p string) error {
	if strings.TrimSpace(p) == "" || strings.ContainsAny(p, "@ \t\n") {
		return fmt.Errorf("%w: principal must be a session account id, not an email or blank", ErrInvalidRule)
	}
	return nil
}

// Validate checks a rule before it is stored: the principal, and that every
// kind is under the fixed delegation ceiling.
func (r *DelegationRule) Validate() error {
	if err := ValidatePrincipal(r.Principal); err != nil {
		return err
	}
	if len(r.Kinds) == 0 {
		return fmt.Errorf("%w: at least one kind is required", ErrInvalidRule)
	}
	for _, k := range r.Kinds {
		if k != KindMerge && k != KindDeviation {
			return fmt.Errorf("%w: kind %q is never delegable", ErrInvalidRule, k)
		}
	}
	return nil
}

// RuleStore reads (and, for P4, writes) delegation rules.
type RuleStore interface {
	Create(ctx context.Context, r *DelegationRule) error
	// ListByPrincipal returns every rule for the principal in the workspace,
	// including expired and revoked ones; callers filter with Active.
	ListByPrincipal(ctx context.Context, workspaceID uuid.UUID, principal string) ([]*DelegationRule, error)
	Revoke(ctx context.Context, workspaceID, id uuid.UUID) error
}

// Active reports whether the rule can be used at now.
func (r *DelegationRule) Active(now time.Time) bool {
	if r.RevokedAt != nil && !r.RevokedAt.After(now) {
		return false
	}
	if r.ExpiresAt != nil && !r.ExpiresAt.After(now) {
		return false
	}
	return true
}

// conditionsMet checks rule conditions against the approval. Unknown condition
// keys fail closed: a rule must not become broader than the server understands.
func (r *DelegationRule) conditionsMet(a *Approval) bool {
	for k, v := range r.Conditions {
		switch k {
		case "require_evidence_types":
			want, ok := toStrings(v)
			if !ok {
				return false
			}
			have := map[string]bool{}
			for _, e := range a.Evidence {
				have[e.Type] = true
			}
			for _, w := range want {
				if !have[w] {
					return false
				}
			}
		default:
			return false
		}
	}
	return true
}

func toStrings(v any) ([]string, bool) {
	switch s := v.(type) {
	case []string:
		return s, true
	case []any:
		out := make([]string, 0, len(s))
		for _, x := range s {
			str, ok := x.(string)
			if !ok {
				return nil, false
			}
			out = append(out, str)
		}
		return out, true
	}
	return nil, false
}

func (r *DelegationRule) matches(a *Approval) bool {
	kindOK := false
	for _, k := range r.Kinds {
		if k == a.Kind {
			kindOK = true
		}
	}
	if !kindOK {
		return false
	}
	if len(r.Repos) > 0 {
		ok := false
		for _, pat := range r.Repos {
			if m, err := path.Match(pat, a.Subject.Repo); err == nil && m {
				ok = true
			}
		}
		if !ok {
			return false
		}
	}
	return r.conditionsMet(a)
}

// MemRuleStore is an in-memory RuleStore for tests.
type MemRuleStore struct {
	mu    sync.Mutex
	rules []*DelegationRule
}

func NewMemRuleStore() *MemRuleStore { return &MemRuleStore{} }

func (m *MemRuleStore) Create(_ context.Context, r *DelegationRule) error {
	if err := r.Validate(); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if r.ID == uuid.Nil {
		r.ID = uuid.New()
	}
	if r.CreatedAt.IsZero() {
		r.CreatedAt = time.Now().UTC()
	}
	c := *r
	m.rules = append(m.rules, &c)
	return nil
}

func (m *MemRuleStore) ListByPrincipal(_ context.Context, ws uuid.UUID, principal string) ([]*DelegationRule, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []*DelegationRule
	for _, r := range m.rules {
		if r.WorkspaceID == ws && r.Principal == principal {
			c := *r
			out = append(out, &c)
		}
	}
	return out, nil
}

func (m *MemRuleStore) Revoke(_ context.Context, ws, id uuid.UUID) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, r := range m.rules {
		if r.ID == id && r.WorkspaceID == ws {
			now := time.Now().UTC()
			r.RevokedAt = &now
			return nil
		}
	}
	return ErrRuleNotFound
}
