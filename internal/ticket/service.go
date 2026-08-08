package ticket

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
)

// titleNameRe validates the format: <ABBR>-<keyword1>[-<keyword2>] (lowercase, hyphens only).
var titleNameRe = regexp.MustCompile(`^[a-z][a-z0-9]*(-[a-z0-9]+)+$`)

type Publisher interface {
	PublishCreated(ctx context.Context, t *Ticket) error
	PublishAssigned(ctx context.Context, t *Ticket) error
	PublishUpdated(ctx context.Context, t *Ticket) error
	PublishCompleted(ctx context.Context, t *Ticket) error
}

type NuclexEmitter interface {
	EmitCompleted(ctx context.Context, t *Ticket) error
	EmitFailed(ctx context.Context, t *Ticket) error
	EmitPendingApproval(ctx context.Context, t *Ticket) error
	EmitCreated(ctx context.Context, t *Ticket) error
}

// XPAwarder is called when a ticket is completed to grant XP.
type XPAwarder interface {
	AwardXP(ctx context.Context, workspaceID, userID string, amount int) error
}

// WebhookSender fires an HTTP callback when a ticket reaches a terminal state.
type WebhookSender interface {
	Send(ctx context.Context, url string, payload any) error
}

// MagicTokener issues one-time magic-link tokens (satisfied by *magic.Service).
// When nil, public_token generation is skipped.
type MagicTokener interface {
	Request(ctx context.Context, email string, ticketID *uuid.UUID) (string, error)
}

type Service struct {
	store   Store
	pub     Publisher
	nuclex  NuclexEmitter
	xp      XPAwarder
	webhook WebhookSender
	magic   MagicTokener
}

func NewService(store Store, pub Publisher, nuclex NuclexEmitter, xp XPAwarder, wh WebhookSender, magic MagicTokener) *Service {
	return &Service{store: store, pub: pub, nuclex: nuclex, xp: xp, webhook: wh, magic: magic}
}

