package ticket

import (
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
)

type Status string

const (
	StatusPending         Status = "pending"
	StatusAssigned        Status = "assigned"
	StatusInProgress      Status = "in_progress"
	StatusCompleted       Status = "completed"
	StatusFailed          Status = "failed"
	StatusCancelled       Status = "cancelled"
	StatusPendingApproval Status = "pending_approval"
	StatusPendingRevision Status = "pending_revision"
	// StatusOnHold marks a ticket intentionally paused (goq-ticket-on-hold-status).
	// Non-terminal: does not count as "settled" for AllSubtasksTerminal (a paused
	// child must keep blocking its parent's auto-complete), but IS excluded from
	// ListOverdue so the SLA worker never auto-fails a ticket someone deliberately
	// held. The only allowed outgoing transition is back to StatusPending.
	StatusOnHold Status = "on_hold"
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
	// AssigneeOrg assigns the ticket to an external lymphhub organization; the
	// org's admin re-assigns internally (assignee_id then holds the member).
	AssigneeOrg AssigneeType = "org"
)

// RiskTier declares how much scrutiny a ticket's auto-completion warrants.
// medium/high tickets auto-completed by a merged PR are routed to
// pending_approval for human sign-off instead of straight to completed.
type RiskTier string

const (
	RiskLow    RiskTier = "low"
	RiskMedium RiskTier = "medium"
	RiskHigh   RiskTier = "high"
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
	ID           uuid.UUID    `json:"id"`
	TitleName    string       `json:"title_name,omitempty"`
	Title        string       `json:"title"`
	Description  string       `json:"description"`
	Type         TicketType   `json:"type"`
	Status       Status       `json:"status"`
	Priority     Priority     `json:"priority"`
	RiskTier     RiskTier     `json:"risk_tier"`
	AssigneeType AssigneeType `json:"assignee_type"`
	AssigneeID   string       `json:"assignee_id"`
	RequesterID  string       `json:"requester_id"`
	// Org reference axes (lymphhub org UUIDs; source of truth is external).
	RequesterOrgID *uuid.UUID     `json:"requester_org_id,omitempty"`
	AssigneeOrgID  *uuid.UUID     `json:"assignee_org_id,omitempty"`
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
	Refs           []string       `json:"ref_ids,omitempty"`
	// ChildCount is the number of immediate child tickets (one level only),
	// populated by store List/Get/ListSubtasks via a LEFT JOIN derived table.
	// Always set (defaults to 0). UI uses this to decide whether to render an
	// expander chevron.
	ChildCount int `json:"child_count"`
	// Denormalized read-only fields populated via JOIN in store queries.
	WorkspaceName string `json:"workspace_name,omitempty"`
	ProjectName   string `json:"project_name,omitempty"`
	ParentTitle   string `json:"parent_title,omitempty"`
	// CancellationNote is populated by contract-plane cascade_reject_ticket.
	CancellationNote *string `json:"cancellation_note,omitempty"`
	// PublicToken is a one-time magic-link token included only in the CreateTicket response.
	// It is not persisted in the tickets table; the underlying token lives in magic_tokens.
	PublicToken string     `json:"public_token,omitempty"`
	CreatedAt   time.Time  `json:"created_at"`
	UpdatedAt   time.Time  `json:"updated_at"`
	CompletedAt *time.Time `json:"completed_at,omitempty"`
}

// Tag is a workspace-scoped label attached to tickets.
type Tag struct {
	ID    uuid.UUID `json:"id"`
	Name  string    `json:"name"`
	Color string    `json:"color"`
}

