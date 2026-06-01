package ticket_test

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/tojiuni/goquest/internal/ticket"
)

func TestCompletionSpec_MarshalJSON_Nil(t *testing.T) {
	var spec ticket.CompletionSpec
	b, err := json.Marshal(spec)
	if err != nil {
		t.Fatalf("marshal nil spec: %v", err)
	}
	if string(b) != "{}" {
		t.Errorf("expected {}, got %s", b)
	}
}

func TestCompletionSpec_MarshalJSON_Value(t *testing.T) {
	spec := ticket.CompletionSpec{
		"question":    "TCP vs UDP?",
		"answer_type": "exact",
	}
	b, err := json.Marshal(spec)
	if err != nil {
		t.Fatalf("marshal spec: %v", err)
	}
	var out map[string]any
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatalf("unmarshal back: %v", err)
	}
	if out["question"] != "TCP vs UDP?" {
		t.Errorf("question field mismatch: %v", out["question"])
	}
}

func TestTicket_NewFields_Defaults(t *testing.T) {
	tk := &ticket.Ticket{}
	if tk.Visibility != "" {
		t.Errorf("expected empty Visibility zero value, got %q", tk.Visibility)
	}
	if tk.CompletionMode != "" {
		t.Errorf("expected empty CompletionMode zero value, got %q", tk.CompletionMode)
	}
	if tk.RewardXP != 0 {
		t.Errorf("expected 0 RewardXP, got %d", tk.RewardXP)
	}
	if tk.Tags != nil {
		t.Errorf("expected nil Tags, got %v", tk.Tags)
	}
}

func TestVisibility_Constants(t *testing.T) {
	if ticket.VisibilityProject != "project" {
		t.Errorf("VisibilityProject = %q, want %q", ticket.VisibilityProject, "project")
	}
	if ticket.VisibilityRestricted != "restricted" {
		t.Errorf("VisibilityRestricted = %q, want %q", ticket.VisibilityRestricted, "restricted")
	}
}

func TestCompletionMode_Constants(t *testing.T) {
	cases := map[ticket.CompletionMode]string{
		ticket.CompletionManual:  "manual",
		ticket.CompletionAnswer:  "answer",
		ticket.CompletionAgent:   "agent",
		ticket.CompletionSubtask: "subtask",
	}
	for mode, want := range cases {
		if string(mode) != want {
			t.Errorf("CompletionMode %q: got %q, want %q", mode, string(mode), want)
		}
	}
}

func TestTicketRef_JSONShape(t *testing.T) {
	now := time.Date(2026, 5, 30, 12, 0, 0, 0, time.UTC)
	r := ticket.TicketRef{
		TicketID:    uuid.MustParse("11111111-1111-1111-1111-111111111111"),
		GopediaL1ID: "l1-abc",
		Position:    2,
		CreatedAt:   now,
		CreatedBy:   "user@example.com",
	}
	b, err := json.Marshal(r)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	got := string(b)
	want := `{"ticket_id":"11111111-1111-1111-1111-111111111111","gopedia_l1_id":"l1-abc","position":2,"created_at":"2026-05-30T12:00:00Z","created_by":"user@example.com"}`
	if got != want {
		t.Errorf("json shape mismatch:\n got=%s\nwant=%s", got, want)
	}
}

func TestTicket_RefsOmittedWhenEmpty(t *testing.T) {
	var tk ticket.Ticket
	b, _ := json.Marshal(tk)
	if strings.Contains(string(b), `"ref_ids"`) {
		t.Errorf("empty Refs must be omitted: %s", string(b))
	}
}

func TestTicket_RefsPresentWhenSet(t *testing.T) {
	tk := ticket.Ticket{Refs: []string{"l1-a", "l1-b"}}
	b, _ := json.Marshal(tk)
	if !strings.Contains(string(b), `"ref_ids":["l1-a","l1-b"]`) {
		t.Errorf("Refs not serialized as ref_ids: %s", string(b))
	}
}

func TestTicket_CancellationNoteOmittedWhenNil(t *testing.T) {
	var tk ticket.Ticket
	b, _ := json.Marshal(tk)
	if strings.Contains(string(b), `"cancellation_note"`) {
		t.Errorf("nil CancellationNote must be omitted: %s", string(b))
	}
}

func TestTicket_CancellationNotePresentWhenSet(t *testing.T) {
	note := "rejected by approver"
	tk := ticket.Ticket{CancellationNote: &note}
	b, _ := json.Marshal(tk)
	if !strings.Contains(string(b), `"cancellation_note":"rejected by approver"`) {
		t.Errorf("CancellationNote not serialized: %s", string(b))
	}
}

func TestUpdateTicketReq_CancellationNoteOmittedWhenNil(t *testing.T) {
	req := ticket.UpdateTicketReq{}
	b, _ := json.Marshal(req)
	if strings.Contains(string(b), `"cancellation_note"`) {
		t.Errorf("nil CancellationNote must be omitted from UpdateTicketReq: %s", string(b))
	}
}

func TestUpdateTicketReq_CancellationNotePresentWhenSet(t *testing.T) {
	note := "cascade reject"
	req := ticket.UpdateTicketReq{CancellationNote: &note}
	b, _ := json.Marshal(req)
	if !strings.Contains(string(b), `"cancellation_note":"cascade reject"`) {
		t.Errorf("CancellationNote not in UpdateTicketReq JSON: %s", string(b))
	}
}
