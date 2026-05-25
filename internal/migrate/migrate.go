package migrate

import (
	"context"
	"embed"
	"errors"
	"fmt"
	"log"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

//go:embed sql/*.sql
var migrationsFS embed.FS

// benignCodes are PostgreSQL SQLSTATE codes for "object already exists" errors.
// Migrations are re-run on every boot (there is no version table), so the
// non-idempotent statements in early migrations (e.g. CREATE TABLE / ADD COLUMN
// without IF NOT EXISTS) re-raise these on every subsequent boot. That is
// expected and safe to skip.
//
// Any OTHER error is a genuine failure and MUST abort startup loudly. The
// motivating case: `CREATE UNIQUE INDEX` over data that contains duplicates
// fails with 23505 (unique_violation) — previously this was swallowed, leaving
// the uniqueness invariant silently unenforced. Such errors now stop the boot.
var benignCodes = map[string]bool{
	"42P07": true, // duplicate_table (also covers an already-existing index/sequence)
	"42701": true, // duplicate_column
	"42710": true, // duplicate_object — load-bearing: migration 001's bare `CREATE TYPE ... ENUM` re-raises this on every re-run
	"42P06": true, // duplicate_schema
	"42723": true, // duplicate_function
}

// isBenign reports whether err is an "already applied" error that is safe to
// skip when re-running the idempotent-by-intent migration set.
func isBenign(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && benignCodes[pgErr.Code]
}

func Run(ctx context.Context, pool *pgxpool.Pool) error {
	entries, err := migrationsFS.ReadDir("sql")
	if err != nil {
		return fmt.Errorf("read migrations dir: %w", err)
	}

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		sqlBytes, err := migrationsFS.ReadFile("sql/" + entry.Name())
		if err != nil {
			return fmt.Errorf("read migration %s: %w", entry.Name(), err)
		}
		if _, err := pool.Exec(ctx, string(sqlBytes)); err != nil {
			if isBenign(err) {
				log.Printf("migration %s: %v (already applied, continuing)", entry.Name(), err)
				continue
			}
			// Fail closed: a genuine migration error must not be swallowed.
			return fmt.Errorf("migration %s: %w", entry.Name(), err)
		}
		log.Printf("migration %s: applied", entry.Name())
	}
	return nil
}