type CreateTicketReq struct {
	TitleName    string       `json:"title_name,omitempty"`
	Title        string       `json:"title"`
	Description  string       `json:"description"`
	Type         TicketType   `json:"type"`
	Priority     Priority     `json:"priority"`
	RiskTier     RiskTier     `json:"risk_tier,omitempty"`
	AssigneeType AssigneeType `json:"assignee_type"`
	AssigneeID   string       `json:"assignee_id"`
	// RequesterOrgID is server-stamped only (from the caller's auth claims);
	// clients cannot set it via the request body.
	RequesterOrgID *uuid.UUID     `json:"-"`
	Domain         Domain         `json:"domain,omitempty"`
	AgentContext   map[string]any `json:"agent_context,omitempty"`
	SourceBot      string         `json:"source_bot,omitempty"`
	Metadata       map[string]any `json:"metadata,omitempty"`
	ProjectID      *uuid.UUID     `json:"project_id,omitempty"`
	WorkspaceSlug  string         `json:"workspace_slug,omitempty"`
	WorkspaceName  string         `json:"workspace_name,omitempty"`
	ProjectName    string         `json:"project_name,omitempty"`
	ParentID       *uuid.UUID     `json:"parent_id,omitempty"`
	Visibility     Visibility     `json:"visibility,omitempty"`
	CompletionMode CompletionMode `json:"completion_mode,omitempty"`
	CompletionSpec CompletionSpec `json:"completion_spec,omitempty"`
	RewardXP       int            `json:"reward_xp,omitempty"`
	DueAt          *time.Time     `json:"due_at,omitempty"`
	CallbackURL    string         `json:"callback_url,omitempty"`
	TagIDs         []uuid.UUID    `json:"tag_ids,omitempty"`
	TagNames       []string       `json:"tag_names,omitempty"`
	RefIDs         []string       `json:"ref_ids,omitempty"`
}

// BulkCreateSource identifies what produced a bulk-create request, for
// audit/debugging (goq-cogito-bulk-create-api). Not freeform: only the values
// below are accepted.
type BulkCreateSource string

const (
	BulkCreateSourceContentSplit          BulkCreateSource = "content-split"
	BulkCreateSourceQuestionDecomposition BulkCreateSource = "question-decomposition"
	BulkCreateSourceManual                BulkCreateSource = "manual"
)

// BulkCreateItem is one sub-ticket to create under BulkCreateReq.ParentID.
// type/domain/priority are not overridable per-item in v1 — they're inherited
// from the parent (see design doc §3.1); override support is a later addition.
type BulkCreateItem struct {
	Title       string `json:"title"`
	Description string `json:"description,omitempty"`
}

// BulkCreateReq batch-creates child tickets under ParentID in one call (the
// cogito content-split / question-decomposition consumer, §3.1 of
// 2026-08-02-cogito-subtask-generation-design.md). Each item is processed
// independently by Service.BulkCreate; a single bad item does not abort the
// rest (mirrors BulkUpdate's partial-failure contract).
type BulkCreateReq struct {
	ParentID  uuid.UUID        `json:"parent_id"`
	Items     []BulkCreateItem `json:"items"`
	Source    BulkCreateSource `json:"source"`
	SourceRef string           `json:"source_ref,omitempty"`
}

// BulkCreateResult reports the outcome of one BulkCreateReq.Items entry, keyed
// by its request index (items don't have an id until created). ID is a string
// (not uuid.UUID) so a failed item can omit it entirely rather than render the
// zero UUID (mirrors bulkUpdateResult's string ID in internal/handler).
type BulkCreateResult struct {
	Index int    `json:"index"`
	ID    string `json:"id,omitempty"`
	OK    bool   `json:"ok"`
	Error string `json:"error,omitempty"`
}

// BulkCreateResp is the Service.BulkCreate response shape: a per-item result
// list (partial failure allowed) plus a count of tickets actually created.
type BulkCreateResp struct {
	Results []BulkCreateResult `json:"results"`
	Created int                `json:"created"`
}

type UpdateTicketReq struct {
	TitleName     *string       `json:"title_name,omitempty"`
	Title         *string       `json:"title,omitempty"`
	Description   *string       `json:"description,omitempty"`
	Status        *Status       `json:"status,omitempty"`
	RiskTier      *RiskTier     `json:"risk_tier,omitempty"`
	AssigneeID    *string       `json:"assignee_id,omitempty"`
	AssigneeType  *AssigneeType `json:"assignee_type,omitempty"`
	AssigneeOrgID *uuid.UUID    `json:"assignee_org_id,omitempty"`
	Domain        *Domain       `json:"domain,omitempty"`
	// ProjectID moves the ticket to another project. The target must exist and
	// live in the same workspace as the current project (tags, title_name
	// uniqueness and visibility are all workspace/project-scoped).
	ProjectID      *uuid.UUID      `json:"project_id,omitempty"`
	AgentContext   map[string]any  `json:"agent_context,omitempty"`
	Metadata       map[string]any  `json:"metadata,omitempty"`
	Visibility     *Visibility     `json:"visibility,omitempty"`
	CompletionMode *CompletionMode `json:"completion_mode,omitempty"`
	CompletionSpec CompletionSpec  `json:"completion_spec,omitempty"`
	RewardXP       *int            `json:"reward_xp,omitempty"`
	DueAt          *time.Time      `json:"due_at,omitempty"`
	CallbackURL    *string         `json:"callback_url,omitempty"`
	TagIDs         []uuid.UUID     `json:"tag_ids,omitempty"`
	// RefIDs replaces the ticket's full ref-doc set on Update.
	// Semantics: nil (field omitted) = leave refs untouched;
	// &[]string{} = clear all refs; &[]string{...} = replace with that ordered set.
	RefIDs           *[]string `json:"ref_ids,omitempty"`
	CancellationNote *string   `json:"cancellation_note,omitempty"`
}

