package ticket

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

type Status string

const (
	StatusPending          Status = "pending"
	StatusAssigned         Status = "assigned"
	StatusInProgress       Status = "in_progress"
	StatusCompleted        Status = "completed"
	StatusFailed           Status = "failed"
	StatusCancelled        Status = "cancelled"
	StatusPendingApproval  Status = "pending_approval"
	StatusPendingRevision  Status = "pending_revision"
)

type TicketType string

const (
	TypeTask     TicketType = "task"
	TypeBug      TicketType = "bug"
	TypeFeature  TicketType = "feature"
	TypeIncident TicketType = "incident"
	TypeRequest  TicketType = "request"
)

type Priority string

const (
	PriorityUrgent Priority = "urgent"
	PriorityHigh   Priority = "high"
	PriorityNormal Priority = "normal"
	PriorityLow    Priority = "low"
)

type AssigneeType string

const (
	AssigneeHuman AssigneeType = "human"
	AssigneeAgent AssigneeType = "agent"
)

// Domain maps to hermes-{domain} jobs
type Domain string

const (
	DomainInfra     Domain = "infra"
	DomainData      Domain = "data"
	DomainCode      Domain = "code"
	DomainMonitor   Domain = "monitor"
	DomainKnowledge Domain = "knowledge"
	DomainPM        Domain = "pm"
)

// Visibility controls who can see a ticket beyond RBAC.
type Visibility string

const (
	VisibilityProject    Visibility = "project"
	VisibilityRestricted Visibility = "restricted"
)

// CompletionMode defines how a ticket is marked completed.
type CompletionMode string

const (
	CompletionManual  CompletionMode = "manual"
	CompletionAnswer  CompletionMode = "answer"
	CompletionAgent   CompletionMode = "agent"
	CompletionSubtask CompletionMode = "subtask"
)

// CompletionSpec is the JSONB payload for answer/agent modes.
type CompletionSpec map[string]any

func (c CompletionSpec) MarshalJSON() ([]byte, error) {
	if c == nil {
		return []byte("{}"), nil
	}
	return json.Marshal(map[string]any(c))
}

type Ticket struct {
	ID             uuid.UUID      `json:"id"`
	TitleName      string         `json:"title_name,omitempty"`
	Title          string         `json:"title"`
	Description    string         `json:"description"`
	Type           TicketType     `json:"type"`
	Status         Status         `json:"status"`
	Priority       Priority       `json:"priority"`
	AssigneeType   AssigneeType   `json:"assignee_type"`
	AssigneeID     string         `json:"assignee_id"`
	RequesterID    string         `json:"requester_id"`
	SourceBot      string         `json:"source_bot,omitempty"`
	Domain         Domain         `json:"domain,omitempty"`
	AgentContext   map[string]any `json:"agent_context,omitempty"`
	Metadata       map[string]any `json:"metadata,omitempty"`
	// Phase 0: multi-tenancy + game quest fields
	ProjectID      *uuid.UUID     `json:"project_id,omitempty"`
	ParentID       *uuid.UUID     `json:"parent_id,omitempty"`
	Visibility     Visibility     `json:"visibility"`
	CompletionMode CompletionMode `json:"completion_mode"`
	CompletionSpec CompletionSpec `json:"completion_spec,omitempty"`
	RewardXP       int            `json:"reward_xp"`
	DueAt          *time.Time     `json:"due_at,omitempty"`
	CallbackURL    string         `json:"callback_url,omitempty"`
	Tags           []Tag          `json:"tags,omitempty"`
	// Denormalized read-only fields populated via JOIN in store queries.
	WorkspaceName  string         `json:"workspace_name,omitempty"`
	ProjectName    string         `json:"project_name,omitempty"`
	// PublicToken is a one-time magic-link token included only in the CreateTicket response.
	// It is not persisted in the tickets table; the underlying token lives in magic_tokens.
	PublicToken    string         `json:"public_token,omitempty"`
	CreatedAt      time.Time      `json:"created_at"`
	UpdatedAt      time.Time      `json:"updated_at"`
	CompletedAt    *time.Time     `json:"completed_at,omitempty"`
}

// Tag is a workspace-scoped label attached to tickets.
type Tag struct {
	ID    uuid.UUID `json:"id"`
	Name  string    `json:"name"`
	Color string    `json:"color"`
}

