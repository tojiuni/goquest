package approval

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

type pgRuleStore struct{ db *pgxpool.Pool }

func NewPGRuleStore(db *pgxpool.Pool) RuleStore { return &pgRuleStore{db: db} }

func (s *pgRuleStore) Create(ctx context.Context, r *DelegationRule) error {
	kinds := make([]string, len(r.Kinds))
	for i, k := range r.Kinds {
		kinds[i] = string(k)
	}
	repos := r.Repos
	if repos == nil {
		repos = []string{}
	}
	cond := r.Conditions
	if cond == nil {
		cond = map[string]any{}
	}
	cj, _ := json.Marshal(cond)
	err := s.db.QueryRow(ctx, `
		INSERT INTO delegation_rules (workspace_id, principal, kinds, repos, conditions, user_ref, created_by, expires_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8)
		RETURNING id, created_at`,
		r.WorkspaceID, r.Principal, kinds, repos, cj, r.UserRef, r.CreatedBy, r.ExpiresAt,
	).Scan(&r.ID, &r.CreatedAt)
	if err != nil {
		return fmt.Errorf("create delegation rule: %w", err)
	}
	return nil
}

func (s *pgRuleStore) ListByPrincipal(ctx context.Context, ws uuid.UUID, principal string) ([]*DelegationRule, error) {
	rows, err := s.db.Query(ctx, `
		SELECT id, workspace_id, principal, kinds, repos, conditions, user_ref, created_by, created_at, expires_at, revoked_at
		FROM delegation_rules WHERE workspace_id=$1 AND principal=$2 ORDER BY created_at, id`, ws, principal)
	if err != nil {
		return nil, fmt.Errorf("list delegation rules: %w", err)
	}
	defer rows.Close()
	var out []*DelegationRule
	for rows.Next() {
		var r DelegationRule
		var kinds []string
		var cond []byte
		if err := rows.Scan(&r.ID, &r.WorkspaceID, &r.Principal, &kinds, &r.Repos, &cond, &r.UserRef,
			&r.CreatedBy, &r.CreatedAt, &r.ExpiresAt, &r.RevokedAt); err != nil {
			return nil, err
		}
		for _, k := range kinds {
			r.Kinds = append(r.Kinds, Kind(k))
		}
		if err := json.Unmarshal(cond, &r.Conditions); err != nil {
			return nil, fmt.Errorf("decode rule conditions: %w", err)
		}
		out = append(out, &r)
	}
	return out, rows.Err()
}

func (s *pgRuleStore) Revoke(ctx context.Context, ws, id uuid.UUID) error {
	ct, err := s.db.Exec(ctx, `UPDATE delegation_rules SET revoked_at=NOW() WHERE id=$1 AND workspace_id=$2 AND revoked_at IS NULL`, id, ws)
	if err != nil {
		return fmt.Errorf("revoke delegation rule: %w", err)
	}
	if ct.RowsAffected() == 0 {
		return ErrRuleNotFound
	}
	return nil
}