// Create creates a new ticket. requesterID is pre-extracted from the JWT by the HTTP handler.
func (s *Service) Create(ctx context.Context, req CreateTicketReq, requesterID string) (*Ticket, error) {
	if req.Title == "" {
		return nil, fmt.Errorf("title is required")
	}
	if req.TitleName != "" {
		if !titleNameRe.MatchString(req.TitleName) {
			return nil, fmt.Errorf("title_name must be lowercase hyphen-separated (e.g. goq-vault-portforward)")
		}
	}
	if req.Type == "" {
		req.Type = TypeTask
	}
	if req.Priority == "" {
		req.Priority = PriorityNormal
	}
	if req.AssigneeType == "" {
		req.AssigneeType = AssigneeHuman
	}
	if req.Domain != "" {
		if err := validateDomain(req.Domain); err != nil {
			return nil, err
		}
	}
	if req.RiskTier == "" {
		req.RiskTier = RiskLow
	}
	if err := validateRiskTier(req.RiskTier); err != nil {
		return nil, err
	}
	if req.StartAt != nil && req.DueAt != nil && req.StartAt.After(*req.DueAt) {
		return nil, fmt.Errorf("%w: start_at must not be after due_at", ErrValidation)
	}
	// Pre-validate refs before creating the ticket so a bad request doesn't
	// leave a half-built ticket behind.
	if len(req.RefIDs) > 0 {
		if err := validateRefIDs(req.RefIDs); err != nil {
			return nil, err
		}
	}

	if req.CompletionMode == "" {
		req.CompletionMode = CompletionManual
	}
	if req.Visibility == "" {
		req.Visibility = VisibilityProject
	}

	// Subtasks inherit the parent's project when none is given, so they get a
	// SpiceDB ticket→project tuple and are reachable by project-visibility checks
	// (fine-grained authz). The subtask keeps its own Visibility. Best-effort: if
	// the parent can't be read we leave project_id nil (no behavior change).
	if req.ParentID != nil && req.ProjectID == nil {
		if parent, err := s.store.Get(ctx, *req.ParentID); err == nil && parent != nil {
			req.ProjectID = parent.ProjectID
		}
	}

	now := time.Now().UTC()
	t := &Ticket{
		ID:             uuid.New(),
		TitleName:      req.TitleName,
		Title:          req.Title,
		Description:    req.Description,
		Type:           req.Type,
		Status:         StatusPending,
		Priority:       req.Priority,
		RiskTier:       req.RiskTier,
		AssigneeType:   req.AssigneeType,
		AssigneeID:     req.AssigneeID,
		RequesterID:    requesterID,
		RequesterOrgID: req.RequesterOrgID,
		SourceBot:      req.SourceBot,
		Domain:         req.Domain,
		AgentContext:   req.AgentContext,
		Metadata:       req.Metadata,
		ProjectID:      req.ProjectID,
		ParentID:       req.ParentID,
		Visibility:     req.Visibility,
		CompletionMode: req.CompletionMode,
		CompletionSpec: req.CompletionSpec,
		RewardXP:       req.RewardXP,
		DueAt:          req.DueAt,
		StartAt:        req.StartAt,
		CallbackURL:    req.CallbackURL,
		CreatedAt:      now,
		UpdatedAt:      now,
	}

	if err := s.store.Create(ctx, t); err != nil {
		return nil, fmt.Errorf("store.Create: %w", err)
	}

	// Generate a one-time public_token for requester-facing ticket status access.
	// Reads requester_email from metadata (top-level field addition tracked in BUG-3).
	if s.magic != nil {
		if email, ok := metadataString(req.Metadata, "requester_email"); ok {
			if plain, err := s.magic.Request(ctx, email, &t.ID); err != nil {
				log.Printf("warn: public_token generation failed for ticket %s: %v", t.ID, err)
			} else {
				t.PublicToken = plain
			}
		}
	}

	if len(req.TagIDs) > 0 {
		if err := s.store.SetTicketTags(ctx, t.ID, req.TagIDs); err != nil {
			log.Printf("warn: set ticket tags: %v", err)
		}
	}

	if len(req.RefIDs) > 0 {
		if _, err := s.store.AddRefs(ctx, t.ID, req.RefIDs, requesterID); err != nil {
			return nil, fmt.Errorf("attach refs: %w", err)
		}
		t.Refs = append([]string(nil), req.RefIDs...)
	}

	if err := s.pub.PublishCreated(ctx, t); err != nil {
		log.Printf("warn: publish created: %v", err)
	}

	if s.nuclex != nil {
		if err := s.nuclex.EmitCreated(ctx, t); err != nil {
			log.Printf("warn: nuclex emit created: %v", err)
		}
	}

	if t.AssigneeType == AssigneeAgent {
		if err := s.pub.PublishAssigned(ctx, t); err != nil {
			log.Printf("warn: publish assigned: %v", err)
		}
	}

	return t, nil
}

