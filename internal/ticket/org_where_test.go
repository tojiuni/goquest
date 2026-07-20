package ticket

import (
	"strings"
	"testing"

	"github.com/google/uuid"
)

func TestTicketWhereOrgScope(t *testing.T) {
	org := uuid.New()

	t.Run("admin sees both sides of own org", func(t *testing.T) {
		where, args, _ := ticketWhere(ListTicketsFilter{
			OrgScope: &OrgScope{OrgID: org, Role: "admin", CallerID: "a@corp.com"},
		})
		if !strings.Contains(where, "t.requester_org_id=$1 OR t.assignee_org_id=$1") {
			t.Fatalf("admin clause missing: %s", where)
		}
		if len(args) != 1 || args[0] != org {
			t.Fatalf("args = %v", args)
		}
	})

	t.Run("member sees only own tickets within org", func(t *testing.T) {
		where, args, _ := ticketWhere(ListTicketsFilter{
			OrgScope: &OrgScope{OrgID: org, Role: "member", CallerID: "m@corp.com"},
		})
		if !strings.Contains(where, "t.requester_org_id=$1 AND t.requester_id=$2") ||
			!strings.Contains(where, "t.assignee_org_id=$1 AND t.assignee_id=$2") {
			t.Fatalf("member clause missing: %s", where)
		}
		if len(args) != 2 || args[1] != "m@corp.com" {
			t.Fatalf("args = %v", args)
		}
	})

	t.Run("side narrows admin to requester axis", func(t *testing.T) {
		where, _, _ := ticketWhere(ListTicketsFilter{
			OrgScope: &OrgScope{OrgID: org, Role: "admin", CallerID: "a@corp.com", Side: "requester"},
		})
		if strings.Contains(where, "assignee_org_id") {
			t.Fatalf("side=requester must not touch assignee axis: %s", where)
		}
	})
}
