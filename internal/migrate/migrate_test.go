package migrate

import (
	"errors"
	"fmt"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
)

func TestIsBenign(t *testing.T) {
	cases := []struct {
		name string
		code string
		want bool
	}{
		{"duplicate_table", "42P07", true},   // CREATE TABLE / INDEX already exists on re-run
		{"duplicate_column", "42701", true},  // ADD COLUMN already exists on re-run
		{"duplicate_object", "42710", true},  // constraint already exists
		{"unique_violation", "23505", false}, // CREATE UNIQUE INDEX over duplicate data — must fail closed
		{"undefined_table", "42P01", false},  // genuine error
	}
	for _, c := range cases {
		err := fmt.Errorf("migration wrap: %w", &pgconn.PgError{Code: c.code})
		if got := isBenign(err); got != c.want {
			t.Errorf("%s: isBenign(code=%s) = %v, want %v", c.name, c.code, got, c.want)
		}
	}

	if isBenign(errors.New("not a pg error")) {
		t.Error("isBenign(non-pg error) = true, want false")
	}
	if isBenign(nil) {
		t.Error("isBenign(nil) = true, want false")
	}
}