// BulkCreate creates req.Items as child tickets under req.ParentID in one call
// (goq-cogito-bulk-create-api — the cogito content-split / question-decomposition
// consumer, design doc §3.1). It fetches the parent once to fail fast with
// ErrNotFound; per-item creation is then delegated to Create so type/domain/
// priority inheritance and project inheritance stay defined in exactly one
// place (service.go:~111-115) instead of being duplicated here. Authz (manage
// permission on the parent) is the caller's responsibility — Service has no
// SpiceDB dependency, so the HTTP handler checks it once before calling this
// (mirrors BulkUpdate's per-id authz-in-handler pattern).
//
// Each item is processed independently: one item's failure (e.g. empty title)
// is recorded in its result row and does not abort the rest, matching
// BulkUpdate's partial-failure contract.
func (s *Service) BulkCreate(ctx context.Context, req BulkCreateReq, actorID string) (BulkCreateResp, error) {
	if err := validateBulkCreateSource(req.Source); err != nil {
		return BulkCreateResp{}, err
	}
	if len(req.Items) == 0 {
		return BulkCreateResp{}, fmt.Errorf("%w: items must be non-empty", ErrValidation)
	}

	parent, err := s.store.Get(ctx, req.ParentID)
	if err != nil {
		return BulkCreateResp{}, err
	}

	results := make([]BulkCreateResult, 0, len(req.Items))
	created := 0
	for i, item := range req.Items {
		res := BulkCreateResult{Index: i}

		itemReq := CreateTicketReq{
			Title:       item.Title,
			Description: item.Description,
			ParentID:    &req.ParentID,
			Type:        parent.Type,
			Priority:    parent.Priority,
			Domain:      parent.Domain,
			Metadata: map[string]any{
				"bulk_create_source": string(req.Source),
			},
		}
		if req.SourceRef != "" {
			itemReq.Metadata["bulk_create_source_ref"] = req.SourceRef
		}

		t, cerr := s.Create(ctx, itemReq, actorID)
		if cerr != nil {
			res.Error = cerr.Error()
			results = append(results, res)
			continue
		}
		res.OK = true
		res.ID = t.ID.String()
		results = append(results, res)
		created++
	}

	return BulkCreateResp{Results: results, Created: created}, nil
}

func (s *Service) Get(ctx context.Context, id uuid.UUID) (*Ticket, error) {
	return s.store.Get(ctx, id)
}

// GetByProjectAndTitleName resolves a ticket by its (project_id, title_name)
// pair. Used by the create handler to replay an existing ticket idempotently
// when a duplicate title_name is submitted.
func (s *Service) GetByProjectAndTitleName(ctx context.Context, projectID uuid.UUID, titleName string) (*Ticket, error) {
	return s.store.GetByProjectAndTitleName(ctx, projectID, titleName)
}

func (s *Service) Update(ctx context.Context, id uuid.UUID, req UpdateTicketReq, actor string) (*Ticket, error) {
	if req.AssigneeType != nil && *req.AssigneeType == AssigneeOrg && req.AssigneeOrgID == nil {
		return nil, fmt.Errorf("%w: assignee_org_id required when assignee_type is org", ErrValidation)
	}
	if req.Title != nil && strings.TrimSpace(*req.Title) == "" {
		return nil, fmt.Errorf("%w: title cannot be empty", ErrValidation)
	}
	if req.TitleName != nil {
		if *req.TitleName != "" && !titleNameRe.MatchString(*req.TitleName) {
			return nil, fmt.Errorf("%w: title_name must be lowercase hyphen-separated (e.g. goq-vault-portforward)", ErrValidation)
		}
	}
	if req.Domain != nil {
		if err := validateDomain(*req.Domain); err != nil {
			return nil, err
		}
	}
	if req.RiskTier != nil {
		if err := validateRiskTier(*req.RiskTier); err != nil {
			return nil, err
		}
	}
	t, err := s.store.Update(ctx, id, req)
	if err != nil {
		return nil, err
	}

	// Replace-semantics: nil = leave untouched; empty = clear; non-empty = replace.
	// PATCH-replaced refs carry the caller's actor for created_by attribution;
	// the handler records the corresponding audit entry.
	if req.RefIDs != nil {
		newIDs := *req.RefIDs
		if err := validateRefIDs(newIDs); err != nil {
			return nil, err
		}
		if err := s.replaceRefs(ctx, id, newIDs, actor); err != nil {
			return nil, fmt.Errorf("replace refs: %w", err)
		}
		if refreshed, gerr := s.store.Get(ctx, id); gerr == nil && refreshed != nil {
			t = refreshed
		}
	}

	if err := s.pub.PublishUpdated(ctx, t); err != nil {
		log.Printf("warn: publish updated: %v", err)
	}

	if t.Status == StatusCompleted || t.Status == StatusFailed {
		if err := s.pub.PublishCompleted(ctx, t); err != nil {
			log.Printf("warn: publish completed: %v", err)
		}
		if s.nuclex != nil {
			var emitErr error
			if t.Status == StatusCompleted {
				emitErr = s.nuclex.EmitCompleted(ctx, t)
			} else {
				emitErr = s.nuclex.EmitFailed(ctx, t)
			}
			if emitErr != nil {
				log.Printf("warn: nuclex emit: %v", emitErr)
			}
		}
		// Award XP to requester on successful completion.
		if t.Status == StatusCompleted && t.RewardXP > 0 && t.ProjectID != nil && s.xp != nil {
			workspaceID := t.ProjectID.String() // proxy: workspace derived from context at higher layer
			if err := s.xp.AwardXP(ctx, workspaceID, t.RequesterID, t.RewardXP); err != nil {
				log.Printf("warn: award xp: %v", err)
			}
		}
		// Fire callback_url asynchronously so slow receivers don't block the caller.
		if t.CallbackURL != "" && s.webhook != nil {
			go func(url string, payload *Ticket) {
				if err := s.webhook.Send(context.Background(), url, payload); err != nil {
					log.Printf("warn: webhook delivery to %s: %v", url, err)
				}
			}(t.CallbackURL, t)
		}
	}

	if req.AssigneeType != nil && *req.AssigneeType == AssigneeAgent {
		if err := s.pub.PublishAssigned(ctx, t); err != nil {
			log.Printf("warn: publish assigned: %v", err)
		}
	}

	if t.Status == StatusPendingApproval && s.nuclex != nil {
		if err := s.nuclex.EmitPendingApproval(ctx, t); err != nil {
			log.Printf("warn: nuclex emit pending_approval: %v", err)
		}
	}

	return t, nil
}

