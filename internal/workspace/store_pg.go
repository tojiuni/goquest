package workspace

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

type pgStore struct {
	db *pgxpool.Pool
}

func NewPGStore(db *pgxpool.Pool) Store {
	return &pgStore{db: db}
}

// ── Workspace ─────────────────────────────────────────────────────

func (s *pgStore) Create(ctx context.Context, ws *Workspace) error {
	_, err := s.db.Exec(ctx, `
		INSERT INTO workspaces (id, name, slug, description, type, owner_id, org_id, created_at, updated_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)`,
		ws.ID, ws.Name, ws.Slug, ws.Description, string(ws.Type), ws.OwnerID, ws.OrgID,
		ws.CreatedAt, ws.UpdatedAt,
	)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" && pgErr.ConstraintName == "workspaces_lower_name_key" {
			return ErrDuplicateName
		}
		return fmt.Errorf("create workspace: %w", err)
	}
	return nil
}

func (s *pgStore) Get(ctx context.Context, id uuid.UUID) (*Workspace, error) {
	row := s.db.QueryRow(ctx, `
		SELECT id, name, slug, description, type, owner_id, org_id, created_at, updated_at
		FROM workspaces WHERE id = $1`, id)
	return scanWorkspace(row)
}

func (s *pgStore) GetBySlug(ctx context.Context, slug string) (*Workspace, error) {
	row := s.db.QueryRow(ctx, `
		SELECT id, name, slug, description, type, owner_id, org_id, created_at, updated_at
		FROM workspaces WHERE slug = $1`, slug)
	return scanWorkspace(row)
}

func (s *pgStore) GetByName(ctx context.Context, name string) (*Workspace, error) {
	row := s.db.QueryRow(ctx, `
		SELECT id, name, slug, description, type, owner_id, org_id, created_at, updated_at
		FROM workspaces WHERE LOWER(name) = LOWER($1)`, name)
	return scanWorkspace(row)
}

func (s *pgStore) Update(ctx context.Context, id uuid.UUID, req UpdateReq) (*Workspace, error) {
	now := time.Now().UTC()
	_, err := s.db.Exec(ctx, `
		UPDATE workspaces SET
			name        = COALESCE($2, name),
			description = COALESCE($3, description),
			updated_at  = $4
		WHERE id = $1`,
		id, req.Name, req.Description, now,
	)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return nil, ErrDuplicateName
		}
		return nil, fmt.Errorf("update workspace: %w", err)
	}
	return s.Get(ctx, id)
}

func (s *pgStore) Delete(ctx context.Context, id uuid.UUID) error {
	_, err := s.db.Exec(ctx, "DELETE FROM workspaces WHERE id = $1", id)
	if err != nil {
		return fmt.Errorf("delete workspace: %w", err)
	}
	return nil
}

func (s *pgStore) ListByUser(ctx context.Context, userID string) ([]*Workspace, error) {
	rows, err := s.db.Query(ctx, `
		SELECT w.id, w.name, w.slug, w.description, w.type, w.owner_id, w.org_id, w.created_at, w.updated_at
		FROM workspaces w
		JOIN workspace_members wm ON wm.workspace_id = w.id
		WHERE wm.user_id = $1
		ORDER BY w.created_at DESC`, userID)
	if err != nil {
		return nil, fmt.Errorf("list workspaces: %w", err)
	}
	defer rows.Close()

	var list []*Workspace
	for rows.Next() {
		ws, err := scanWorkspace(rows)
		if err != nil {
			return nil, err
		}
		list = append(list, ws)
	}
	return list, rows.Err()
}

func (s *pgStore) SetOrg(ctx context.Context, id uuid.UUID, orgID *string) error {
	_, err := s.db.Exec(ctx, `UPDATE workspaces SET org_id=$2, updated_at=now() WHERE id=$1`, id, orgID)
	if err != nil {
		return fmt.Errorf("set workspace org: %w", err)
	}
	return nil
}

// ── Member ────────────────────────────────────────────────────────

func (s *pgStore) AddMember(ctx context.Context, m *Member) error {
	_, err := s.db.Exec(ctx, `
		INSERT INTO workspace_members (workspace_id, user_id, role, xp, level, title, joined_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7)
		ON CONFLICT (workspace_id, user_id) DO NOTHING`,
		m.WorkspaceID, m.UserID, string(m.Role), m.XP, m.Level, m.Title, m.JoinedAt,
	)
	if err != nil {
		return fmt.Errorf("add member: %w", err)
	}
	return nil
}

func (s *pgStore) GetMember(ctx context.Context, workspaceID uuid.UUID, userID string) (*Member, error) {
	row := s.db.QueryRow(ctx, `
		SELECT workspace_id, user_id, role, xp, level, title, joined_at
		FROM workspace_members WHERE workspace_id=$1 AND user_id=$2`,
		workspaceID, userID)
	return scanMember(row)
}

func (s *pgStore) ListMembers(ctx context.Context, workspaceID uuid.UUID) ([]*Member, error) {
	rows, err := s.db.Query(ctx, `
		SELECT workspace_id, user_id, role, xp, level, title, joined_at
		FROM workspace_members WHERE workspace_id=$1 ORDER BY joined_at ASC`,
		workspaceID)
	if err != nil {
		return nil, fmt.Errorf("list members: %w", err)
	}
	defer rows.Close()

	var list []*Member
	for rows.Next() {
		m, err := scanMember(rows)
		if err != nil {
			return nil, err
		}
		list = append(list, m)
	}
	return list, rows.Err()
}