type CreateTicketReq struct {
	TitleName      string         `json:"title_name,omitempty"`
	Title          string         `json:"title"`
	Description    string         `json:"description"`
	Type           TicketType     `json:"type"`
	Priority       Priority       `json:"priority"`
	AssigneeType   AssigneeType   `json:"assignee_type"`
	AssigneeID     string         `json:"assignee_id"`
	Domain         Domain         `json:"domain,omitempty"`
	AgentContext   map[string]any `json:"agent_context,omitempty"`
	SourceBot      string         `json:"source_bot,omitempty"`
	Metadata       map[string]any `json:"metadata,omitempty"`
	ProjectID      *uuid.UUID     `json:"project_id,omitempty"`
	ParentID       *uuid.UUID     `json:"parent_id,omitempty"`
	Visibility     Visibility     `json:"visibility,omitempty"`
	CompletionMode CompletionMode `json:"completion_mode,omitempty"`
	CompletionSpec CompletionSpec `json:"completion_spec,omitempty"`
	RewardXP       int            `json:"reward_xp,omitempty"`
	DueAt          *time.Time     `json:"due_at,omitempty"`
	CallbackURL    string         `json:"callback_url,omitempty"`
	TagIDs         []uuid.UUID    `json:"tag_ids,omitempty"`
}

type UpdateTicketReq struct {
	TitleName      *string         `json:"title_name,omitempty"`
	Title          *string         `json:"title,omitempty"`
	Description    *string         `json:"description,omitempty"`
	Status         *Status         `json:"status,omitempty"`
	AssigneeID     *string         `json:"assignee_id,omitempty"`
	AssigneeType   *AssigneeType   `json:"assignee_type,omitempty"`
	Domain         *Domain         `json:"domain,omitempty"`
	AgentContext   map[string]any  `json:"agent_context,omitempty"`
	Metadata       map[string]any  `json:"metadata,omitempty"`
	Visibility     *Visibility     `json:"visibility,omitempty"`
	CompletionMode *CompletionMode `json:"completion_mode,omitempty"`
	CompletionSpec CompletionSpec  `json:"completion_spec,omitempty"`
	RewardXP       *int            `json:"reward_xp,omitempty"`
	DueAt          *time.Time      `json:"due_at,omitempty"`
	CallbackURL    *string         `json:"callback_url,omitempty"`
	TagIDs         []uuid.UUID     `json:"tag_ids,omitempty"`
}

type ListTicketsFilter struct {
	Status       *Status
	Type         *TicketType
	Priority     *Priority
	Domain       *Domain
	AssigneeType *AssigneeType
	AssigneeID   *string
	RequesterID  *string
	ProjectID    *uuid.UUID
	WorkspaceID  *uuid.UUID
	ParentID     *uuid.UUID
	TagIDs       []uuid.UUID
	Limit        int
	Offset       int
}

// Attempt records a user's answer submission for answer-mode tickets.
type Attempt struct {
	ID          uuid.UUID `json:"id"`
	TicketID    uuid.UUID `json:"ticket_id"`
	UserID      string    `json:"user_id"`
	Answer      string    `json:"answer"`
	IsCorrect   *bool     `json:"is_correct"`
	AIFeedback  string    `json:"ai_feedback,omitempty"`
	AttemptedAt time.Time `json:"attempted_at"`
}

type SubmitAttemptReq struct {
	Answer string `json:"answer"`
}

// AnswerSpec is the parsed completion_spec for answer-mode tickets.
type AnswerSpec struct {
	Question      string   `json:"question"`
	AnswerType    string   `json:"answer_type"` // "exact" | "choice" | "ai_eval"
	CorrectAnswer string   `json:"correct_answer,omitempty"`
	Choices       []string `json:"choices,omitempty"`
	MaxAttempts   int      `json:"max_attempts"`
	Hint          string   `json:"hint,omitempty"`
	AIAgentID     string   `json:"ai_agent_id,omitempty"`
}

// IsTerminal returns true for statuses that allow no further transitions.
func (s Status) IsTerminal() bool {
	return s == StatusCompleted || s == StatusCancelled || s == StatusFailed
}

type ApproveDecision string

const (
	ApproveDecisionApproved  ApproveDecision = "approved"
	ApproveDecisionRejected  ApproveDecision = "rejected"
	ApproveDecisionRevision  ApproveDecision = "revision"
)

type ApproveTicketReq struct {
	Decision     ApproveDecision `json:"decision"`
	RevisionNote string          `json:"revision_note,omitempty"`
	OperatorID   string          `json:"operator_id"`
}