func (s *Service) Cancel(ctx context.Context, id uuid.UUID, actor string) (*Ticket, error) {
	cancelled := StatusCancelled
	return s.Update(ctx, id, UpdateTicketReq{Status: &cancelled}, actor)
}

// Reopen moves a completed/cancelled ticket back to in_progress. This is an
// explicit, audited escape hatch for tickets closed in error (e.g. a VCS
// auto-link false-positive completion, or a mistaken manual completion) —
// it intentionally bypasses the normal validateTransition state machine,
// which keeps terminals closed by design (goq-ticket-reopen-terminal). The
// caller (handler) is responsible for audit-logging the reason; this method
// only enforces that a reason was given and that the ticket is actually
// terminal.
func (s *Service) Reopen(ctx context.Context, id uuid.UUID, actor, reason string) (*Ticket, error) {
	if strings.TrimSpace(reason) == "" {
		return nil, fmt.Errorf("%w: reason is required", ErrValidation)
	}
	t, err := s.store.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	if t.Status != StatusCompleted && t.Status != StatusCancelled {
		return nil, fmt.Errorf("%w: only completed or cancelled tickets can be reopened (current: %s)", ErrInvalidTransition, t.Status)
	}
	reopened, err := s.store.ReopenTerminal(ctx, id)
	if err != nil {
		return nil, err
	}
	if err := s.pub.PublishUpdated(ctx, reopened); err != nil {
		log.Printf("warn: publish updated: %v", err)
	}
	return reopened, nil
}

