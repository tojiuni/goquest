package handler

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/tojiuni/goquest/internal/auth"
)

// TestParseTimeQuery covers goq-ticket-list-date-range-filter's created_from/
// created_to/completed_from/completed_to query params: valid RFC3339 parses,
// empty/malformed input is silently ignored (nil) rather than erroring,
// matching this file's convention for other optional filter params.
func TestParseTimeQuery(t *testing.T) {
	t.Run("empty string returns nil", func(t *testing.T) {
		if got := parseTimeQuery(""); got != nil {
			t.Fatalf("want nil, got %v", got)
		}
	})

	t.Run("malformed value returns nil", func(t *testing.T) {
		if got := parseTimeQuery("2026-08-08"); got != nil {
			t.Fatalf("want nil for a bare date (not RFC3339), got %v", got)
		}
		if got := parseTimeQuery("not-a-timestamp"); got != nil {
			t.Fatalf("want nil, got %v", got)
		}
	})

	t.Run("valid RFC3339 UTC timestamp parses", func(t *testing.T) {
		got := parseTimeQuery("2026-08-08T00:00:00Z")
		want := time.Date(2026, 8, 8, 0, 0, 0, 0, time.UTC)
		if got == nil || !got.Equal(want) {
			t.Fatalf("want %v, got %v", want, got)
		}
	})
}

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

// TestOrgScopeForSide covers goq-org-claim-staff-visibility-bypass: a caller
// whose JWT carries an org claim must NOT be silently limited to org-only
// results unless they explicitly asked for an org-scoped view via org_side.
// A staff/owner account that also holds an org claim (e.g. for org-admin
// testing) has to keep seeing its normal SpiceDB-visible tickets on plain
// GET /tickets — only the portal's explicit org_side=requester|assignee
// narrows to the org axis.
func TestOrgScopeForSide(t *testing.T) {
	org := &auth.OrgCtx{ID: uuid.New(), Role: "admin"}

	t.Run("nil org claim never yields a scope", func(t *testing.T) {
		if got := orgScopeForSide(nil, "caller@corp.com", "requester"); got != nil {
			t.Fatalf("want nil, got %+v", got)
		}
	})

	t.Run("org claim without an explicit side does not scope — staff+org callers keep normal visibility", func(t *testing.T) {
		if got := orgScopeForSide(org, "caller@corp.com", ""); got != nil {
			t.Fatalf("want nil (fall through to SpiceDB visibility), got %+v", got)
		}
	})

	t.Run("unrecognized side value does not scope either", func(t *testing.T) {
		if got := orgScopeForSide(org, "caller@corp.com", "bogus"); got != nil {
			t.Fatalf("want nil, got %+v", got)
		}
	})

	t.Run("side=requester scopes to the requester axis", func(t *testing.T) {
		got := orgScopeForSide(org, "caller@corp.com", "requester")
		if got == nil {
			t.Fatal("want non-nil scope")
		}
		if got.OrgID != org.ID || got.Role != org.Role || got.CallerID != "caller@corp.com" || got.Side != "requester" {
			t.Fatalf("got %+v", got)
		}
	})

	t.Run("side=assignee scopes to the assignee axis", func(t *testing.T) {
		got := orgScopeForSide(org, "caller@corp.com", "assignee")
		if got == nil || got.Side != "assignee" {
			t.Fatalf("got %+v", got)
		}
	})
}