// VisibleTicketIDs carries the caller's SpiceDB-visible ticket ids, split by the
// permission that granted them. Set only under enforce; nil means "no visibility
// restriction" (shadow). Honors per-ticket visibility: project-visibility tickets
// must be in Project, restricted ones in Restricted.
type VisibleTicketIDs struct {
	Project    []uuid.UUID
	Restricted []uuid.UUID
}

// OrgScope restricts a listing to what an external-org caller may see,
// derived from JWT org claims (never from client input). Role semantics:
// admin → every ticket where the org is requester or assignee side;
// member → only tickets the caller personally requested / is assigned.
type OrgScope struct {
	OrgID    uuid.UUID
	Role     string // "admin" | "member"
	CallerID string
	Side     string // "" | "requester" | "assignee"
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
	TopLevelOnly bool
	TagIDs       []uuid.UUID
	TitleName    *string
	// Query is a free-text search term matched (case-insensitive substring)
	// against title, title_name, and description. Empty/nil → no text filter.
	Query         *string
	Limit         int
	Offset        int
	VisibleFilter *VisibleTicketIDs
	// OrgScope, when non-nil, restricts the listing to what an external-org
	// caller may see (populated only from JWT org claims, never client input).
	OrgScope *OrgScope
	// SortField is one of: created_at, updated_at, due_at, priority, title,
	// status, type, domain, assignee_id. Empty → created_at.
	SortField string
	// SortOrder is "asc" or "desc". Empty → desc.
	SortOrder string
}

// AggBucket is one row of a group-by aggregation: the group key and its count.
// An empty Key represents rows where the grouped column is NULL/empty.
type AggBucket struct {
	Key   string `json:"key"`
	Count int    `json:"count"`
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
	ApproveDecisionApproved ApproveDecision = "approved"
	ApproveDecisionRejected ApproveDecision = "rejected"
	ApproveDecisionRevision ApproveDecision = "revision"
)

type ApproveTicketReq struct {
	Decision     ApproveDecision `json:"decision"`
	RevisionNote string          `json:"revision_note,omitempty"`
	OperatorID   string          `json:"operator_id"`
}

type TicketRef struct {
	TicketID    uuid.UUID `json:"ticket_id"`
	GopediaL1ID string    `json:"gopedia_l1_id"`
	Position    int       `json:"position"`
	CreatedAt   time.Time `json:"created_at"`
	CreatedBy   string    `json:"created_by"`
}

const (
	MaxRefsPerTicket = 50
	MaxRefIDLength   = 512
)

var (
	ErrRefConflict      = errors.New("ref already exists for this ticket")
	ErrRefLimitExceeded = errors.New("ref count exceeds MaxRefsPerTicket")
	ErrRefIDInvalid     = errors.New("gopedia_l1_id must be 1..512 chars")
	ErrRefIDsDuplicate  = errors.New("duplicate gopedia_l1_id in request")
)

// ErrValidation wraps application-level client-input validation failures
// (bad title/title_name/domain/risk_tier/org-assignment fields) so handlers
// can map them to 400 with errors.Is, instead of falling through to their
// catch-all 500 (goq-ticket-update-validation-500).
var ErrValidation = errors.New("validation failed")

type TicketRelation struct {
	ID           uuid.UUID `json:"id"`
	FromTicketID uuid.UUID `json:"from_ticket_id"`
	ToTicketID   uuid.UUID `json:"to_ticket_id"`
	RelationType string    `json:"relation_type"`
	CreatedAt    time.Time `json:"created_at"`
	CreatedBy    string    `json:"created_by"`
}

var (
	ErrRelationSelfReference = errors.New("from_ticket_id and to_ticket_id must differ")
	ErrRelationTypeInvalid   = errors.New("relation_type must be blocks, depends_on, or duplicates")
)

var ValidRelationTypes = map[string]bool{"blocks": true, "depends_on": true, "duplicates": true}
