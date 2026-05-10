// Package pii implements compliance-driven anonymisation of customer PII on
// completed tickets older than the configured retention window.
//
// This is intended to be called from a periodic cron / scheduled job, not on
// the request hot path.
package pii

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Default retention windows. PII is masked after Standard, hard-deleted after Hard.
const (
	DefaultStandardRetention = 3 * 365 * 24 * time.Hour // 3 years
	DefaultHardRetention     = 5 * 365 * 24 * time.Hour // 5 years
)

type Masker struct {
	pool                *pgxpool.Pool
	standardRetention   time.Duration
	hardRetention       time.Duration
}

func New(pool *pgxpool.Pool) *Masker {
	return &Masker{
		pool:              pool,
		standardRetention: DefaultStandardRetention,
		hardRetention:     DefaultHardRetention,
	}
}

// SetRetention overrides the default retention windows (for tests/configuration).
func (m *Masker) SetRetention(standard, hard time.Duration) {
	m.standardRetention = standard
	m.hardRetention = hard
}

// Result reports counts from a single Run() invocation.
type Result struct {
	MaskedTickets int64 `json:"masked_tickets"`
	DeletedAttempts int64 `json:"deleted_attempts"`
}

// Run anonymises completed tickets older than the standard retention and
// hard-deletes attempts older than the hard retention. Idempotent — running
// it twice in a row is harmless.
func (m *Masker) Run(ctx context.Context) (*Result, error) {
	now := time.Now().UTC()
	standardCutoff := now.Add(-m.standardRetention)
	hardCutoff := now.Add(-m.hardRetention)

	res := &Result{}

	// 1. Mask requester_email + requester_name on tickets completed/cancelled
	//    before the standard cutoff. requester_id stays so audit trails resolve.
	tag, err := m.pool.Exec(ctx, `
		UPDATE tickets
		SET requester_email = NULL,
		    requester_name  = NULL,
		    description     = '[redacted]'
		WHERE status IN ('completed','cancelled','failed')
		  AND completed_at IS NOT NULL
		  AND completed_at < $1
		  AND (requester_email IS NOT NULL OR requester_name IS NOT NULL)
	`, standardCutoff)
	if err != nil {
		return nil, fmt.Errorf("mask tickets: %w", err)
	}
	res.MaskedTickets = tag.RowsAffected()

	// 2. Hard-delete attempt rows past the hard retention window. They contain
	//    free-form answer bodies that may include personal info.
	tag2, err := m.pool.Exec(ctx, `
		DELETE FROM ticket_attempts
		WHERE attempted_at < $1
	`, hardCutoff)
	if err != nil {
		return nil, fmt.Errorf("delete attempts: %w", err)
	}
	res.DeletedAttempts = tag2.RowsAffected()

	log.Printf("pii: masked=%d deleted_attempts=%d", res.MaskedTickets, res.DeletedAttempts)
	return res, nil
}
