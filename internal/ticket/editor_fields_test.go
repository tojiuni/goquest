package ticket_test

import (
	"reflect"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/tojiuni/goquest/internal/ticket"
)

func TestEditorFieldViolations(t *testing.T) {
	s := func(v string) *string { return &v }
	st := func(v ticket.Status) *ticket.Status { return &v }
	pr := func(v ticket.Priority) *ticket.Priority { return &v }
	now := time.Now()
	id := uuid.New()

	cases := []struct {
		name string
		req  ticket.UpdateTicketReq
		want []string
	}{
		{"empty request is within the remit", ticket.UpdateTicketReq{}, nil},
		{"status", ticket.UpdateTicketReq{Status: st(ticket.StatusInProgress)}, nil},
		{"completed is a normal status change", ticket.UpdateTicketReq{Status: st(ticket.StatusCompleted)}, nil},
		{"pending_approval too", ticket.UpdateTicketReq{Status: st(ticket.StatusPendingApproval)}, nil},
		{"title and description", ticket.UpdateTicketReq{Title: s("t"), Description: s("d")}, nil},
		{"assignee, dates, priority", ticket.UpdateTicketReq{AssigneeID: s("a"), DueAt: &now, StartAt: &now, Priority: pr(ticket.PriorityHigh)}, nil},
		{"plain metadata", ticket.UpdateTicketReq{Metadata: map[string]any{"note": 1}}, nil},

		// cancelling is the DELETE route by another name
		{"cancel", ticket.UpdateTicketReq{Status: st(ticket.StatusCancelled)}, []string{"status"}},
		// title_name is the VCS link handle
		{"title_name", ticket.UpdateTicketReq{TitleName: s("goq-x-y")}, []string{"title_name"}},
		{"move and reparent", ticket.UpdateTicketReq{ProjectID: &id, ParentID: &id}, []string{"parent_id", "project_id"}},
		{"visibility, domain, risk", ticket.UpdateTicketReq{Visibility: vis("restricted"), Domain: dom("code"), RiskTier: risk("high")}, []string{"domain", "risk_tier", "visibility"}},
		{"tags and refs", ticket.UpdateTicketReq{TagIDs: []uuid.UUID{id}, RefIDs: refs()}, []string{"ref_ids", "tag_ids"}},
		{"an empty (clearing) tag list is still a write", ticket.UpdateTicketReq{TagIDs: []uuid.UUID{}}, []string{"tag_ids"}},
		{"server-written metadata keys", ticket.UpdateTicketReq{Metadata: map[string]any{"approval_decision": "approved"}}, []string{"metadata"}},
		{"the server-owned origin key", ticket.UpdateTicketReq{Metadata: map[string]any{ticket.MetaPendingApprovalFrom: "in_progress"}}, []string{"metadata"}},
		{"mixed: one bad field rejects the lot, and all bad ones are listed",
			ticket.UpdateTicketReq{Title: s("t"), TitleName: s("goq-a-b"), Status: st(ticket.StatusCancelled)}, []string{"status", "title_name"}},
	}
	for _, c := range cases {
		got := ticket.EditorFieldViolations(c.req)
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s: got %v want %v", c.name, got, c.want)
		}
	}
}

// Every field UpdateTicketReq accepts is either explicitly allowed for editors or
// reported; there is no third state to forget. This fails when someone adds a new
// update field and must decide which side of the line it is on.
func TestEditorFieldViolations_CoversEveryUpdateField(t *testing.T) {
	allowed := map[string]bool{
		"status": true, "title": true, "description": true, "assignee_id": true,
		"assignee_type": true, "assignee_org_id": true, "due_at": true, "start_at": true,
		"priority": true, "metadata": true,
	}
	// the fields a manager-only caller must not set, as of the approved spec
	managerOnly := []string{
		"title_name", "risk_tier", "domain", "project_id", "parent_id", "agent_context",
		"visibility", "completion_mode", "completion_spec", "reward_xp", "callback_url",
		"tag_ids", "ref_ids", "cancellation_note",
	}
	known := ticket.UpdateFieldNames()
	for name := range known {
		if allowed[name] {
			continue
		}
		found := false
		for _, m := range managerOnly {
			if m == name {
				found = true
			}
		}
		if !found {
			t.Errorf("update field %q is neither editor-allowed nor on the manager-only list: decide which and update the spec", name)
		}
	}
	for _, m := range managerOnly {
		if !known[m] {
			t.Errorf("manager-only list names %q which UpdateTicketReq no longer has", m)
		}
	}
}

func vis(v string) *ticket.Visibility { x := ticket.Visibility(v); return &x }
func dom(v string) *ticket.Domain     { x := ticket.Domain(v); return &x }
func risk(v string) *ticket.RiskTier  { x := ticket.RiskTier(v); return &x }
func refs() *[]string                 { x := []string{"geneso:a/b.md"}; return &x }
