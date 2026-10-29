// Package approval holds append-only approval records (many per ticket).
// Design: geneso goquest/spec/2026-10-29-goquest-approval-records-design.md.
package approval

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
)

type Kind string

const (
	KindDesign    Kind = "design"
	KindMerge     Kind = "merge"
	KindInfra     Kind = "infra"
	KindOps       Kind = "ops"
	KindDeviation Kind = "deviation"
	// KindTicket records the legacy POST /tickets/{id}/approve sign-off.
	KindTicket Kind = "ticket"
)

func (k Kind) Valid() bool {
	switch k {
	case KindDesign, KindMerge, KindInfra, KindOps, KindDeviation, KindTicket:
		return true
	}
	return false
}

type Status string

const (
	StatusPending           Status = "pending"
	StatusApproved          Status = "approved"
	StatusRejected          Status = "rejected"
	StatusRevisionRequested Status = "revision_requested"
	StatusWithdrawn         Status = "withdrawn"
	StatusSuperseded        Status = "superseded"
)

type Basis string

const (
	BasisUserDirect   Basis = "user_direct"
	BasisDeskDelegate Basis = "desk_delegated"
)

var (
	ErrNotFound          = errors.New("approval not found")
	ErrInvalidTransition = errors.New("invalid approval status transition")
	ErrInvalidSubject    = errors.New("invalid approval subject")
	// ErrStale means the compare-and-set status guard did not match: someone
	// else moved the approval first.
	ErrStale = errors.New("approval status changed concurrently")
)

// transitions is the allowed status graph. rejected, withdrawn and superseded
// are terminal; revision_requested is followed by a new record that supersedes
// it (spec 3.1).
var transitions = map[Status][]Status{
	StatusPending:           {StatusApproved, StatusRejected, StatusRevisionRequested, StatusWithdrawn, StatusSuperseded},
	StatusApproved:          {StatusSuperseded},
	StatusRevisionRequested: {StatusSuperseded},
}

// CanTransition reports whether from -> to is allowed.
func CanTransition(from, to Status) bool {
	for _, s := range transitions[from] {
		if s == to {
			return true
		}
	}
	return false
}

// Terminal reports whether no further transition is possible.
func (s Status) Terminal() bool { return len(transitions[s]) == 0 }

// Subject identifies what is being approved.
type Subject struct {
	Repo    string `json:"repo,omitempty"`
	PR      int    `json:"pr,omitempty"`
	HeadSHA string `json:"head_sha,omitempty"`
	Doc     string `json:"doc,omitempty"`
	DocSHA  string `json:"doc_sha,omitempty"`
	Target  string `json:"target,omitempty"`
	Ticket  string `json:"ticket,omitempty"`
}

// Validate enforces the per-kind required fields (spec 3.2).
func (s Subject) Validate(kind Kind, withinDirection *bool) error {
	bad := func(msg string) error { return fmt.Errorf("%w: %s", ErrInvalidSubject, msg) }
	switch kind {
	case KindMerge:
		if s.Repo == "" || s.PR <= 0 || s.HeadSHA == "" {
			return bad("merge requires repo, pr and head_sha")
		}
	case KindDesign:
		if s.Doc == "" {
			return bad("design requires doc")
		}
	case KindInfra, KindOps:
		if (s.Repo == "" || s.PR <= 0) && s.Doc == "" && s.Target == "" {
			return bad(string(kind) + " requires repo+pr, doc or target")
		}
	case KindDeviation:
		if s.Doc == "" {
			return bad("deviation requires doc")
		}
		if withinDirection == nil {
			return bad("deviation requires within_direction")
		}
	case KindTicket:
		if s.Ticket == "" {
			return bad("ticket requires ticket")
		}
	default:
		return bad("unknown kind")
	}
	return nil
}

// Delegable reports whether a kind may ever be decided with basis
// desk_delegated. The fixed ceiling from spec 4.3 / user decision 2026-10-29:
// only merge and a deviation that stays within the design direction.
func Delegable(kind Kind, withinDirection *bool) bool {
	switch kind {
	case KindMerge:
		return true
	case KindDeviation:
		return withinDirection != nil && *withinDirection
	}
	return false
}

// EvidenceItem is one supporting reference (pr, ci_run, test, doc, comment).
type EvidenceItem struct {
	Type string `json:"type"`
	Ref  string `json:"ref"`
	Note string `json:"note,omitempty"`
}

type Approval struct {
	ID               uuid.UUID      `json:"id"`
	TicketID         uuid.UUID      `json:"ticket_id"`
	Kind             Kind           `json:"kind"`
	Subject          Subject        `json:"subject"`
	Status           Status         `json:"status"`
	RequestedBy      string         `json:"requested_by"`
	RequestedByLabel string         `json:"requested_by_label,omitempty"`
	RequestedAt      time.Time      `json:"requested_at"`
	RequestNote      string         `json:"request_note,omitempty"`
	Evidence         []EvidenceItem `json:"evidence"`
	WithinDirection  *bool          `json:"within_direction,omitempty"`
	DecidedBy        *string        `json:"decided_by,omitempty"`
	DecidedAt        *time.Time     `json:"decided_at,omitempty"`
	DecisionNote     *string        `json:"decision_note,omitempty"`
	Basis            *Basis         `json:"basis,omitempty"`
	DelegationRuleID *uuid.UUID     `json:"delegation_rule_id,omitempty"`
	UserRef          *string        `json:"user_ref,omitempty"`
	SupersedesID     *uuid.UUID     `json:"supersedes_id,omitempty"`
	Legacy           bool           `json:"legacy,omitempty"`
	SourceCommentID  *uuid.UUID     `json:"source_comment_id,omitempty"`
	CreatedAt        time.Time      `json:"created_at"`
	Events           []Event        `json:"events,omitempty"`
}

// Event is one status transition, kept forever.
type Event struct {
	ID         uuid.UUID `json:"id"`
	ApprovalID uuid.UUID `json:"approval_id"`
	From       Status    `json:"from"`
	To         Status    `json:"to"`
	Actor      string    `json:"actor"`
	Note       string    `json:"note,omitempty"`
	CreatedAt  time.Time `json:"created_at"`
}

// Decision carries the fields written when a pending approval is decided.
// DecidedBy and Basis are always server-derived by the caller, never taken
// from a request body.
type Decision struct {
	Status           Status
	DecidedBy        string
	Basis            Basis
	DelegationRuleID *uuid.UUID
	UserRef          string
	Note             string
}

// NewReq is the input to Store.Create. RequestedBy is server-derived.
type NewReq struct {
	TicketID         uuid.UUID
	Kind             Kind
	Subject          Subject
	RequestedBy      string
	RequestedByLabel string
	RequestNote      string
	Evidence         []EvidenceItem
	WithinDirection  *bool
	SupersedesID     *uuid.UUID
	Legacy           bool
	SourceCommentID  *uuid.UUID
}

func (r NewReq) Validate() error {
	if !r.Kind.Valid() {
		return fmt.Errorf("%w: unknown kind %q", ErrInvalidSubject, r.Kind)
	}
	if strings.TrimSpace(r.RequestedBy) == "" {
		return errors.New("requested_by is required")
	}
	return r.Subject.Validate(r.Kind, r.WithinDirection)
}
