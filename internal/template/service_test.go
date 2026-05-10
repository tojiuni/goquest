package template_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/tojiuni/goquest/internal/template"
	"github.com/tojiuni/goquest/internal/ticket"
)

// --- mock store ---

type mockStore struct {
	created   *template.Template
	createErr error
	templates []*template.Template
	found     *template.Template
	getErr    error
}

func (m *mockStore) Create(_ context.Context, t *template.Template) error {
	if m.createErr != nil {
		return m.createErr
	}
	m.created = t
	return nil
}
func (m *mockStore) List(_ context.Context, _ uuid.UUID) ([]*template.Template, error) {
	return m.templates, nil
}
func (m *mockStore) Get(_ context.Context, _ uuid.UUID) (*template.Template, error) {
	return m.found, m.getErr
}
func (m *mockStore) Delete(_ context.Context, _ uuid.UUID) error { return nil }

// --- tests ---

func TestService_Create_RequiresName(t *testing.T) {
	svc := template.NewService(&mockStore{})
	_, err := svc.Create(context.Background(), uuid.New(), template.CreateReq{Title: "t"}, "u1")
	if err == nil {
		t.Fatal("expected error for missing name")
	}
}

func TestService_Create_RequiresTitle(t *testing.T) {
	svc := template.NewService(&mockStore{})
	_, err := svc.Create(context.Background(), uuid.New(), template.CreateReq{Name: "n"}, "u1")
	if err == nil {
		t.Fatal("expected error for missing title")
	}
}

func TestService_Create_AppliesDefaults(t *testing.T) {
	store := &mockStore{}
	svc := template.NewService(store)

	tmpl, err := svc.Create(context.Background(), uuid.New(), template.CreateReq{
		Name: "Bug Report", Title: "Bug: {{description}}",
	}, "u1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if tmpl.CompletionMode != ticket.CompletionManual {
		t.Errorf("completion_mode = %q, want manual", tmpl.CompletionMode)
	}
	if tmpl.Type != ticket.TypeTask {
		t.Errorf("type = %q, want task", tmpl.Type)
	}
	if tmpl.Priority != ticket.PriorityNormal {
		t.Errorf("priority = %q, want normal", tmpl.Priority)
	}
}

func TestService_Create_StoresRecord(t *testing.T) {
	store := &mockStore{}
	svc := template.NewService(store)
	pid := uuid.New()

	tmpl, err := svc.Create(context.Background(), pid, template.CreateReq{
		Name: "Sprint Task", Title: "Task: implement {{feature}}",
	}, "user1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if store.created == nil {
		t.Fatal("expected store.Create to be called")
	}
	if tmpl.ProjectID != pid {
		t.Errorf("project_id mismatch")
	}
}

func TestService_ToTicketReq_DefaultsFromTemplate(t *testing.T) {
	svc := template.NewService(&mockStore{})
	pid := uuid.New()
	tmpl := &template.Template{
		ID:             uuid.New(),
		ProjectID:      pid,
		Title:          "Deploy {{service}}",
		Description:    "standard deploy flow",
		Type:           ticket.TypeTask,
		Priority:       ticket.PriorityHigh,
		CompletionMode: ticket.CompletionManual,
		RewardXP:       50,
		CreatedAt:      time.Now(),
	}

	req := svc.ToTicketReq(tmpl, ticket.CreateTicketReq{})
	if req.Title != tmpl.Title {
		t.Errorf("title not copied from template")
	}
	if req.RewardXP != 50 {
		t.Errorf("reward_xp not copied")
	}
	if req.ProjectID == nil || *req.ProjectID != pid {
		t.Errorf("project_id not set from template")
	}
}

func TestService_ToTicketReq_OverrideTitle(t *testing.T) {
	svc := template.NewService(&mockStore{})
	tmpl := &template.Template{
		ID: uuid.New(), Title: "template title",
		CompletionMode: ticket.CompletionManual,
	}
	req := svc.ToTicketReq(tmpl, ticket.CreateTicketReq{Title: "custom title"})
	if req.Title != "custom title" {
		t.Errorf("expected override title, got %q", req.Title)
	}
}

func TestService_List(t *testing.T) {
	store := &mockStore{
		templates: []*template.Template{
			{ID: uuid.New(), Name: "t1"},
			{ID: uuid.New(), Name: "t2"},
		},
	}
	svc := template.NewService(store)
	got, err := svc.List(context.Background(), uuid.New())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 2 {
		t.Errorf("expected 2, got %d", len(got))
	}
}
