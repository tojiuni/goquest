package ticket

import (
	"time"

	"github.com/google/uuid"
)

type Status string

const (
	StatusPending    Status = "pending"
	StatusAssigned   Status = "assigned"
	StatusInProgress Status = "in_progress"
	StatusCompleted  Status = "completed"
	StatusFailed     Status = "failed"
	StatusCancelled  Status = "cancelled"
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

type Ticket struct {
	ID            uuid.UUID      `json:"id"`
	Title         string         `json:"title"`
	Description   string         `json:"description"`
	Type          TicketType     `json:"type"`
	Status        Status         `json:"status"`
	Priority      Priority       `json:"priority"`
	AssigneeType  AssigneeType   `json:"assignee_type"`
	AssigneeID    string         `json:"assignee_id"`
	RequesterID   string         `json:"requester_id"`
	SourceBot     string         `json:"source_bot,omitempty"`
	Domain        Domain         `json:"domain,omitempty"`
	AgentContext  map[string]any `json:"agent_context,omitempty"`
	VikunjaTaskID *int64         `json:"vikunja_task_id,omitempty"`
	Metadata      map[string]any `json:"metadata,omitempty"`
	CreatedAt     time.Time      `json:"created_at"`
	UpdatedAt     time.Time      `json:"updated_at"`
	CompletedAt   *time.Time     `json:"completed_at,omitempty"`
}

type CreateTicketReq struct {
	Title         string         `json:"title"`
	Description   string         `json:"description"`
	Type          TicketType     `json:"type"`
	Priority      Priority       `json:"priority"`
	AssigneeType  AssigneeType   `json:"assignee_type"`
	AssigneeID    string         `json:"assignee_id"`
	Domain        Domain         `json:"domain,omitempty"`
	AgentContext  map[string]any `json:"agent_context,omitempty"`
	SourceBot     string         `json:"source_bot,omitempty"`
	VikunjaTaskID *int64         `json:"vikunja_task_id,omitempty"`
	Metadata      map[string]any `json:"metadata,omitempty"`
}

type UpdateTicketReq struct {
	Status        *Status        `json:"status,omitempty"`
	AssigneeID    *string        `json:"assignee_id,omitempty"`
	AssigneeType  *AssigneeType  `json:"assignee_type,omitempty"`
	Domain        *Domain        `json:"domain,omitempty"`
	AgentContext  map[string]any `json:"agent_context,omitempty"`
	VikunjaTaskID *int64         `json:"vikunja_task_id,omitempty"`
}

type ListTicketsFilter struct {
	Status       *Status
	Domain       *Domain
	AssigneeType *AssigneeType
	AssigneeID   *string
	RequesterID  *string
	Limit        int
	Offset       int
}
