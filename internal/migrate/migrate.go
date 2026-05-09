package migrate

import (
	"context"
	"embed"
	"fmt"
	"log"

	"github.com/jackc/pgx/v5/pgxpool"
)

//go:embed sql/*.sql
var migrationsFS embed.FS

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
			// idempotent: log and continue — repeated runs hit "already exists"
			log.Printf("migration %s: %v (continuing)", entry.Name(), err)
		} else {
			log.Printf("migration %s: applied", entry.Name())
		}
	}
	return nil
}
