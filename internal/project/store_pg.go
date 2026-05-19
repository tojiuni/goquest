package project

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type pgStore struct {
	db *pgxpool.Pool
}

func NewPGStore(db *pgxpool.Pool) Store {
	return &pgStore{db: db}
}

func (s *pgStore) Create(ctx context.Context, p *Project) error {
	var difficulty *string
	if p.Difficulty != "" {
		d := string(p.Difficulty)
		difficulty = &d
	}
	_, err := s.db.Exec(ctx, `
		INSERT INTO projects
			(id, workspace_id, name, description, status, difficulty, reward_xp, created_by, created_at, updated_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)`,
		p.ID, p.WorkspaceID, p.Name, p.Description,
		string(p.Status), difficulty, p.RewardXP, p.CreatedBy,
		p.CreatedAt, p.UpdatedAt,
	)
	if err != nil {
		return fmt.Errorf("create project: %w", err)
	}
	return nil
}

func (s *pgStore) Get(ctx context.Context, id uuid.UUID) (*Project, error) {
	row := s.db.QueryRow(ctx, `
		SELECT id, workspace_id, name, description, status, difficulty, reward_xp,
		       created_by, created_at, updated_at
		FROM projects WHERE id=$1`, id)
	return scanProject(row)
}

func (s *pgStore) GetByName(ctx context.Context, workspaceName, projectName string) (*Project, error) {
	row := s.db.QueryRow(ctx, `
		SELECT p.id, p.workspace_id, p.name, p.description, p.status, p.difficulty, p.reward_xp,
		       p.created_by, p.created_at, p.updated_at
		FROM projects p
		JOIN workspaces w ON w.id = p.workspace_id
		WHERE LOWER(w.name) = LOWER($1) AND LOWER(p.name) = LOWER($2)
		LIMIT 1`, workspaceName, projectName)
	return scanProject(row)
}

func (s *pgStore) Update(ctx context.Context, id uuid.UUID, req UpdateReq) (*Project, error) {
	now := time.Now().UTC()
	var diff *string
	if req.Difficulty != nil {
		d := string(*req.Difficulty)
		diff = &d
	}
	var status *string
	if req.Status != nil {
		s2 := string(*req.Status)
		status = &s2
	}
	_, err := s.db.Exec(ctx, `
		UPDATE projects SET
			name        = COALESCE($2, name),
			description = COALESCE($3, description),
			status      = COALESCE($4, status),
			difficulty  = COALESCE($5, difficulty),
			reward_xp   = COALESCE($6, reward_xp),
			updated_at  = $7
		WHERE id=$1`,
		id, req.Name, req.Description, status, diff, req.RewardXP, now,
	)
	if err != nil {
		return nil, fmt.Errorf("update project: %w", err)
	}
	return s.Get(ctx, id)
}

func (s *pgStore) Delete(ctx context.Context, id uuid.UUID) error {
	_, err := s.db.Exec(ctx, "DELETE FROM projects WHERE id=$1", id)
	return err
}

func (s *pgStore) ListByWorkspace(ctx context.Context, workspaceID uuid.UUID) ([]*Project, error) {
	rows, err := s.db.Query(ctx, `
		SELECT id, workspace_id, name, description, status, difficulty, reward_xp,
		       created_by, created_at, updated_at
		FROM projects WHERE workspace_id=$1 ORDER BY created_at DESC`,
		workspaceID)
	if err != nil {
		return nil, fmt.Errorf("list projects: %w", err)
	}
	defer rows.Close()

	var list []*Project
	for rows.Next() {
		p, err := scanProject(rows)
		if err != nil {
			return nil, err
		}
		list = append(list, p)
	}
	return list, rows.Err()
}

// ── Member ─────────────────────────────────────────────────────────

func (s *pgStore) AddMember(ctx context.Context, m *Member) error {
	_, err := s.db.Exec(ctx, `
		INSERT INTO project_members (project_id, user_id, role, assigned_at)
		VALUES ($1,$2,$3,$4)
		ON CONFLICT (project_id, user_id) DO UPDATE SET role=EXCLUDED.role`,
		m.ProjectID, m.UserID, string(m.Role), m.AssignedAt,
	)
	if err != nil {
		return fmt.Errorf("add project member: %w", err)
	}
	return nil
}

func (s *pgStore) GetMember(ctx context.Context, projectID uuid.UUID, userID string) (*Member, error) {
	row := s.db.QueryRow(ctx, `
		SELECT project_id, user_id, role, assigned_at
		FROM project_members WHERE project_id=$1 AND user_id=$2`,
		projectID, userID)

	m := &Member{}
	err := row.Scan(&m.ProjectID, &m.UserID, &m.Role, &m.AssignedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("get project member: %w", err)
	}
	return m, nil
}

func (s *pgStore) ListMembers(ctx context.Context, projectID uuid.UUID) ([]*Member, error) {
	rows, err := s.db.Query(ctx, `
		SELECT project_id, user_id, role, assigned_at
		FROM project_members WHERE project_id=$1 ORDER BY assigned_at ASC`,
		projectID)
	if err != nil {
		return nil, fmt.Errorf("list project members: %w", err)
	}
	defer rows.Close()

	var list []*Member
	for rows.Next() {
		m := &Member{}
		if err := rows.Scan(&m.ProjectID, &m.UserID, &m.Role, &m.AssignedAt); err != nil {
			return nil, err
		}
		list = append(list, m)
	}
	return list, rows.Err()
}

func (s *pgStore) SetMemberRole(ctx context.Context, projectID uuid.UUID, userID string, role Role) error {
	_, err := s.db.Exec(ctx,
		"UPDATE project_members SET role=$3 WHERE project_id=$1 AND user_id=$2",
		projectID, userID, string(role))
	return err
}

func (s *pgStore) RemoveMember(ctx context.Context, projectID uuid.UUID, userID string) error {
	_, err := s.db.Exec(ctx,
		"DELETE FROM project_members WHERE project_id=$1 AND user_id=$2",
		projectID, userID)
	return err
}

// ── Scanner ────────────────────────────────────────────────────────

type rowScanner interface {
	Scan(dest ...any) error
}

func scanProject(s rowScanner) (*Project, error) {
	p := &Project{}
	var difficulty *string
	err := s.Scan(
		&p.ID, &p.WorkspaceID, &p.Name, &p.Description,
		&p.Status, &difficulty, &p.RewardXP,
		&p.CreatedBy, &p.CreatedAt, &p.UpdatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("scan project: %w", err)
	}
	if difficulty != nil {
		p.Difficulty = Difficulty(*difficulty)
	}
	return p, nil
}

var ErrNotFound = errors.New("project: not found")
