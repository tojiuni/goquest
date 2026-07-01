package vcs

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type pgStore struct{ db *pgxpool.Pool }

func NewPGStore(db *pgxpool.Pool) Store { return &pgStore{db: db} }

type rowScanner interface{ Scan(dest ...any) error }

func scanRepo(row rowScanner) (*VcsRepo, error) {
	var r VcsRepo
	if err := row.Scan(&r.ID, &r.Provider, &r.RepoFullName, &r.ProjectID,
		&r.AutoTransition, &r.MergeTarget, &r.Active, &r.CreatedAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &r, nil
}

const repoCols = `id, provider, repo_full_name, project_id, auto_transition, merge_target, active, created_at`

func (s *pgStore) CreateRepo(ctx context.Context, r *VcsRepo) error {
	if r.ID == uuid.Nil {
		r.ID = uuid.New()
	}
	_, err := s.db.Exec(ctx, `
		INSERT INTO vcs_repos (id, provider, repo_full_name, project_id, auto_transition, merge_target, active)
		VALUES ($1,$2,$3,$4,$5,$6,$7)
		ON CONFLICT (provider, repo_full_name) DO UPDATE SET
			project_id = EXCLUDED.project_id,
			auto_transition = EXCLUDED.auto_transition,
			merge_target = EXCLUDED.merge_target,
			active = EXCLUDED.active`,
		r.ID, r.Provider, r.RepoFullName, r.ProjectID, r.AutoTransition, r.MergeTarget, r.Active)
	return err
}

func (s *pgStore) GetRepo(ctx context.Context, provider, repoFullName string) (*VcsRepo, error) {
	return scanRepo(s.db.QueryRow(ctx,
		`SELECT `+repoCols+` FROM vcs_repos WHERE provider=$1 AND repo_full_name=$2 AND active`, provider, repoFullName))
}

func (s *pgStore) ListRepos(ctx context.Context) ([]*VcsRepo, error) {
	rows, err := s.db.Query(ctx, `SELECT `+repoCols+` FROM vcs_repos ORDER BY created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*VcsRepo
	for rows.Next() {
		r, err := scanRepo(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

const linkCols = `id, ticket_id, provider, repo_full_name, kind, external_ref, url, state, title, actor, created_at, updated_at`

func scanLink(row rowScanner) (*VcsLink, error) {
	var l VcsLink
	if err := row.Scan(&l.ID, &l.TicketID, &l.Provider, &l.RepoFullName, &l.Kind, &l.ExternalRef,
		&l.URL, &l.State, &l.Title, &l.Actor, &l.CreatedAt, &l.UpdatedAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &l, nil
}

func (s *pgStore) UpsertLink(ctx context.Context, l *VcsLink) (*VcsLink, error) {
	if l.ID == uuid.Nil {
		l.ID = uuid.New()
	}
	return scanLink(s.db.QueryRow(ctx, `
		INSERT INTO vcs_links (id, ticket_id, provider, repo_full_name, kind, external_ref, url, state, title, actor)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)
		ON CONFLICT (ticket_id, provider, repo_full_name, kind, external_ref) DO UPDATE SET
			url = EXCLUDED.url, state = EXCLUDED.state, title = EXCLUDED.title,
			actor = EXCLUDED.actor, updated_at = now()
		RETURNING `+linkCols,
		l.ID, l.TicketID, l.Provider, l.RepoFullName, l.Kind, l.ExternalRef, l.URL, l.State, l.Title, l.Actor))
}

func (s *pgStore) GetBranchLink(ctx context.Context, provider, repoFullName, branch string) (*VcsLink, error) {
	return scanLink(s.db.QueryRow(ctx,
		`SELECT `+linkCols+` FROM vcs_links WHERE provider=$1 AND repo_full_name=$2 AND kind='branch' AND external_ref=$3 LIMIT 1`,
		provider, repoFullName, branch))
}

func (s *pgStore) LinksForTicket(ctx context.Context, ticketID uuid.UUID) ([]VcsLink, error) {
	rows, err := s.db.Query(ctx, `SELECT `+linkCols+` FROM vcs_links WHERE ticket_id=$1 ORDER BY created_at`, ticketID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []VcsLink
	for rows.Next() {
		l, err := scanLink(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *l)
	}
	return out, rows.Err()
}
