package approval

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
)

const (
	maxNoteLen     = 2000
	maxEvidence    = 20
	maxLabelLen    = 64
	maxEvidenceLen = 500
)

var (
	// ErrForbidden means the caller may not perform this action on the approval.
	ErrForbidden = errors.New("not allowed")
	// ErrInvalidInput is a request the caller can fix (bad kind, oversized field).
	ErrInvalidInput = errors.New("invalid approval request")
)

type Service struct {
	store Store
	rules RuleStore
}

func NewService(store Store) *Service { return &Service{store: store} }

// RequestInput is what a caller may send when asking for an approval. The
// requester identity is never part of it; it comes from the authenticated
// principal.
type RequestInput struct {
	Kind            Kind
	Subject         Subject
	RequestNote     string
	Evidence        []EvidenceItem
	WithinDirection *bool
}

// Request records a pending approval on ticketID. principal is the
// authenticated requester; label is the optional, unverified session label.
func (s *Service) Request(ctx context.Context, ticketID uuid.UUID, in RequestInput, principal, label string) (*Approval, error) {
	if strings.TrimSpace(principal) == "" {
		return nil, fmt.Errorf("%w: requester identity required", ErrForbidden)
	}
	if in.Kind == KindTicket {
		// kind=ticket is written only by the legacy /approve bridge (plan P5).
		return nil, fmt.Errorf("%w: kind %q is reserved", ErrInvalidInput, in.Kind)
	}
	if len(in.RequestNote) > maxNoteLen {
		return nil, fmt.Errorf("%w: request_note too long", ErrInvalidInput)
	}
	if len(in.Evidence) > maxEvidence {
		return nil, fmt.Errorf("%w: too many evidence items", ErrInvalidInput)
	}
	for _, e := range in.Evidence {
		if strings.TrimSpace(e.Type) == "" || strings.TrimSpace(e.Ref) == "" || len(e.Ref) > maxEvidenceLen || len(e.Note) > maxEvidenceLen {
			return nil, fmt.Errorf("%w: evidence needs type and ref (max %d chars)", ErrInvalidInput, maxEvidenceLen)
		}
	}
	wd := in.WithinDirection
	if in.Kind != KindDeviation {
		wd = nil // only meaningful for deviation
	}
	if len(label) > maxLabelLen {
		label = label[:maxLabelLen]
	}
	return s.store.Create(ctx, NewReq{
		TicketID: ticketID, Kind: in.Kind, Subject: in.Subject,
		RequestedBy: principal, RequestedByLabel: label,
		RequestNote: in.RequestNote, Evidence: in.Evidence, WithinDirection: wd,
	})
}

// ListForTicket returns the ticket's approvals, newest first.
func (s *Service) ListForTicket(ctx context.Context, ticketID uuid.UUID, status Status, limit, offset int) ([]*Approval, error) {
	return s.store.List(ctx, ListFilter{TicketID: &ticketID, Status: status, Limit: limit, Offset: offset})
}

// GetForTicket returns the approval only if it belongs to ticketID, so a
// caller authorised for one ticket cannot read another ticket's approvals by id.
func (s *Service) GetForTicket(ctx context.Context, ticketID, id uuid.UUID) (*Approval, error) {
	a, err := s.store.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	if a.TicketID != ticketID {
		return nil, ErrNotFound
	}
	return a, nil
}

// Withdraw lets the requester pull back a pending approval.
func (s *Service) Withdraw(ctx context.Context, ticketID, id uuid.UUID, principal, note string) (*Approval, error) {
	a, err := s.GetForTicket(ctx, ticketID, id)
	if err != nil {
		return nil, err
	}
	if principal == "" || a.RequestedBy != principal {
		return nil, fmt.Errorf("%w: only the requester can withdraw", ErrForbidden)
	}
	if len(note) > maxNoteLen {
		return nil, fmt.Errorf("%w: note too long", ErrInvalidInput)
	}
	return s.store.Transition(ctx, id, a.Status, StatusWithdrawn, principal, note, nil)
}

// ActorKind is how the caller authenticated, as classified by the auth layer.
type ActorKind string

const (
	ActorHuman   ActorKind = "human"
	ActorSession ActorKind = "session"
	ActorOther   ActorKind = "other"
)

// Actor is an authenticated caller. ID is the server-derived principal.
type Actor struct {
	ID   string
	Kind ActorKind
}

// DenyReason says why a decision was refused, for the API and the audit log.
type DenyReason string

const (
	ReasonPrincipalCannotDecide DenyReason = "principal_cannot_decide"
	ReasonKindNotDelegable      DenyReason = "kind_not_delegable"
	ReasonNoMatchingRule        DenyReason = "no_matching_rule"
	ReasonSelfApproval          DenyReason = "self_approval"
)