func (s *Service) Approve(ctx context.Context, id uuid.UUID, req ApproveTicketReq) (*Ticket, error) {
	t, err := s.store.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	// Pre-check for user-friendly error; transition validator in store.Update
	// provides atomic protection against concurrent approvals.
	if t.Status != StatusPendingApproval {
		return nil, fmt.Errorf("%w: only pending_approval tickets can be approved (current: %s)", ErrInvalidTransition, t.Status)
	}
	if req.Decision == "" {
		return nil, fmt.Errorf("decision is required")
	}

	var newStatus Status
	switch req.Decision {
	case ApproveDecisionApproved:
		newStatus = StatusAssigned
	case ApproveDecisionRejected:
		newStatus = StatusCancelled
	case ApproveDecisionRevision:
		newStatus = StatusPendingRevision
	default:
		return nil, fmt.Errorf("unknown decision: %s", req.Decision)
	}

	meta := t.Metadata
	if meta == nil {
		meta = map[string]any{}
	}
	meta["approval_decision"] = string(req.Decision)
	meta["approval_operator_id"] = req.OperatorID
	if req.RevisionNote != "" {
		meta["approval_revision_note"] = req.RevisionNote
	}

	updated, err := s.store.Update(ctx, id, UpdateTicketReq{
		Status:   &newStatus,
		Metadata: meta,
	})
	if err != nil {
		return nil, fmt.Errorf("store.Update: %w", err)
	}

	if err := s.pub.PublishUpdated(ctx, updated); err != nil {
		log.Printf("warn: publish updated (approve): %v", err)
	}

	if newStatus == StatusAssigned {
		if err := s.pub.PublishAssigned(ctx, updated); err != nil {
			log.Printf("warn: publish assigned (approve): %v", err)
		}
	}
	if newStatus == StatusCancelled && s.nuclex != nil {
		if err := s.nuclex.EmitFailed(ctx, updated); err != nil {
			log.Printf("warn: nuclex emit failed (approve rejected): %v", err)
		}
	}

	return updated, nil
}

func (s *Service) List(ctx context.Context, f ListTicketsFilter) ([]*Ticket, int, error) {
	return s.store.List(ctx, f)
}

// Aggregate returns COUNT(*) grouped by one whitelisted column, scoped by f
// (including the visibility filter). Unknown groupBy → ErrInvalidGroupBy.
func (s *Service) Aggregate(ctx context.Context, groupBy string, f ListTicketsFilter) ([]AggBucket, error) {
	return s.store.Aggregate(ctx, groupBy, f)
}

func (s *Service) ListSubtasks(ctx context.Context, parentID uuid.UUID) ([]*Ticket, error) {
	return s.store.ListSubtasks(ctx, parentID)
}

func (s *Service) ListAttempts(ctx context.Context, ticketID uuid.UUID) ([]*Attempt, error) {
	return s.store.ListAttempts(ctx, ticketID)
}

// SubmitAttempt records an answer attempt for an answer-mode ticket.
// Returns the updated attempt (with IsCorrect set) and any error.
func (s *Service) SubmitAttempt(ctx context.Context, ticketID uuid.UUID, userID string, req SubmitAttemptReq) (*Attempt, error) {
	t, err := s.store.Get(ctx, ticketID)
	if err != nil {
		return nil, err
	}
	if t.CompletionMode != CompletionAnswer {
		return nil, fmt.Errorf("ticket is not in answer mode")
	}
	if t.Status.IsTerminal() {
		return nil, fmt.Errorf("ticket is already closed")
	}
	if t.Status == StatusPendingApproval || t.Status == StatusPendingRevision {
		return nil, fmt.Errorf("ticket is awaiting approval and cannot receive new submissions")
	}

	var spec AnswerSpec
	specBytes, err := json.Marshal(t.CompletionSpec)
	if err != nil {
		return nil, fmt.Errorf("marshal spec: %w", err)
	}
	if err := json.Unmarshal(specBytes, &spec); err != nil {
		return nil, fmt.Errorf("parse answer spec: %w", err)
	}

	if spec.MaxAttempts > 0 {
		count, err := s.store.CountAttempts(ctx, ticketID, userID)
		if err != nil {
			return nil, err
		}
		if count >= spec.MaxAttempts {
			return nil, fmt.Errorf("max attempts (%d) reached", spec.MaxAttempts)
		}
	}

	correct := false
	switch spec.AnswerType {
	case "exact":
		correct = strings.EqualFold(strings.TrimSpace(req.Answer), strings.TrimSpace(spec.CorrectAnswer))
	case "choice":
		correct = strings.EqualFold(req.Answer, spec.CorrectAnswer)
	default:
		// ai_eval: leave IsCorrect nil until AI grades it
	}

	a := &Attempt{
		ID:          uuid.New(),
		TicketID:    ticketID,
		UserID:      userID,
		Answer:      req.Answer,
		AttemptedAt: time.Now().UTC(),
	}
	if spec.AnswerType != "ai_eval" {
		a.IsCorrect = &correct
	}

	if err := s.store.CreateAttempt(ctx, a); err != nil {
		return nil, err
	}

	if a.IsCorrect != nil && *a.IsCorrect {
		completed := StatusCompleted
		if _, err := s.Update(ctx, ticketID, UpdateTicketReq{Status: &completed}, "system:autocomplete"); err != nil {
			log.Printf("warn: auto-complete ticket after correct answer: %v", err)
		}
	}

	return a, nil
}

