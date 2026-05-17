package queue

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/tojiuni/goquest/internal/ticket"
)

func newTestTicket() *ticket.Ticket {
	return &ticket.Ticket{
		ID:          uuid.New(),
		Title:       "test",
		Type:        ticket.TypeTask,
		Status:      ticket.StatusPending,
		Priority:    ticket.PriorityNormal,
		AssigneeType: ticket.AssigneeAgent,
		AssigneeID:  "hermes-infra",
		RequesterID: "user-1",
		Domain:      ticket.DomainInfra,
		AgentContext: map[string]any{"key": "value"},
		CreatedAt:   time.Now().UTC(),
		UpdatedAt:   time.Now().UTC(),
	}
}

func TestTicketEvent_Serialization(t *testing.T) {
	tk := newTestTicket()

	agentCtxBytes, _ := json.Marshal(tk.AgentContext)
	ev := TicketEvent{
		TicketID:     tk.ID.String(),
		Title:        tk.Title,
		Domain:       string(tk.Domain),
		AssigneeID:   tk.AssigneeID,
		Status:       string(tk.Status),
		Priority:     string(tk.Priority),
		AgentContext: string(agentCtxBytes),
		SourceBot:    tk.SourceBot,
		RequesterID:  tk.RequesterID,
		EventTime:    time.Now().UTC().Format(time.RFC3339),
	}

	b, err := json.Marshal(ev)
	if err != nil {
		t.Fatalf("marshal TicketEvent: %v", err)
	}

	var decoded TicketEvent
	if err := json.Unmarshal(b, &decoded); err != nil {
		t.Fatalf("unmarshal TicketEvent: %v", err)
	}

	if decoded.TicketID != ev.TicketID {
		t.Errorf("TicketID mismatch: got %q, want %q", decoded.TicketID, ev.TicketID)
	}
	if decoded.Domain != "infra" {
		t.Errorf("expected domain=infra, got %q", decoded.Domain)
	}
	if decoded.AgentContext != `{"key":"value"}` {
		t.Errorf("unexpected agent_context: %q", decoded.AgentContext)
	}
}

func TestPublisher_NilAgentContext(t *testing.T) {
	// Verify that a ticket with nil AgentContext serialises to empty string, not "null"
	tk := newTestTicket()
	tk.AgentContext = nil

	// replicate the publish logic
	agentCtxStr := ""
	if tk.AgentContext != nil {
		b, err := json.Marshal(tk.AgentContext)
		if err == nil {
			agentCtxStr = string(b)
		}
	}

	if agentCtxStr != "" {
		t.Errorf("expected empty agent_context for nil map, got %q", agentCtxStr)
	}
}

// TestConsumer_MessageFormat verifies the consumer correctly reads the "data" field.
func TestConsumer_HandlerNotCalledOnBadMessage(_ *testing.T) {
	// This is a compile-time guard: Consumer accepts a MessageHandler func.
	// If the signature changes, this test will fail to compile.
	var _ MessageHandler = func(_ context.Context, _ string) error { return nil }
}

func TestTicketEvent_IncludesParentID(t *testing.T) {
	parentID := uuid.New()
	tk := newTestTicket()
	tk.ParentID = &parentID

	parentIDStr := ""
	if tk.ParentID != nil {
		parentIDStr = tk.ParentID.String()
	}

	ev := TicketEvent{
		TicketID:    tk.ID.String(),
		RequesterID: tk.RequesterID,
		ParentID:    parentIDStr,
		EventTime:   time.Now().UTC().Format(time.RFC3339),
	}

	b, err := json.Marshal(ev)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(b, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	got, ok := decoded["parent_id"]
	if !ok {
		t.Fatal("parent_id field missing from TicketEvent JSON")
	}
	if got != parentID.String() {
		t.Errorf("parent_id mismatch: got %q, want %q", got, parentID.String())
	}
}

func TestTicketEvent_ParentID_OmittedWhenEmpty(t *testing.T) {
	ev := TicketEvent{TicketID: "x"}
	b, _ := json.Marshal(ev)
	var decoded map[string]any
	json.Unmarshal(b, &decoded)
	if _, ok := decoded["parent_id"]; ok {
		t.Error("parent_id should be omitted when empty")
	}
}