// DeniedError is returned when the caller is not allowed to decide this approval.
type DeniedError struct{ Reason DenyReason }

func (e *DeniedError) Error() string { return "decision denied: " + string(e.Reason) }

// DecideInput is a decision request. Who decides and on what basis is never an
// input: both come from the Actor and the stored delegation rules.
type DecideInput struct {
	TicketID    uuid.UUID
	WorkspaceID uuid.UUID // uuid.Nil for a ticket without a project
	ApprovalID  uuid.UUID
	Decision    Status
	Note        string
	UserRef     string // required for desk_delegated: where the user's go-ahead is recorded
}

// CreateRuleInput is a request to delegate. Only a person may create one.
type CreateRuleInput struct {
	Principal  string
	Kinds      []Kind
	Repos      []string
	Conditions map[string]any
	UserRef    string
	ExpiresAt  *time.Time
}

// knownConditions are the only condition keys the matcher understands; the
// API refuses others so a stored rule never means more than it says.
var knownConditions = map[string]bool{"require_evidence_types": true}

// CreateRule stores a delegation rule for the workspace. creator must be a
// person (ActorHuman); every other caller is refused.
func (s *Service) CreateRule(ctx context.Context, ws uuid.UUID, in CreateRuleInput, creator Actor) (*DelegationRule, error) {
	if s.rules == nil {
		return nil, errors.New("delegation rules are not configured")
	}
	if creator.Kind != ActorHuman || strings.TrimSpace(creator.ID) == "" {
		return nil, &DeniedError{Reason: ReasonPrincipalCannotDecide}
	}
	if strings.TrimSpace(in.UserRef) == "" || len(in.UserRef) > maxEvidenceLen {
		return nil, fmt.Errorf("%w: user_ref (where the user granted this) is required", ErrInvalidRule)
	}
	if in.ExpiresAt != nil && !in.ExpiresAt.After(time.Now()) {
		return nil, fmt.Errorf("%w: expires_at must be in the future", ErrInvalidRule)
	}
	for k := range in.Conditions {
		if !knownConditions[k] {
			return nil, fmt.Errorf("%w: unknown condition %q", ErrInvalidRule, k)
		}
	}
	r := &DelegationRule{WorkspaceID: ws, Principal: strings.TrimSpace(in.Principal), Kinds: in.Kinds,
		Repos: in.Repos, Conditions: in.Conditions, UserRef: in.UserRef, CreatedBy: creator.ID, ExpiresAt: in.ExpiresAt}
	if err := r.Validate(); err != nil {
		return nil, err
	}
	if err := s.rules.Create(ctx, r); err != nil {
		return nil, err
	}
	return r, nil
}

// ListRules returns every rule in the workspace.
func (s *Service) ListRules(ctx context.Context, ws uuid.UUID) ([]*DelegationRule, error) {
	if s.rules == nil {
		return nil, nil
	}
	return s.rules.ListByWorkspace(ctx, ws)
}

// RevokeRule ends a rule immediately. Only a person may revoke.
func (s *Service) RevokeRule(ctx context.Context, ws, id uuid.UUID, by Actor) error {
	if s.rules == nil {
		return ErrRuleNotFound
	}
	if by.Kind != ActorHuman || strings.TrimSpace(by.ID) == "" {
		return &DeniedError{Reason: ReasonPrincipalCannotDecide}
	}
	return s.rules.Revoke(ctx, ws, id)
}

// WithRules attaches the delegation rule store. Without one, no session can
// ever decide (only humans).
func (s *Service) WithRules(rs RuleStore) *Service { s.rules = rs; return s }