var validDomains = map[Domain]struct{}{
	DomainInfra: {}, DomainData: {}, DomainCode: {},
	DomainMonitor: {}, DomainKnowledge: {}, DomainPM: {},
}

func validateDomain(d Domain) error {
	if _, ok := validDomains[d]; !ok {
		return fmt.Errorf("%w: invalid domain: %s", ErrValidation, d)
	}
	return nil
}

var validBulkCreateSources = map[BulkCreateSource]struct{}{
	BulkCreateSourceContentSplit: {}, BulkCreateSourceQuestionDecomposition: {}, BulkCreateSourceManual: {},
}

// validateBulkCreateSource rejects an empty or unknown source — unlike
// risk_tier, source has no server-side default (it's an audit/debugging field
// the caller must always supply, per design doc §3.1).
func validateBulkCreateSource(src BulkCreateSource) error {
	if _, ok := validBulkCreateSources[src]; !ok {
		return fmt.Errorf("%w: invalid source: %q", ErrValidation, src)
	}
	return nil
}

var validRiskTiers = map[RiskTier]struct{}{
	RiskLow: {}, RiskMedium: {}, RiskHigh: {},
}

// validateRiskTier allows "" (defaulted by the caller) plus the three known
// tiers; anything else is rejected.
func validateRiskTier(r RiskTier) error {
	if r == "" {
		return nil
	}
	if _, ok := validRiskTiers[r]; !ok {
		return fmt.Errorf("%w: invalid risk_tier: %q", ErrValidation, r)
	}
	return nil
}

// --- Sort ---

// ErrInvalidSortField is returned when a _sort query param names a column not
// in the allowedSortFields whitelist.
var ErrInvalidSortField = errors.New("invalid sort field")

// allowedSortFields maps user-facing field names to their qualified SQL columns.
var allowedSortFields = map[string]string{
	"created_at":  "t.created_at",
	"updated_at":  "t.updated_at",
	"due_at":      "t.due_at",
	"start_at":    "t.start_at",
	"priority":    "t.priority",
	"title":       "t.title",
	"status":      "t.status",
	"type":        "t.type",
	"domain":      "t.domain",
	"assignee_id": "t.assignee_id",
}

// ValidateSortField returns nil for empty (means "use default") or any field in
// the whitelist, and ErrInvalidSortField otherwise.
func ValidateSortField(name string) error {
	if name == "" {
		return nil
	}
	if _, ok := allowedSortFields[name]; !ok {
		return ErrInvalidSortField
	}
	return nil
}

