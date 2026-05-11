package device

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type pgStore struct{ db *pgxpool.Pool }

func NewPGStore(db *pgxpool.Pool) Store { return &pgStore{db: db} }

func (s *pgStore) Upsert(ctx context.Context, userID string, req RegisterReq) (*Token, error) {
	now := time.Now().UTC()
	// UNIQUE (user_id, apns_token) lets us refresh last_seen_at + app_version without dup rows.
	row := s.db.QueryRow(ctx, `
		INSERT INTO device_tokens (user_id, platform, apns_token, app_version, created_at, last_seen_at)
		VALUES ($1,$2,$3,$4,$5,$5)
		ON CONFLICT (user_id, apns_token) DO UPDATE SET
			app_version  = EXCLUDED.app_version,
			last_seen_at = EXCLUDED.last_seen_at,
			platform     = EXCLUDED.platform
		RETURNING id, user_id, platform, apns_token, app_version, created_at, last_seen_at`,
		userID, string(req.Platform), req.APNSToken, req.AppVersion, now,
	)
	t := &Token{}
	var platform string
	if err := row.Scan(&t.ID, &t.UserID, &platform, &t.APNSToken, &t.AppVersion, &t.CreatedAt, &t.LastSeenAt); err != nil {
		return nil, fmt.Errorf("upsert device token: %w", err)
	}
	t.Platform = Platform(platform)
	return t, nil
}

func (s *pgStore) Delete(ctx context.Context, id uuid.UUID) error {
	res, err := s.db.Exec(ctx, `DELETE FROM device_tokens WHERE id = $1`, id)
	if err != nil {
		return fmt.Errorf("delete device token: %w", err)
	}
	if res.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *pgStore) ListByUser(ctx context.Context, userID string) ([]*Token, error) {
	rows, err := s.db.Query(ctx, `
		SELECT id, user_id, platform, apns_token, app_version, created_at, last_seen_at
		FROM device_tokens WHERE user_id = $1
		ORDER BY last_seen_at DESC`, userID)
	if err != nil {
		return nil, fmt.Errorf("list device tokens: %w", err)
	}
	defer rows.Close()

	var out []*Token
	for rows.Next() {
		t := &Token{}
		var platform string
		if err := rows.Scan(&t.ID, &t.UserID, &platform, &t.APNSToken, &t.AppVersion, &t.CreatedAt, &t.LastSeenAt); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				continue
			}
			return nil, fmt.Errorf("scan device token: %w", err)
		}
		t.Platform = Platform(platform)
		out = append(out, t)
	}
	return out, rows.Err()
}

type Service struct{ store Store }

func NewService(store Store) *Service { return &Service{store: store} }

func (svc *Service) Register(ctx context.Context, userID string, req RegisterReq) (*Token, error) {
	if userID == "" {
		return nil, errors.New("user id required")
	}
	if req.APNSToken == "" {
		return nil, errors.New("apns_token required")
	}
	if req.Platform != PlatformIOS && req.Platform != PlatformAndroid {
		return nil, fmt.Errorf("invalid platform %q", req.Platform)
	}
	return svc.store.Upsert(ctx, userID, req)
}

func (svc *Service) Unregister(ctx context.Context, id uuid.UUID) error {
	return svc.store.Delete(ctx, id)
}

func (svc *Service) List(ctx context.Context, userID string) ([]*Token, error) {
	return svc.store.ListByUser(ctx, userID)
}