// Decide records a decision on a pending approval.
//
// human: basis=user_direct. session: basis=desk_delegated, allowed only when the
// kind is under the fixed delegation ceiling, the session is not the requester,
// and an active rule for this workspace names the session and covers the
// kind/repo/conditions. Every other caller is refused.
func (s *Service) Decide(ctx context.Context, in DecideInput, actor Actor) (*Approval, error) {
	switch in.Decision {
	case StatusApproved:
	case StatusRejected, StatusRevisionRequested:
		if strings.TrimSpace(in.Note) == "" {
			return nil, fmt.Errorf("%w: note is required to reject or request revision", ErrInvalidInput)
		}
	default:
		return nil, fmt.Errorf("%w: decision must be approved, rejected or revision_requested", ErrInvalidInput)
	}
	if len(in.Note) > maxNoteLen || len(in.UserRef) > maxEvidenceLen {
		return nil, fmt.Errorf("%w: note or user_ref too long", ErrInvalidInput)
	}
	if strings.TrimSpace(actor.ID) == "" {
		return nil, &DeniedError{Reason: ReasonPrincipalCannotDecide}
	}
	a, err := s.GetForTicket(ctx, in.TicketID, in.ApprovalID)
	if err != nil {
		return nil, err
	}
	if a.Status != StatusPending {
		return nil, ErrInvalidTransition
	}

	d := &Decision{Status: in.Decision, DecidedBy: actor.ID, Note: in.Note}
	switch actor.Kind {
	case ActorHuman:
		d.Basis = BasisUserDirect
		d.UserRef = in.UserRef
	case ActorSession:
		rule, err := s.delegationRule(ctx, a, in.WorkspaceID, actor)
		if err != nil {
			return nil, err
		}
		if strings.TrimSpace(in.UserRef) == "" {
			return nil, fmt.Errorf("%w: user_ref is required for a delegated decision", ErrInvalidInput)
		}
		d.Basis, d.DelegationRuleID, d.UserRef = BasisDeskDelegate, &rule.ID, in.UserRef
	default:
		return nil, &DeniedError{Reason: ReasonPrincipalCannotDecide}
	}
	return s.store.Transition(ctx, a.ID, StatusPending, in.Decision, actor.ID, in.Note, d)
}

func (s *Service) delegationRule(ctx context.Context, a *Approval, ws uuid.UUID, actor Actor) (*DelegationRule, error) {
	if !Delegable(a.Kind, a.WithinDirection) {
		return nil, &DeniedError{Reason: ReasonKindNotDelegable}
	}
	// Defense in depth: a session account id is never an email. A person's id
	// here means a person's token reached the session path; refuse it even if a
	// rule somehow names it.
	if ValidatePrincipal(actor.ID) != nil {
		return nil, &DeniedError{Reason: ReasonPrincipalCannotDecide}
	}
	if a.RequestedBy == actor.ID {
		return nil, &DeniedError{Reason: ReasonSelfApproval}
	}
	if s.rules == nil || ws == uuid.Nil {
		return nil, &DeniedError{Reason: ReasonNoMatchingRule}
	}
	rules, err := s.rules.ListByPrincipal(ctx, ws, actor.ID)
	if err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	for _, r := range rules {
		if r.Active(now) && r.matches(a) {
			return r, nil
		}
	}
	return nil, &DeniedError{Reason: ReasonNoMatchingRule}
}

// RecordTicketApproval mirrors a legacy POST /tickets/{id}/approve sign-off as
// an approval record (kind=ticket). decision is the legacy value: "approved",
// "rejected" or "revision". claimedOperator is the operator_id the caller put
// in the request body; it is never used as the decider, only noted.
//
// Only a person attested by the UI client gets basis=user_direct. Anyone else
// (agent, API key, device-code token) is recorded with no basis and
// legacy=true, because the legacy endpoint never proved who was behind them.
func (s *Service) RecordTicketApproval(ctx context.Context, ticketID uuid.UUID, decision, note string, actor Actor, claimedOperator string) (*Approval, error) {
	var to Status
	switch decision {
	case "approved":
		to = StatusApproved
	case "rejected":
		to = StatusRejected
	case "revision":
		to = StatusRevisionRequested
	default:
		return nil, fmt.Errorf("%w: unknown legacy decision %q", ErrInvalidInput, decision)
	}
	if strings.TrimSpace(actor.ID) == "" {
		return nil, fmt.Errorf("%w: requester identity required", ErrForbidden)
	}
	legacy := actor.Kind != ActorHuman || (claimedOperator != "" && claimedOperator != actor.ID)
	req := NewReq{TicketID: ticketID, Kind: KindTicket, Subject: Subject{Ticket: ticketID.String()},
		RequestedBy: actor.ID, RequestNote: "legacy /approve", Legacy: legacy}
	a, err := s.store.Create(ctx, req)
	if errors.Is(err, ErrDuplicatePending) {
		// an earlier attempt left a pending record; decide that one
		open, lerr := s.store.List(ctx, ListFilter{TicketID: &ticketID, Kind: KindTicket, Status: StatusPending, Limit: 1})
		if lerr != nil || len(open) == 0 {
			return nil, err
		}
		a, err = open[0], nil
	}
	if err != nil {
		return nil, err
	}
	d := &Decision{Status: to, DecidedBy: actor.ID, Note: truncate(note, maxNoteLen)}
	if actor.Kind == ActorHuman {
		d.Basis = BasisUserDirect
	}
	if claimedOperator != "" && claimedOperator != actor.ID {
		d.Note = truncate("claimed operator_id="+claimedOperator+"; "+d.Note, maxNoteLen)
	}
	return s.store.Transition(ctx, a.ID, StatusPending, to, actor.ID, d.Note, d)
}

func truncate(s string, n int) string {
	if len(s) > n {
		return s[:n]
	}
	return s
}
