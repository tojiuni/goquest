package ticket_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
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

	reopenedID uuid.UUID
	reopenErr  error

	// created records every ticket passed to Create, in call order — used by
	// bulk-create tests to assert per-item field inheritance (goq-cogito-bulk-create-api).
	created []*ticket.Ticket
}

func (m *mockStore) Create(_ context.Context, t *ticket.Ticket) error {
	if m.createErr != nil {
		return m.createErr
	}
	m.created = append(m.created, t)
	return nil
}

func (m *mockStore) Get(_ context.Context, _ uuid.UUID) (*ticket.Ticket, error) {
	return m.getTicket, m.getErr
}

func (m *mockStore) GetByProjectAndTitleName(_ context.Context, _ uuid.UUID, _ string) (*ticket.Ticket, error) {
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

func (m *mockStore) ReopenTerminal(_ context.Context, id uuid.UUID) (*ticket.Ticket, error) {
	m.reopenedID = id
	if m.reopenErr != nil {
		return nil, m.reopenErr
	}
	result := *m.getTicket
	result.Status = ticket.StatusInProgress
	result.CompletedAt = nil
	return &result, nil
}

func (m *mockStore) Aggregate(_ context.Context, _ string, _ ticket.ListTicketsFilter) ([]ticket.AggBucket, error) {
	return nil, nil
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

func (m *mockStore) ListOverdue(_ context.Context) ([]*ticket.Ticket, error) {
	return nil, nil
}

// --- refs no-op stubs (Task 4 compile-glue; real behavior may be added in Task 5) ---

func (m *mockStore) AddRefs(_ context.Context, _ uuid.UUID, _ []string, _ string) ([]ticket.TicketRef, error) {
	return nil, nil
}
func (m *mockStore) ListRefs(_ context.Context, _ uuid.UUID) ([]ticket.TicketRef, error) {
	return nil, nil
}
func (m *mockStore) RemoveRef(_ context.Context, _ uuid.UUID, _ string) error { return nil }
func (m *mockStore) ReorderRefs(_ context.Context, _ uuid.UUID, _ []string) error {
	return nil
}
func (m *mockStore) CountRefs(_ context.Context, _ uuid.UUID) (int, error) { return 0, nil }
func (m *mockStore) ListTicketIDsByRef(_ context.Context, _ string) ([]uuid.UUID, error) {
	return nil, nil
}
func (m *mockStore) ListRefIDsForTickets(_ context.Context, _ []uuid.UUID) (map[uuid.UUID][]string, error) {
	return nil, nil
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

type mockWebhook struct {
	calls atomic.Int32
	url   string
}

func (m *mockWebhook) Send(_ context.Context, url string, _ any) error {
	m.calls.Add(1)
	m.url = url
	return nil
}

type mockNuclex struct {
	completedCalls       int
	failedCalls          int
	pendingApprovalCalls int
	createdCalls         []ticket.Ticket
}

func (m *mockNuclex) EmitCompleted(_ context.Context, _ *ticket.Ticket) error {
	m.completedCalls++
	return nil
}
func (m *mockNuclex) EmitFailed(_ context.Context, _ *ticket.Ticket) error {
	m.failedCalls++
	return nil
}
func (m *mockNuclex) EmitPendingApproval(_ context.Context, _ *ticket.Ticket) error {
	m.pendingApprovalCalls++
	return nil
}
func (m *mockNuclex) EmitCreated(_ context.Context, t *ticket.Ticket) error {
	m.createdCalls = append(m.createdCalls, *t)
	return nil
}

func newTestTicket(status ticket.Status) *ticket.Ticket {
	return &ticket.Ticket{
		ID:           uuid.New(),
		Title:        "test ticket",
		Type:         ticket.TypeTask,
		Status:       status,
		Priority:     ticket.PriorityNormal,
		AssigneeType: ticket.AssigneeHuman,
		CreatedAt:    time.Now().UTC(),
		UpdatedAt:    time.Now().UTC(),
	}
}

// --- tests ---

func TestService_Create_RequireTitle(t *testing.T) {
	svc := ticket.NewService(&mockStore{}, &mockPublisher{}, &mockNuclex{}, nil, nil, nil)
	_, err := svc.Create(context.Background(), ticket.CreateTicketReq{}, "user1")
	if err == nil {
		t.Fatal("expected error for empty title")
	}
}

func TestService_Create_SubtaskInheritsParentProject(t *testing.T) {
	proj := uuid.New()
	parentID := uuid.New()
	store := &mockStore{getTicket: &ticket.Ticket{ID: parentID, ProjectID: &proj}}
	svc := ticket.NewService(store, &mockPublisher{}, &mockNuclex{}, nil, nil, nil)

	// subtask with a parent but no project → should inherit parent's project.
	created, err := svc.Create(context.Background(),
		ticket.CreateTicketReq{Title: "sub", ParentID: &parentID}, "user1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if created.ProjectID == nil || *created.ProjectID != proj {
		t.Errorf("subtask did not inherit parent project: got %v, want %s", created.ProjectID, proj)
	}
}

func TestService_Create_ExplicitProjectNotOverridden(t *testing.T) {
	parentProj := uuid.New()
	ownProj := uuid.New()
	parentID := uuid.New()
	store := &mockStore{getTicket: &ticket.Ticket{ID: parentID, ProjectID: &parentProj}}
	svc := ticket.NewService(store, &mockPublisher{}, &mockNuclex{}, nil, nil, nil)

	// explicit project on the subtask must win over the parent's.
	created, err := svc.Create(context.Background(),
		ticket.CreateTicketReq{Title: "sub", ParentID: &parentID, ProjectID: &ownProj}, "user1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if created.ProjectID == nil || *created.ProjectID != ownProj {
		t.Errorf("explicit project overridden: got %v, want %s", created.ProjectID, ownProj)
	}
}

func TestService_Create_TopLevelNoProjectStaysNil(t *testing.T) {
	store := &mockStore{}
	svc := ticket.NewService(store, &mockPublisher{}, &mockNuclex{}, nil, nil, nil)

	// no parent → no inheritance, project stays nil (Get is never consulted).
	created, err := svc.Create(context.Background(),
		ticket.CreateTicketReq{Title: "top"}, "user1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if created.ProjectID != nil {
		t.Errorf("top-level ticket got a project: %v", created.ProjectID)
	}
}

func TestService_Create_DefaultsApplied(t *testing.T) {
	store := &mockStore{}
	pub := &mockPublisher{}
	svc := ticket.NewService(store, pub, &mockNuclex{}, nil, nil, nil)

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
	svc := ticket.NewService(store, pub, &mockNuclex{}, nil, nil, nil)

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
	svc := ticket.NewService(&mockStore{}, &mockPublisher{}, &mockNuclex{}, nil, nil, nil)
	req := ticket.CreateTicketReq{Title: "x", Domain: "unknown-domain"}
	_, err := svc.Create(context.Background(), req, "user1")
	if err == nil {
		t.Fatal("expected error for invalid domain")
	}
}

func TestService_Create_DefaultsRiskTierLow(t *testing.T) {
	store := &mockStore{}
	svc := ticket.NewService(store, &mockPublisher{}, &mockNuclex{}, nil, nil, nil)

	created, err := svc.Create(context.Background(), ticket.CreateTicketReq{Title: "no risk given"}, "user1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if created.RiskTier != ticket.RiskLow {
		t.Errorf("expected default risk_tier=low, got %q", created.RiskTier)
	}
}

func TestService_Create_RiskTierPersisted(t *testing.T) {
	store := &mockStore{}
	svc := ticket.NewService(store, &mockPublisher{}, &mockNuclex{}, nil, nil, nil)

	created, err := svc.Create(context.Background(),
		ticket.CreateTicketReq{Title: "risky", RiskTier: ticket.RiskHigh}, "user1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if created.RiskTier != ticket.RiskHigh {
		t.Errorf("expected risk_tier=high, got %q", created.RiskTier)
	}
}

func TestService_Create_InvalidRiskTier(t *testing.T) {
	svc := ticket.NewService(&mockStore{}, &mockPublisher{}, &mockNuclex{}, nil, nil, nil)
	_, err := svc.Create(context.Background(),
		ticket.CreateTicketReq{Title: "x", RiskTier: ticket.RiskTier("critical")}, "user1")
	if err == nil {
		t.Fatal("expected error for invalid risk_tier")
	}
}

func TestService_Update_InvalidRiskTier(t *testing.T) {
	existing := newTestTicket(ticket.StatusPending)
	store := &mockStore{getTicket: existing}
	svc := ticket.NewService(store, &mockPublisher{}, &mockNuclex{}, nil, nil, nil)

	bad := ticket.RiskTier("extreme")
	_, err := svc.Update(context.Background(), existing.ID, ticket.UpdateTicketReq{RiskTier: &bad}, "test-actor")
	if err == nil {
		t.Fatal("expected error for invalid risk_tier on update")
	}
}

func TestService_Update_RiskTierPassedToStore(t *testing.T) {
	existing := newTestTicket(ticket.StatusPending)
	store := &mockStore{getTicket: existing}
	svc := ticket.NewService(store, &mockPublisher{}, &mockNuclex{}, nil, nil, nil)

	rt := ticket.RiskMedium
	_, err := svc.Update(context.Background(), existing.ID, ticket.UpdateTicketReq{RiskTier: &rt}, "test-actor")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if store.updated == nil || store.updated.RiskTier == nil || *store.updated.RiskTier != ticket.RiskMedium {
		t.Errorf("expected risk_tier=medium passed to store, got %v", store.updated)
	}
}

func TestService_Update_CompletedEmitsNuclex(t *testing.T) {
	base := newTestTicket(ticket.StatusInProgress)
	store := &mockStore{getTicket: base}
	pub := &mockPublisher{}
	nuclex := &mockNuclex{}
	svc := ticket.NewService(store, pub, nuclex, nil, nil, nil)

	completed := ticket.StatusCompleted
	_, err := svc.Update(context.Background(), base.ID, ticket.UpdateTicketReq{Status: &completed}, "test-actor")
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
	svc := ticket.NewService(store, &mockPublisher{}, nuclex, nil, nil, nil)

	failed := ticket.StatusFailed
	_, err := svc.Update(context.Background(), base.ID, ticket.UpdateTicketReq{Status: &failed}, "test-actor")
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
	svc := ticket.NewService(store, &mockPublisher{}, &mockNuclex{}, nil, nil, nil)

	_, err := svc.Cancel(context.Background(), base.ID, "test-actor")
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

func TestService_Reopen_CompletedToInProgress(t *testing.T) {
	base := newTestTicket(ticket.StatusCompleted)
	store := &mockStore{getTicket: base}
	svc := ticket.NewService(store, &mockPublisher{}, &mockNuclex{}, nil, nil, nil)

	got, err := svc.Reopen(context.Background(), base.ID, "test-actor", "reopened by mistake")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.Status != ticket.StatusInProgress {
		t.Errorf("status = %s, want in_progress", got.Status)
	}
	if got.CompletedAt != nil {
		t.Error("expected completed_at cleared")
	}
	if store.reopenedID != base.ID {
		t.Errorf("store.ReopenTerminal called with %s, want %s", store.reopenedID, base.ID)
	}
}

func TestService_Reopen_CancelledToInProgress(t *testing.T) {
	base := newTestTicket(ticket.StatusCancelled)
	store := &mockStore{getTicket: base}
	svc := ticket.NewService(store, &mockPublisher{}, &mockNuclex{}, nil, nil, nil)

	got, err := svc.Reopen(context.Background(), base.ID, "test-actor", "reopened by mistake")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.Status != ticket.StatusInProgress {
		t.Errorf("status = %s, want in_progress", got.Status)
	}
}

func TestService_Reopen_NonTerminalRejected(t *testing.T) {
	base := newTestTicket(ticket.StatusInProgress)
	store := &mockStore{getTicket: base}
	svc := ticket.NewService(store, &mockPublisher{}, &mockNuclex{}, nil, nil, nil)

	_, err := svc.Reopen(context.Background(), base.ID, "test-actor", "reason")
	if !errors.Is(err, ticket.ErrInvalidTransition) {
		t.Fatalf("err = %v, want errors.Is(err, ticket.ErrInvalidTransition)", err)
	}
	if store.reopenedID != uuid.Nil {
		t.Error("store.ReopenTerminal must not be called for a non-terminal ticket")
	}
}

func TestService_Reopen_RequiresReason(t *testing.T) {
	base := newTestTicket(ticket.StatusCompleted)
	store := &mockStore{getTicket: base}
	svc := ticket.NewService(store, &mockPublisher{}, &mockNuclex{}, nil, nil, nil)

	_, err := svc.Reopen(context.Background(), base.ID, "test-actor", "  ")
	if !errors.Is(err, ticket.ErrValidation) {
		t.Fatalf("err = %v, want errors.Is(err, ticket.ErrValidation)", err)
	}
}

func TestService_Reopen_NotFound(t *testing.T) {
	store := &mockStore{getErr: ticket.ErrNotFound}
	svc := ticket.NewService(store, &mockPublisher{}, &mockNuclex{}, nil, nil, nil)

	_, err := svc.Reopen(context.Background(), uuid.New(), "test-actor", "reason")
	if !errors.Is(err, ticket.ErrNotFound) {
		t.Fatalf("err = %v, want errors.Is(err, ticket.ErrNotFound)", err)
	}
}

func TestService_Get_NotFound(t *testing.T) {
	store := &mockStore{getErr: ticket.ErrNotFound}
	svc := ticket.NewService(store, &mockPublisher{}, &mockNuclex{}, nil, nil, nil)

	_, err := svc.Get(context.Background(), uuid.New())
	if !errors.Is(err, ticket.ErrNotFound) {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

// Phase 2 tests

func TestService_Create_DefaultsCompletionModeAndVisibility(t *testing.T) {
	store := &mockStore{}
	svc := ticket.NewService(store, &mockPublisher{}, &mockNuclex{}, nil, nil, nil)

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
	svc := ticket.NewService(store, &mockPublisher{}, &mockNuclex{}, nil, nil, nil)

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
	svc := ticket.NewService(store, &mockPublisher{}, &mockNuclex{}, nil, nil, nil)

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
	svc := ticket.NewService(store, &mockPublisher{}, &mockNuclex{}, nil, nil, nil)

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
	svc := ticket.NewService(store, &mockPublisher{}, &mockNuclex{}, nil, nil, nil)

	attempt, err := svc.SubmitAttempt(context.Background(), base.ID, "u1", ticket.SubmitAttemptReq{Answer: "5"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if attempt.IsCorrect == nil || *attempt.IsCorrect {
		t.Error("expected IsCorrect = false")
	}
}

func TestService_Update_CompletedFiresWebhook(t *testing.T) {
	var called atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	base := newTestTicket(ticket.StatusInProgress)
	base.CallbackURL = srv.URL
	store := &mockStore{getTicket: base}
	wh := &mockWebhook{}
	svc := ticket.NewService(store, &mockPublisher{}, &mockNuclex{}, nil, wh, nil)

	completed := ticket.StatusCompleted
	_, err := svc.Update(context.Background(), base.ID, ticket.UpdateTicketReq{Status: &completed}, "test-actor")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// webhook fires asynchronously — give it a moment
	time.Sleep(100 * time.Millisecond)
	if wh.calls.Load() != 1 {
		t.Errorf("expected 1 webhook call, got %d", wh.calls.Load())
	}
	if wh.url != srv.URL {
		t.Errorf("webhook called wrong URL: %s", wh.url)
	}
}

func TestService_Update_NoPendingCallbackOnNoURL(t *testing.T) {
	base := newTestTicket(ticket.StatusInProgress)
	// CallbackURL intentionally empty
	store := &mockStore{getTicket: base}
	wh := &mockWebhook{}
	svc := ticket.NewService(store, &mockPublisher{}, &mockNuclex{}, nil, wh, nil)

	completed := ticket.StatusCompleted
	_, err := svc.Update(context.Background(), base.ID, ticket.UpdateTicketReq{Status: &completed}, "test-actor")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	time.Sleep(50 * time.Millisecond)
	if wh.calls.Load() != 0 {
		t.Errorf("expected no webhook call when CallbackURL is empty, got %d", wh.calls.Load())
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

// --- mockMagicTokener for public_token tests ---

type mockMagicTokener struct {
	returnToken string
	returnErr   error
	callCount   int
	lastEmail   string
	lastTicket  *uuid.UUID
}

func (m *mockMagicTokener) Request(_ context.Context, email string, ticketID *uuid.UUID) (string, error) {
	m.callCount++
	m.lastEmail = email
	m.lastTicket = ticketID
	return m.returnToken, m.returnErr
}

// TestService_Create_PublicToken_WhenRequesterEmailPresent verifies that
// public_token is set on the returned ticket when metadata.requester_email is
// present and magic is non-nil.
func TestService_Create_PublicToken_WhenRequesterEmailPresent(t *testing.T) {
	store := &mockStore{}
	magic := &mockMagicTokener{returnToken: "ml_abc123"}
	svc := ticket.NewService(store, &mockPublisher{}, &mockNuclex{}, nil, nil, magic)

	req := ticket.CreateTicketReq{
		Title:    "helpdesk ticket",
		Metadata: map[string]any{"requester_email": "alice@example.com"},
	}
	created, err := svc.Create(context.Background(), req, "user1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if created.PublicToken != "ml_abc123" {
		t.Errorf("public_token = %q, want %q", created.PublicToken, "ml_abc123")
	}
	if magic.callCount != 1 {
		t.Errorf("magic.Request called %d times, want 1", magic.callCount)
	}
	if magic.lastEmail != "alice@example.com" {
		t.Errorf("magic.Request email = %q, want %q", magic.lastEmail, "alice@example.com")
	}
	if magic.lastTicket == nil || *magic.lastTicket != created.ID {
		t.Errorf("magic.Request ticketID mismatch")
	}
}

// TestService_Create_PublicToken_AbsentWhenNoEmail verifies that public_token
// is empty (and magic is not called) when requester_email is absent.
func TestService_Create_PublicToken_AbsentWhenNoEmail(t *testing.T) {
	store := &mockStore{}
	magic := &mockMagicTokener{returnToken: "ml_shouldnotappear"}
	svc := ticket.NewService(store, &mockPublisher{}, &mockNuclex{}, nil, nil, magic)

	req := ticket.CreateTicketReq{
		Title:    "no-email ticket",
		Metadata: map[string]any{"other_key": "value"},
	}
	created, err := svc.Create(context.Background(), req, "user1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if created.PublicToken != "" {
		t.Errorf("expected empty public_token when requester_email absent, got %q", created.PublicToken)
	}
	if magic.callCount != 0 {
		t.Errorf("magic.Request should not be called when email absent, got %d calls", magic.callCount)
	}
}

// TestService_Create_PublicToken_AbsentWhenMagicNil verifies that when the
// service is constructed with magic=nil, no public_token is set even if
// requester_email is in metadata.
func TestService_Create_PublicToken_AbsentWhenMagicNil(t *testing.T) {
	store := &mockStore{}
	svc := ticket.NewService(store, &mockPublisher{}, &mockNuclex{}, nil, nil, nil)

	req := ticket.CreateTicketReq{
		Title:    "no-magic ticket",
		Metadata: map[string]any{"requester_email": "bob@example.com"},
	}
	created, err := svc.Create(context.Background(), req, "user1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if created.PublicToken != "" {
		t.Errorf("expected empty public_token when magic is nil, got %q", created.PublicToken)
	}
}

func TestApprove_Approved(t *testing.T) {
	pending := &ticket.Ticket{
		ID:     uuid.New(),
		Status: ticket.StatusPendingApproval,
		Domain: ticket.DomainKnowledge,
	}
	store := &mockStore{getTicket: pending}
	pub := &mockPublisher{}
	nuclex := &mockNuclex{}
	svc := ticket.NewService(store, pub, nuclex, nil, nil, nil)

	got, err := svc.Approve(context.Background(), pending.ID, ticket.ApproveTicketReq{
		Decision:   ticket.ApproveDecisionApproved,
		OperatorID: "telegram_123",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.Status != ticket.StatusAssigned {
		t.Errorf("expected assigned, got %s", got.Status)
	}
	if pub.assigned != 1 {
		t.Errorf("expected PublishAssigned to be called once, got %d", pub.assigned)
	}
}

func TestApprove_Rejected(t *testing.T) {
	pending := &ticket.Ticket{ID: uuid.New(), Status: ticket.StatusPendingApproval}
	store := &mockStore{getTicket: pending}
	pub := &mockPublisher{}
	svc := ticket.NewService(store, pub, nil, nil, nil, nil)

	got, err := svc.Approve(context.Background(), pending.ID, ticket.ApproveTicketReq{
		Decision:   ticket.ApproveDecisionRejected,
		OperatorID: "telegram_123",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.Status != ticket.StatusCancelled {
		t.Errorf("expected cancelled, got %s", got.Status)
	}
}

func TestApprove_Revision(t *testing.T) {
	pending := &ticket.Ticket{ID: uuid.New(), Status: ticket.StatusPendingApproval}
	store := &mockStore{getTicket: pending}
	pub := &mockPublisher{}
	svc := ticket.NewService(store, pub, nil, nil, nil, nil)

	got, err := svc.Approve(context.Background(), pending.ID, ticket.ApproveTicketReq{
		Decision:     ticket.ApproveDecisionRevision,
		RevisionNote: "add more detail",
		OperatorID:   "telegram_123",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.Status != ticket.StatusPendingRevision {
		t.Errorf("expected pending_revision, got %s", got.Status)
	}
}

func TestApprove_WrongStatus(t *testing.T) {
	inProgress := &ticket.Ticket{ID: uuid.New(), Status: ticket.StatusInProgress}
	store := &mockStore{getTicket: inProgress}
	svc := ticket.NewService(store, &mockPublisher{}, nil, nil, nil, nil)

	_, err := svc.Approve(context.Background(), inProgress.ID, ticket.ApproveTicketReq{
		Decision: ticket.ApproveDecisionApproved,
	})
	if err == nil {
		t.Error("expected error for non-pending_approval ticket")
	}
}

// TestService_Create_PublicToken_NonFatalOnError verifies that a failure from
// magic.Request does not cause Create to return an error — ticket creation
// succeeds and public_token is simply empty.
func TestService_Create_PublicToken_NonFatalOnError(t *testing.T) {
	store := &mockStore{}
	magic := &mockMagicTokener{returnErr: errors.New("db down")}
	svc := ticket.NewService(store, &mockPublisher{}, &mockNuclex{}, nil, nil, magic)

	req := ticket.CreateTicketReq{
		Title:    "error ticket",
		Metadata: map[string]any{"requester_email": "carol@example.com"},
	}
	created, err := svc.Create(context.Background(), req, "user1")
	if err != nil {
		t.Fatalf("Create should succeed even when magic.Request fails, got: %v", err)
	}
	if created.PublicToken != "" {
		t.Errorf("public_token should be empty on magic error, got %q", created.PublicToken)
	}
	if magic.callCount != 1 {
		t.Errorf("magic.Request should be called once, got %d", magic.callCount)
	}
}

func TestService_Update_TitleChange(t *testing.T) {
	existing := newTestTicket(ticket.StatusPending)
	existing.Title = "old title"
	store := &mockStore{getTicket: existing}
	pub := &mockPublisher{}
	svc := ticket.NewService(store, pub, &mockNuclex{}, nil, nil, nil)

	newTitle := "new title"
	_, err := svc.Update(context.Background(), existing.ID, ticket.UpdateTicketReq{
		Title: &newTitle,
	}, "test-actor")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if store.updated == nil {
		t.Fatal("expected store.Update to be called")
	}
	if store.updated.Title == nil || *store.updated.Title != "new title" {
		t.Errorf("expected Title='new title', got %v", store.updated.Title)
	}
}

func TestService_Update_EmptyTitleRejected(t *testing.T) {
	existing := newTestTicket(ticket.StatusPending)
	store := &mockStore{getTicket: existing}
	pub := &mockPublisher{}
	svc := ticket.NewService(store, pub, &mockNuclex{}, nil, nil, nil)

	empty := ""
	_, err := svc.Update(context.Background(), existing.ID, ticket.UpdateTicketReq{
		Title: &empty,
	}, "test-actor")
	if err == nil {
		t.Fatal("expected error for empty title")
	}
}

func TestService_Create_TitleName(t *testing.T) {
	store := &mockStore{}
	svc := ticket.NewService(store, &mockPublisher{}, &mockNuclex{}, nil, nil, nil)

	created, err := svc.Create(context.Background(), ticket.CreateTicketReq{
		Title:     "vault issue",
		TitleName: "goq-vault-portforward",
	}, "user1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if created.TitleName != "goq-vault-portforward" {
		t.Errorf("expected TitleName='goq-vault-portforward', got %q", created.TitleName)
	}
}

func TestService_Create_InvalidTitleNameFormat(t *testing.T) {
	store := &mockStore{}
	svc := ticket.NewService(store, &mockPublisher{}, &mockNuclex{}, nil, nil, nil)

	cases := []string{
		"NoHyphen",
		"UPPER-case",
		"-leading-hyphen",
		"trailing-hyphen-",
		"double--hyphen",
	}
	for _, name := range cases {
		_, err := svc.Create(context.Background(), ticket.CreateTicketReq{
			Title:     "test",
			TitleName: name,
		}, "user1")
		if err == nil {
			t.Errorf("expected error for invalid title_name %q, got nil", name)
		}
	}
}

func TestService_Create_TitleNameOptional(t *testing.T) {
	store := &mockStore{}
	svc := ticket.NewService(store, &mockPublisher{}, &mockNuclex{}, nil, nil, nil)

	created, err := svc.Create(context.Background(), ticket.CreateTicketReq{
		Title: "no title_name ticket",
	}, "user1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if created.TitleName != "" {
		t.Errorf("expected empty TitleName, got %q", created.TitleName)
	}
}

func TestService_Update_TitleName(t *testing.T) {
	existing := newTestTicket(ticket.StatusPending)
	store := &mockStore{getTicket: existing}
	svc := ticket.NewService(store, &mockPublisher{}, &mockNuclex{}, nil, nil, nil)

	name := "goq-oom-restart"
	_, err := svc.Update(context.Background(), existing.ID, ticket.UpdateTicketReq{
		TitleName: &name,
	}, "test-actor")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if store.updated == nil || store.updated.TitleName == nil || *store.updated.TitleName != "goq-oom-restart" {
		t.Errorf("expected TitleName='goq-oom-restart' in store call, got %v", store.updated)
	}
}

func TestService_Update_InvalidTitleNameFormat(t *testing.T) {
	existing := newTestTicket(ticket.StatusPending)
	store := &mockStore{getTicket: existing}
	svc := ticket.NewService(store, &mockPublisher{}, &mockNuclex{}, nil, nil, nil)

	bad := "BadFormat"
	_, err := svc.Update(context.Background(), existing.ID, ticket.UpdateTicketReq{
		TitleName: &bad,
	}, "test-actor")
	if err == nil {
		t.Fatal("expected error for invalid title_name format")
	}
}

func TestService_Update_OrgAssignmentRequiresOrgID(t *testing.T) {
	existing := newTestTicket(ticket.StatusPending)
	store := &mockStore{getTicket: existing}
	svc := ticket.NewService(store, &mockPublisher{}, &mockNuclex{}, nil, nil, nil)

	at := ticket.AssigneeOrg
	_, err := svc.Update(context.Background(), existing.ID, ticket.UpdateTicketReq{
		AssigneeType: &at,
	}, "test-actor")
	if err == nil || !strings.Contains(err.Error(), "assignee_org_id required") {
		t.Fatalf("want assignee_org_id required error, got %v", err)
	}
}

// TestService_Update_ValidationErrorsWrapErrValidation covers
// goq-ticket-update-validation-500: every client-input validation failure in
// Update must satisfy errors.Is(err, ticket.ErrValidation) so the handler can
// map it to 400 instead of falling through to its catch-all 500.
func TestService_Update_ValidationErrorsWrapErrValidation(t *testing.T) {
	badTitleName := "BadFormat"
	emptyTitle := "   "
	badDomain := ticket.Domain("not-a-domain")
	badRiskTier := ticket.RiskTier("not-a-tier")
	orgType := ticket.AssigneeOrg

	cases := []struct {
		name string
		req  ticket.UpdateTicketReq
	}{
		{"empty title", ticket.UpdateTicketReq{Title: &emptyTitle}},
		{"bad title_name", ticket.UpdateTicketReq{TitleName: &badTitleName}},
		{"invalid domain", ticket.UpdateTicketReq{Domain: &badDomain}},
		{"invalid risk_tier", ticket.UpdateTicketReq{RiskTier: &badRiskTier}},
		{"org assignment missing org id", ticket.UpdateTicketReq{AssigneeType: &orgType}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			existing := newTestTicket(ticket.StatusPending)
			store := &mockStore{getTicket: existing}
			svc := ticket.NewService(store, &mockPublisher{}, &mockNuclex{}, nil, nil, nil)

			_, err := svc.Update(context.Background(), existing.ID, tc.req, "test-actor")
			if err == nil {
				t.Fatal("expected validation error")
			}
			if !errors.Is(err, ticket.ErrValidation) {
				t.Fatalf("err = %v, want errors.Is(err, ticket.ErrValidation)", err)
			}
		})
	}
}

func TestService_Create_DuplicateTitleName(t *testing.T) {
	store := &mockStore{createErr: ticket.ErrDuplicateTitleName}
	svc := ticket.NewService(store, &mockPublisher{}, &mockNuclex{}, nil, nil, nil)

	_, err := svc.Create(context.Background(), ticket.CreateTicketReq{
		Title:     "vault issue",
		TitleName: "goq-vault-portforward",
	}, "user1")
	if !errors.Is(err, ticket.ErrDuplicateTitleName) {
		t.Fatalf("expected ErrDuplicateTitleName, got %v", err)
	}
}

func TestCreate_EmitsNuclexCreated(t *testing.T) {
	store := &mockStore{}
	pub := &mockPublisher{}
	nux := &mockNuclex{}
	svc := ticket.NewService(store, pub, nux, nil, nil, nil)

	created, err := svc.Create(context.Background(), ticket.CreateTicketReq{
		Title: "test nuclex created",
		Type:  ticket.TypeTask,
	}, "user1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(nux.createdCalls) != 1 {
		t.Fatalf("expected 1 EmitCreated call, got %d", len(nux.createdCalls))
	}
	if nux.createdCalls[0].ID != created.ID {
		t.Errorf("EmitCreated called with ticket ID %s, want %s", nux.createdCalls[0].ID, created.ID)
	}
}