func (s *pgStore) SetMemberRole(ctx context.Context, workspaceID uuid.UUID, userID string, role Role) error {
	_, err := s.db.Exec(ctx,
		"UPDATE workspace_members SET role=$3 WHERE workspace_id=$1 AND user_id=$2",
		workspaceID, userID, string(role))
	if err != nil {
		return fmt.Errorf("set member role: %w", err)
	}
	return nil
}

func (s *pgStore) RemoveMember(ctx context.Context, workspaceID uuid.UUID, userID string) error {
	_, err := s.db.Exec(ctx,
		"DELETE FROM workspace_members WHERE workspace_id=$1 AND user_id=$2",
		workspaceID, userID)
	if err != nil {
		return fmt.Errorf("remove member: %w", err)
	}
	return nil
}

func (s *pgStore) UpdateMemberXP(ctx context.Context, workspaceID uuid.UUID, userID string, xp, level int, title string) error {
	_, err := s.db.Exec(ctx,
		"UPDATE workspace_members SET xp=$3, level=$4, title=$5 WHERE workspace_id=$1 AND user_id=$2",
		workspaceID, userID, xp, level, title)
	if err != nil {
		return fmt.Errorf("update member xp: %w", err)
	}
	return nil
}

// ── Invitation ────────────────────────────────────────────────────

func (s *pgStore) CreateInvitation(ctx context.Context, inv *Invitation) error {
	var email *string
	if inv.Email != "" {
		email = &inv.Email
	}
	_, err := s.db.Exec(ctx, `
		INSERT INTO workspace_invitations
			(id, workspace_id, email, token, role, invited_by, expires_at, created_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8)`,
		inv.ID, inv.WorkspaceID, email, inv.Token, string(inv.Role),
		inv.InvitedBy, inv.ExpiresAt, inv.CreatedAt,
	)
	if err != nil {
		return fmt.Errorf("create invitation: %w", err)
	}
	return nil
}

func (s *pgStore) GetInvitationByToken(ctx context.Context, token string) (*Invitation, error) {
	row := s.db.QueryRow(ctx, `
		SELECT id, workspace_id, email, token, role, invited_by, expires_at, used_at, created_at
		FROM workspace_invitations WHERE token = $1`, token)
	return scanInvitation(row)
}

func (s *pgStore) ListInvitations(ctx context.Context, workspaceID uuid.UUID) ([]*Invitation, error) {
	rows, err := s.db.Query(ctx, `
		SELECT id, workspace_id, email, token, role, invited_by, expires_at, used_at, created_at
		FROM workspace_invitations WHERE workspace_id=$1 ORDER BY created_at DESC`,
		workspaceID)
	if err != nil {
		return nil, fmt.Errorf("list invitations: %w", err)
	}
	defer rows.Close()

	var list []*Invitation
	for rows.Next() {
		inv, err := scanInvitation(rows)
		if err != nil {
			return nil, err
		}
		list = append(list, inv)
	}
	return list, rows.Err()
}

func (s *pgStore) MarkInvitationUsed(ctx context.Context, id uuid.UUID) error {
	now := time.Now().UTC()
	_, err := s.db.Exec(ctx,
		"UPDATE workspace_invitations SET used_at=$2 WHERE id=$1", id, now)
	if err != nil {
		return fmt.Errorf("mark invitation used: %w", err)
	}
	return nil
}

func (s *pgStore) DeleteInvitation(ctx context.Context, id uuid.UUID) error {
	_, err := s.db.Exec(ctx, "DELETE FROM workspace_invitations WHERE id=$1", id)
	if err != nil {
		return fmt.Errorf("delete invitation: %w", err)
	}
	return nil
}

// ── Scanners ──────────────────────────────────────────────────────

type rowScanner interface {
	Scan(dest ...any) error
}

func scanWorkspace(s rowScanner) (*Workspace, error) {
	ws := &Workspace{}
	err := s.Scan(
		&ws.ID, &ws.Name, &ws.Slug, &ws.Description,
		&ws.Type, &ws.OwnerID, &ws.OrgID, &ws.CreatedAt, &ws.UpdatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("scan workspace: %w", err)
	}
	return ws, nil
}

func scanMember(s rowScanner) (*Member, error) {
	m := &Member{}
	err := s.Scan(
		&m.WorkspaceID, &m.UserID, &m.Role,
		&m.XP, &m.Level, &m.Title, &m.JoinedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("scan member: %w", err)
	}
	return m, nil
}

func scanInvitation(s rowScanner) (*Invitation, error) {
	inv := &Invitation{}
	var email *string
	err := s.Scan(
		&inv.ID, &inv.WorkspaceID, &email, &inv.Token, &inv.Role,
		&inv.InvitedBy, &inv.ExpiresAt, &inv.UsedAt, &inv.CreatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("scan invitation: %w", err)
	}
	if email != nil {
		inv.Email = *email
	}
	return inv, nil
}

var ErrNotFound = errors.New("workspace: not found")

var ErrDuplicateName = errors.New("workspace: name already in use")

// ErrInvalidMember marks a bad AddMember request (blank user_id, unknown role).
var ErrInvalidMember = errors.New("workspace: invalid member")

// ErrOwnerRequired is returned when a non-owner attempts an owner-only member operation.
var ErrOwnerRequired = errors.New("workspace: owner role required")
