package ticket_test

import (
	"encoding/json"
	"testing"

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