// SortClause builds the ORDER BY fragment for a List query.
//
// Priority ordering semantics: the CASE assigns urgent=0 (lowest integer), so
// "urgent first" maps to ASC. Callers that want urgent tickets at the top
// should pass order="asc" (or leave it empty — see below). Default for
// priority is "asc" so the most critical items appear first; all other fields
// default to DESC NULLS LAST (newest first).
func SortClause(field, order string) string {
	col, ok := allowedSortFields[field]
	if !ok {
		col = "t.created_at"
	}

	dir := "DESC"
	if order == "asc" || order == "ASC" {
		dir = "ASC"
	}

	if col == "t.priority" {
		// urgent=0, high=1, normal=2, low=3 → ASC means urgent first.
		// When no explicit order is given, default to ASC for priority so the
		// most critical items surface at the top.
		priorityDir := "ASC"
		if order == "desc" || order == "DESC" {
			priorityDir = "DESC"
		}
		return "ORDER BY CASE t.priority WHEN 'urgent' THEN 0 WHEN 'high' THEN 1 WHEN 'normal' THEN 2 WHEN 'low' THEN 3 ELSE 4 END " + priorityDir
	}
	return "ORDER BY " + col + " " + dir + " NULLS LAST"
}

// metadataString extracts a non-empty string value from a metadata map.
func metadataString(m map[string]any, key string) (string, bool) {
	if m == nil {
		return "", false
	}
	v, ok := m[key]
	if !ok {
		return "", false
	}
	s, ok := v.(string)
	return s, ok && s != ""
}

// --- Refs ---

// AddRefs validates the requested gopedia L1 ids and appends them to the
// ticket's ref list. Empty input is a no-op (returns nil, nil). Duplicates
// within the request, empty/over-long ids, and crossing MaxRefsPerTicket are
// all rejected before the store is touched.
func (s *Service) AddRefs(ctx context.Context, ticketID uuid.UUID, gopediaL1IDs []string, createdBy string) ([]TicketRef, error) {
	if len(gopediaL1IDs) == 0 {
		return nil, nil
	}
	if err := validateRefIDs(gopediaL1IDs); err != nil {
		return nil, err
	}
	existing, err := s.store.CountRefs(ctx, ticketID)
	if err != nil {
		return nil, err
	}
	if existing+len(gopediaL1IDs) > MaxRefsPerTicket {
		return nil, ErrRefLimitExceeded
	}
	return s.store.AddRefs(ctx, ticketID, gopediaL1IDs, createdBy)
}

// ListRefs returns the ticket's refs in stored order.
func (s *Service) ListRefs(ctx context.Context, ticketID uuid.UUID) ([]TicketRef, error) {
	return s.store.ListRefs(ctx, ticketID)
}

// RemoveRef removes a single ref from a ticket. Idempotent at the store layer.
func (s *Service) RemoveRef(ctx context.Context, ticketID uuid.UUID, gopediaL1ID string) error {
	return s.store.RemoveRef(ctx, ticketID, gopediaL1ID)
}

// ReorderRefs validates ids and reorders the ticket's refs to match the input.
func (s *Service) ReorderRefs(ctx context.Context, ticketID uuid.UUID, orderedL1IDs []string) error {
	if err := validateRefIDs(orderedL1IDs); err != nil {
		return err
	}
	return s.store.ReorderRefs(ctx, ticketID, orderedL1IDs)
}

// validateRefIDs checks per-request ref id constraints: non-empty, length <= 512,
// no duplicates, and request size within MaxRefsPerTicket. Cumulative limits across
// existing refs must be enforced separately by the caller (see AddRefs).
func validateRefIDs(ids []string) error {
	seen := map[string]bool{}
	for _, id := range ids {
		if id == "" || len(id) > MaxRefIDLength {
			return ErrRefIDInvalid
		}
		if seen[id] {
			return ErrRefIDsDuplicate
		}
		seen[id] = true
	}
	if len(ids) > MaxRefsPerTicket {
		return ErrRefLimitExceeded
	}
	return nil
}

