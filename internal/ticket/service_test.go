package ticket_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/tojiuni/goquest/internal/ticket"
)

// --- mocks ---

type mockStore struct {
	createErr error
	getTicket *ticket.Ticket
	getErr    error
	updated   *ticket.UpdateTicketReq
	updateErr error
	tickets   []*ticket.Ticket
	total     int
}

func (m *mockStore) Create(_ context.Context, t *ticket.Ticket) error {
	if m.createErr != nil {
		return m.createErr
	}
	return nil
}

func (m *mockStore) Get(_ context.Context, _ uuid.UUID) (*ticket.Ticket, error) {
	return m.getTicket, m.getErr
}

func (m *mockStore) Update(_ context.Context, _ uuid.UUID, req ticket.UpdateTicketReq) (*ticket.Ticket, error) {
	m.updated = &req
	if m.updateErr != nil {
		return nil, m.updateErr
	}
	// apply requested status so service logic can check t.Status
	result := *m.getTicket
	if req.Status != nil {
		result.Status = *req.Status
	}
	if req.AssigneeID != nil {
		result.AssigneeID = *req.AssigneeID
	}
	if req.AssigneeType != nil {
		result.AssigneeType = *req.AssigneeType
	}
	return &result, nil
}

func (m *mockStore) List(_ context.Context, _ ticket.ListTicketsFilter) ([]*ticket.Ticket, int, error) {
	return m.tickets, m.total, nil
}

func (m *mockStore) ListSubtasks(_ context.Context, _ uuid.UUID) ([]*ticket.Ticket, error) {
	return nil, nil
}

func (m *mockStore) AllSubtasksTerminal(_ context.Context, _ uuid.UUID) (bool, error) {
	return true, nil
}

func (m *mockStore) SetTicketTags(_ context.Context, _ uuid.UUID, _ []uuid.UUID) error {
	return nil
}

func (m *mockStore) CreateAttempt(_ context.Context, _ *ticket.Attempt) error {
	return nil
}

func (m *mockStore) ListAttempts(_ context.Context, _ uuid.UUID) ([]*ticket.Attempt, error) {
	return nil, nil
}

func (m *mockStore) CountAttempts(_ context.Context, _ uuid.UUID, _ string) (int, error) {
	return 0, nil
}

type mockPublisher struct {
	created  int
	assigned int
	updated  int
	finished int
}

func (m *mockPublisher) PublishCreated(_ context.Context, _ *ticket.Ticket) error {
	m.created++
	return nil
}
func (m *mockPublisher) PublishAssigned(_ context.Context, _ *ticket.Ticket) error {
	m.assigned++
	return nil
}
func (m *mockPublisher) PublishUpdated(_ context.Context, _ *ticket.Ticket) error {
	m.updated++
	return nil
}
func (m *mockPublisher) PublishCompleted(_ context.Context, _ *ticket.Ticket) error {
	m.finished++
	return nil
}

type mockNuclex struct {
	completedCalls int
	failedCalls    int
}

func (m *mockNuclex) EmitCompleted(_ context.Context, _ *ticket.Ticket) error {
	m.completedCalls++
	return nil
}
func (m *mockNuclex) EmitFailed(_ context.Context, _ *ticket.Ticket) error {
	m.failedCalls++
	return nil
}

func newTestTicket(status ticket.Status) *ticket.Ticket {
	return &ticket.Ticket{
		ID:          uuid.New(),
		Title:       "test ticket",
		Type:        ticket.TypeTask,
		Status:      status,
		Priority:    ticket.PriorityNormal,
		AssigneeType: ticket.AssigneeHuman,
		CreatedAt:   time.Now().UTC(),
		UpdatedAt:   time.Now().UTC(),
	}
}

// --- tests ---

func TestService_Create_RequireTitle(t *testing.T) {
	svc := ticket.NewService(&mockStore{}, &mockPublisher{}, &mockNuclex{}, nil)
	_, err := svc.Create(context.Background(), ticket.CreateTicketReq{}, "user1")
	if err == nil {
		t.Fatal("expected error for empty title")
	}
}

