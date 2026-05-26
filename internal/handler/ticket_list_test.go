package handler

import (
	"testing"

	"github.com/google/uuid"
)

func TestParseTicketUUIDs_SkipsInvalid(t *testing.T) {
	good := uuid.New()
	ids := parseTicketUUIDs([]string{good.String(), "not-a-uuid", ""})
	if len(ids) != 1 || ids[0] != good {
		t.Fatalf("want [%s], got %v", good, ids)
	}
	// must be non-nil even for nil input (SQL ANY($) safety)
	if parseTicketUUIDs(nil) == nil {
		t.Error("parseTicketUUIDs(nil) must return non-nil empty slice")
	}
}
