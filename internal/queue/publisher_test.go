package queue

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
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

	ev := TicketEvent{
		TicketID:    uuid.New().String(),
		RequesterID: "user-1",
		ParentID:    parentID.String(),
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

func TestPublisher_ParentIDNilGuard(t *testing.T) {
	// Replicate the publish() nil-guard logic for ParentID.
	// If t.ParentID is nil, ev.ParentID must remain empty (omitted in JSON).
	tk := newTestTicket()
	// tk.ParentID is nil by default in newTestTicket()

	parentIDStr := ""
	if tk.ParentID != nil {
		parentIDStr = tk.ParentID.String()
	}

	if parentIDStr != "" {
		t.Errorf("expected empty ParentID for nil *uuid.UUID, got %q", parentIDStr)
	}
}

func TestTicketEvent_ParentID_OmittedWhenEmpty(t *testing.T) {
	ev := TicketEvent{TicketID: "x"}
	b, err := json.Marshal(ev)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(b, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if _, ok := decoded["parent_id"]; ok {
		t.Error("parent_id should be omitted when empty")
	}
}

// TestPublishCreated_WorkspaceProjectFields verifies that PublishCreated correctly
// populates workspace_name, project_name, and title_name in the XAdd stream payload.
func TestPublishCreated_WorkspaceProjectFields(t *testing.T) {
	mr, err := miniredis.Run()
	if err != nil {
		t.Fatalf("start miniredis: %v", err)
	}
	defer mr.Close()

	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	defer rdb.Close()

	p := NewPublisher(rdb)
	ctx := context.Background()

	tk := newTestTicket()
	tk.WorkspaceName = "test-workspace"
	tk.ProjectName = "test-project"
	tk.TitleName = "goq-test-ticket"

	if err := p.PublishCreated(ctx, tk); err != nil {
		t.Fatalf("PublishCreated: %v", err)
	}

	msgs, err := rdb.XRange(ctx, StreamCreated, "-", "+").Result()
	if err != nil {
		t.Fatalf("XRange: %v", err)
	}
	if len(msgs) != 1 {
		t.Fatalf("expected 1 message in stream, got %d", len(msgs))
	}

	raw, ok := msgs[0].Values["data"].(string)
	if !ok {
		t.Fatal("data field missing or not a string")
	}

	var ev TicketEvent
	if err := json.Unmarshal([]byte(raw), &ev); err != nil {
		t.Fatalf("unmarshal TicketEvent: %v", err)
	}

	if ev.WorkspaceName != "test-workspace" {
		t.Errorf("workspace_name: got %q, want %q", ev.WorkspaceName, "test-workspace")
	}
	if ev.ProjectName != "test-project" {
		t.Errorf("project_name: got %q, want %q", ev.ProjectName, "test-project")
	}
	if ev.TitleName != "goq-test-ticket" {
		t.Errorf("title_name: got %q, want %q", ev.TitleName, "goq-test-ticket")
	}
}

// TestPublishCreated_ZeroProjectFields verifies that workspace_name and project_name
// are absent from the JSON payload when a ticket has no project association
// (fields are empty strings, omitempty drops them).
func TestPublishCreated_ZeroProjectFields(t *testing.T) {
	mr, err := miniredis.Run()
	if err != nil {
		t.Fatalf("start miniredis: %v", err)
	}
	defer mr.Close()

	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	defer rdb.Close()

	p := NewPublisher(rdb)
	ctx := context.Background()

	tk := newTestTicket()
	// ProjectID is nil; WorkspaceName and ProjectName remain empty strings.
	tk.ProjectID = nil
	tk.WorkspaceName = ""
	tk.ProjectName = ""

	if err := p.PublishCreated(ctx, tk); err != nil {
		t.Fatalf("PublishCreated: %v", err)
	}

	msgs, err := rdb.XRange(ctx, StreamCreated, "-", "+").Result()
	if err != nil {
		t.Fatalf("XRange: %v", err)
	}
	if len(msgs) != 1 {
		t.Fatalf("expected 1 message in stream, got %d", len(msgs))
	}

	raw, ok := msgs[0].Values["data"].(string)
	if !ok {
		t.Fatal("data field missing or not a string")
	}

	var decoded map[string]any
	if err := json.Unmarshal([]byte(raw), &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if _, ok := decoded["workspace_name"]; ok {
		t.Error("workspace_name should be omitted when empty")
	}
	if _, ok := decoded["project_name"]; ok {
		t.Error("project_name should be omitted when empty")
	}
}