func TestService_Create_DefaultsApplied(t *testing.T) {
	store := &mockStore{}
	pub := &mockPublisher{}
	svc := ticket.NewService(store, pub, &mockNuclex{}, nil)

	req := ticket.CreateTicketReq{Title: "my task"}
	created, err := svc.Create(context.Background(), req, "user1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if created.Status != ticket.StatusPending {
		t.Errorf("expected status pending, got %s", created.Status)
	}
	if created.Type != ticket.TypeTask {
		t.Errorf("expected type task, got %s", created.Type)
	}
	if created.Priority != ticket.PriorityNormal {
		t.Errorf("expected priority normal, got %s", created.Priority)
	}
	if created.RequesterID != "user1" {
		t.Errorf("expected requester_id user1, got %s", created.RequesterID)
	}
	if pub.created != 1 {
		t.Errorf("expected 1 created event, got %d", pub.created)
	}
}

func TestService_Create_AgentTicketPublishesAssigned(t *testing.T) {
	store := &mockStore{}
	pub := &mockPublisher{}
	svc := ticket.NewService(store, pub, &mockNuclex{}, nil)

	req := ticket.CreateTicketReq{
		Title:        "agent task",
		AssigneeType: ticket.AssigneeAgent,
		AssigneeID:   "hermes-infra",
		Domain:       ticket.DomainInfra,
	}
	_, err := svc.Create(context.Background(), req, "user1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if pub.assigned != 1 {
		t.Errorf("expected 1 assigned event for agent ticket, got %d", pub.assigned)
	}
}

func TestService_Create_InvalidDomain(t *testing.T) {
	svc := ticket.NewService(&mockStore{}, &mockPublisher{}, &mockNuclex{}, nil)
	req := ticket.CreateTicketReq{Title: "x", Domain: "unknown-domain"}
	_, err := svc.Create(context.Background(), req, "user1")
	if err == nil {
		t.Fatal("expected error for invalid domain")
	}
}

func TestService_Update_CompletedEmitsNuclex(t *testing.T) {
	base := newTestTicket(ticket.StatusInProgress)
	store := &mockStore{getTicket: base}
	pub := &mockPublisher{}
	nuclex := &mockNuclex{}
	svc := ticket.NewService(store, pub, nuclex, nil)

	completed := ticket.StatusCompleted
	_, err := svc.Update(context.Background(), base.ID, ticket.UpdateTicketReq{Status: &completed})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if nuclex.completedCalls != 1 {
		t.Errorf("expected 1 nuclex EmitCompleted call, got %d", nuclex.completedCalls)
	}
	if nuclex.failedCalls != 0 {
		t.Errorf("expected 0 nuclex EmitFailed calls, got %d", nuclex.failedCalls)
	}
}

func TestService_Update_FailedEmitsNuclexFailed(t *testing.T) {
	base := newTestTicket(ticket.StatusInProgress)
	store := &mockStore{getTicket: base}
	nuclex := &mockNuclex{}
	svc := ticket.NewService(store, &mockPublisher{}, nuclex, nil)

	failed := ticket.StatusFailed
	_, err := svc.Update(context.Background(), base.ID, ticket.UpdateTicketReq{Status: &failed})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if nuclex.failedCalls != 1 {
		t.Errorf("expected 1 nuclex EmitFailed call, got %d", nuclex.failedCalls)
	}
}