// replaceRefs clears existing refs and installs newIDs. Not transactional across
// the remove/add boundary: a concurrent Update on the same ticket can interleave.
// Acceptable for v1 (refs are optional/idempotent and races are extremely rare in
// practice); revisit when SLO requires hard atomicity.
// replaceRefs records createdBy on the replacement refs; callers thread the
// actor from Update (PATCH) or AddRefs so ticket_refs.created_by is attributed.
func (s *Service) replaceRefs(ctx context.Context, ticketID uuid.UUID, newIDs []string, createdBy string) error {
	existing, err := s.store.ListRefs(ctx, ticketID)
	if err != nil {
		return err
	}
	for _, r := range existing {
		if err := s.store.RemoveRef(ctx, ticketID, r.GopediaL1ID); err != nil {
			return err
		}
	}
	if len(newIDs) > 0 {
		if _, err := s.store.AddRefs(ctx, ticketID, newIDs, createdBy); err != nil {
			return err
		}
	}
	return nil
}

// ListTicketsByRef returns ticket ids that reference the given gopedia L1 doc.
// If `visible` is nil, no visibility filter is applied (caller is unrestricted —
// e.g., shadow mode with NoopChecker). If `visible` is non-nil, only ids whose
// UUID appears as a key with value true are returned.
func (s *Service) ListTicketsByRef(ctx context.Context, gopediaL1ID string, visible map[uuid.UUID]bool) ([]uuid.UUID, error) {
	if gopediaL1ID == "" || len(gopediaL1ID) > MaxRefIDLength {
		return nil, ErrRefIDInvalid
	}
	ids, err := s.store.ListTicketIDsByRef(ctx, gopediaL1ID)
	if err != nil {
		return nil, err
	}
	if visible == nil {
		return ids, nil
	}
	out := make([]uuid.UUID, 0, len(ids))
	for _, id := range ids {
		if visible[id] {
			out = append(out, id)
		}
	}
	return out, nil
}

// --- Relations ---

// AddRelation validates relation_type and self-reference before delegating
// to the store. Idempotent: replaying the same (from, to, type) returns the
// existing row with created=false rather than erroring.
func (s *Service) AddRelation(ctx context.Context, fromTicketID, toTicketID uuid.UUID, relationType, createdBy string) (TicketRelation, bool, error) {
	if !ValidRelationTypes[relationType] {
		return TicketRelation{}, false, ErrRelationTypeInvalid
	}
	if fromTicketID == toTicketID {
		return TicketRelation{}, false, ErrRelationSelfReference
	}
	return s.store.AddRelation(ctx, fromTicketID, toTicketID, relationType, createdBy)
}

// RemoveRelation deletes relationID, scoped to ticketID (either side) at the
// store layer — the ownership invariant lives here, not just in the handler,
// so any future caller (a script, a bulk job, a different handler) can't
// delete an arbitrary relation by id without going through this check.
// Returns ErrNotFound if the id doesn't exist, was already removed, or
// belongs to a different ticket pair — the three cases are indistinguishable
// on purpose (avoids leaking which one it was).
func (s *Service) RemoveRelation(ctx context.Context, ticketID, relationID uuid.UUID) error {
	found, err := s.store.RemoveRelation(ctx, ticketID, relationID)
	if err != nil {
		return err
	}
	if !found {
		return ErrNotFound
	}
	return nil
}

// ListRelations returns every relation where ticketID is either side
// (from or to), unfiltered by direction — callers derive direction/label
// from FromTicketID/ToTicketID vs ticketID themselves.
func (s *Service) ListRelations(ctx context.Context, ticketID uuid.UUID) ([]TicketRelation, error) {
	return s.store.ListRelationsForTicket(ctx, ticketID)
}

// ListRelationsWithSummary is like ListRelations but each row also carries a
// summary (title_name/title/status/visibility) of whichever ticket is NOT
// ticketID, for the GET .../relations handler's response + visibility filter.
func (s *Service) ListRelationsWithSummary(ctx context.Context, ticketID uuid.UUID) ([]RelationWithSummary, error) {
	return s.store.ListRelationsWithSummaryForTicket(ctx, ticketID)
}

// ListAllRelations is the brain-importer bulk pull path (paginated).
func (s *Service) ListAllRelations(ctx context.Context, limit, offset int) ([]TicketRelation, int, error) {
	return s.store.ListAllRelations(ctx, limit, offset)
}