func TestService_Cancel(t *testing.T) {
	base := newTestTicket(ticket.StatusPending)
	store := &mockStore{getTicket: base}
	svc := ticket.NewService(store, &mockPublisher{}, &mockNuclex{}, nil)

	_, err := svc.Cancel(context.Background(), base.ID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if store.updated == nil {
		t.Fatal("expected store.Update to be called")
	}
	if *store.updated.Status != ticket.StatusCancelled {
		t.Errorf("expected cancelled status, got %s", *store.updated.Status)
	}
}

func TestService_Get_NotFound(t *testing.T) {
	store := &mockStore{getErr: ticket.ErrNotFound}
	svc := ticket.NewService(store, &mockPublisher{}, &mockNuclex{}, nil)

	_, err := svc.Get(context.Background(), uuid.New())
	if !errors.Is(err, ticket.ErrNotFound) {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

// Phase 2 tests

func TestService_Create_DefaultsCompletionModeAndVisibility(t *testing.T) {
	store := &mockStore{}
	svc := ticket.NewService(store, &mockPublisher{}, &mockNuclex{}, nil)

	created, err := svc.Create(context.Background(), ticket.CreateTicketReq{Title: "quest"}, "u1")
	if err != nil {
		t.Fatal(err)
	}
	if created.CompletionMode != ticket.CompletionManual {
		t.Errorf("completion_mode = %q, want manual", created.CompletionMode)
	}
	if created.Visibility != ticket.VisibilityProject {
		t.Errorf("visibility = %q, want project", created.Visibility)
	}
}

func TestService_SubmitAttempt_NotAnswerMode(t *testing.T) {
	base := newTestTicket(ticket.StatusPending)
	base.CompletionMode = ticket.CompletionManual
	store := &mockStore{getTicket: base}
	svc := ticket.NewService(store, &mockPublisher{}, &mockNuclex{}, nil)

	_, err := svc.SubmitAttempt(context.Background(), base.ID, "u1", ticket.SubmitAttemptReq{Answer: "x"})
	if err == nil {
		t.Fatal("expected error for non-answer-mode ticket")
	}
}

func TestService_SubmitAttempt_TerminalTicket(t *testing.T) {
	base := newTestTicket(ticket.StatusCompleted)
	base.CompletionMode = ticket.CompletionAnswer
	base.CompletionSpec = ticket.CompletionSpec{
		"question": "2+2?", "answer_type": "exact", "correct_answer": "4", "max_attempts": 3,
	}
	store := &mockStore{getTicket: base}
	svc := ticket.NewService(store, &mockPublisher{}, &mockNuclex{}, nil)

	_, err := svc.SubmitAttempt(context.Background(), base.ID, "u1", ticket.SubmitAttemptReq{Answer: "4"})
	if err == nil {
		t.Fatal("expected error for terminal ticket")
	}
}

func TestService_SubmitAttempt_ExactCorrect(t *testing.T) {
	base := newTestTicket(ticket.StatusPending)
	base.CompletionMode = ticket.CompletionAnswer
	base.CompletionSpec = ticket.CompletionSpec{
		"question": "2+2?", "answer_type": "exact", "correct_answer": "4", "max_attempts": float64(3),
	}
	store := &mockStore{getTicket: base}
	svc := ticket.NewService(store, &mockPublisher{}, &mockNuclex{}, nil)

	attempt, err := svc.SubmitAttempt(context.Background(), base.ID, "u1", ticket.SubmitAttemptReq{Answer: "4"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if attempt.IsCorrect == nil || !*attempt.IsCorrect {
		t.Error("expected IsCorrect = true")
	}
}

func TestService_SubmitAttempt_ExactWrong(t *testing.T) {
	base := newTestTicket(ticket.StatusPending)
	base.CompletionMode = ticket.CompletionAnswer
	base.CompletionSpec = ticket.CompletionSpec{
		"question": "2+2?", "answer_type": "exact", "correct_answer": "4", "max_attempts": float64(3),
	}
	store := &mockStore{getTicket: base}
	svc := ticket.NewService(store, &mockPublisher{}, &mockNuclex{}, nil)

	attempt, err := svc.SubmitAttempt(context.Background(), base.ID, "u1", ticket.SubmitAttemptReq{Answer: "5"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if attempt.IsCorrect == nil || *attempt.IsCorrect {
		t.Error("expected IsCorrect = false")
	}
}

func TestService_IsTerminal(t *testing.T) {
	cases := []struct {
		s        ticket.Status
		terminal bool
	}{
		{ticket.StatusPending, false},
		{ticket.StatusAssigned, false},
		{ticket.StatusInProgress, false},
		{ticket.StatusCompleted, true},
		{ticket.StatusCancelled, true},
		{ticket.StatusFailed, true},
	}
	for _, tc := range cases {
		if got := tc.s.IsTerminal(); got != tc.terminal {
			t.Errorf("Status(%q).IsTerminal() = %v, want %v", tc.s, got, tc.terminal)
		}
	}
}
