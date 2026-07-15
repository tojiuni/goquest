package ticket

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

type pgStore struct {
	db *pgxpool.Pool
}

func NewPGStore(db *pgxpool.Pool) Store {
	return &pgStore{db: db}
}

func (s *pgStore) Create(ctx context.Context, t *Ticket) error {
	agentCtxJSON, err := marshalNullableJSON(t.AgentContext)
	if err != nil {
		return fmt.Errorf("marshal agent_context: %w", err)
	}
	metaJSON, err := marshalNullableJSON(t.Metadata)
	if err != nil {
		return fmt.Errorf("marshal metadata: %w", err)
	}
	specJSON, err := marshalNullableJSON(t.CompletionSpec)
	if err != nil {
		return fmt.Errorf("marshal completion_spec: %w", err)
	}
	if specJSON == nil {
		specJSON = []byte("{}")
	}

	var domain *string
	if t.Domain != "" {
		d := string(t.Domain)
		domain = &d
	}
	var sourceBot *string
	if t.SourceBot != "" {
		sb := t.SourceBot
		sourceBot = &sb
	}
	visibility := t.Visibility
	if visibility == "" {
		visibility = VisibilityProject
	}
	mode := t.CompletionMode
	if mode == "" {
		mode = CompletionManual
	}
	var callbackURL *string
	if t.CallbackURL != "" {
		callbackURL = &t.CallbackURL
	}

	var titleName *string
	if t.TitleName != "" {
		titleName = &t.TitleName
	}

	riskTier := t.RiskTier
	if riskTier == "" {
		riskTier = RiskLow
	}

	_, err = s.db.Exec(ctx, `
		INSERT INTO tickets (
			id, title_name, title, description, type, status, priority, risk_tier,
			assignee_type, assignee_id, requester_id, source_bot,
			domain, agent_context, metadata,
			project_id, parent_id, visibility, completion_mode,
			completion_spec, reward_xp, due_at, callback_url,
			created_at, updated_at
		) VALUES (
			$1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,
			$16,$17,$18,$19,$20,$21,$22,$23,$24,$25
		)`,
		t.ID, titleName, t.Title, t.Description, string(t.Type), string(t.Status), string(t.Priority), string(riskTier),
		string(t.AssigneeType), t.AssigneeID, t.RequesterID, sourceBot,
		domain, agentCtxJSON, metaJSON,
		t.ProjectID, t.ParentID, string(visibility), string(mode),
		specJSON, t.RewardXP, t.DueAt, callbackURL,
		t.CreatedAt, t.UpdatedAt,
	)
	if err != nil {
		if isUniqueViolation(err, "tickets_project_title_name_key") {
			return ErrDuplicateTitleName
		}
		return fmt.Errorf("insert ticket: %w", err)
	}
	return nil
}

func (s *pgStore) Get(ctx context.Context, id uuid.UUID) (*Ticket, error) {
	row := s.db.QueryRow(ctx, ticketSelectSQL+"WHERE t.id = $1"+ticketGroupBySQL, id)
	t, err := scanTicket(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("get ticket: %w", err)
	}
	refs, err := s.ListRefIDsForTickets(ctx, []uuid.UUID{t.ID})
	if err != nil {
		return nil, fmt.Errorf("load refs: %w", err)
	}
	t.Refs = refs[t.ID]
	return t, nil
}

// GetByProjectAndTitleName returns the ticket uniquely identified by the
// (project_id, title_name) pair — the partial-unique index
// tickets_project_title_name_key. Mirrors Get's projection/scan, filtering on
// the pair instead of id. Used for idempotent create replay.
func (s *pgStore) GetByProjectAndTitleName(ctx context.Context, projectID uuid.UUID, titleName string) (*Ticket, error) {
	row := s.db.QueryRow(ctx, ticketSelectSQL+"WHERE t.project_id = $1 AND t.title_name = $2"+ticketGroupBySQL, projectID, titleName)
	t, err := scanTicket(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("get ticket by project+title_name: %w", err)
	}
	refs, err := s.ListRefIDsForTickets(ctx, []uuid.UUID{t.ID})
	if err != nil {
		return nil, fmt.Errorf("load refs: %w", err)
	}
	t.Refs = refs[t.ID]
	return t, nil
}

// ticketSelectSQL is the base SELECT with LEFT JOINs for tags, project, workspace.
// Callers append a WHERE clause followed by ticketGroupBySQL.
//
// ticketGroupBySQL must list every non-aggregated column that comes from a
// LEFT-JOINed table (currently p.name, w.name, pt.title). Add to it whenever
// a new non-aggregated joined column is added to ticketSelectSQL. Columns from
// an already-aggregated derived table (like cc.n from the per-parent
// child_count subquery) are wrapped in MAX/SUM/etc. in the SELECT instead —
// that satisfies the GROUP BY requirement without changing semantics, since the
// subquery guarantees one row per parent_id.
const ticketGroupBySQL = ` GROUP BY t.id, p.name, w.name, pt.title`
const ticketSelectSQL = `
	SELECT t.id, t.title_name, t.title, t.description, t.type, t.status, t.priority, t.risk_tier,
	       t.assignee_type, t.assignee_id, t.requester_id, t.source_bot,
	       t.domain, t.agent_context, t.metadata,
	       t.project_id, t.parent_id, t.visibility, t.completion_mode,
	       t.completion_spec, t.reward_xp, t.due_at, t.callback_url,
	       t.created_at, t.updated_at, t.completed_at, t.cancellation_note,
	       COALESCE(
	         json_agg(json_build_object('id',tg.id,'name',tg.name,'color',tg.color))
	         FILTER (WHERE tg.id IS NOT NULL),
	         '[]'
	       ) AS tags,
	       COALESCE(MAX(cc.n), 0)::int AS child_count,
	       COALESCE(p.name, '') AS project_name,
	       COALESCE(w.name, '') AS workspace_name,
	       COALESCE(pt.title, '') AS parent_title
	FROM tickets t
	LEFT JOIN ticket_tags tt ON tt.ticket_id = t.id
	LEFT JOIN tags tg        ON tg.id = tt.tag_id
	LEFT JOIN projects p     ON p.id = t.project_id
	LEFT JOIN workspaces w   ON w.id = p.workspace_id
	LEFT JOIN tickets pt     ON pt.id = t.parent_id
	LEFT JOIN (
		SELECT parent_id, COUNT(*) AS n
		FROM tickets
		WHERE parent_id IS NOT NULL
		GROUP BY parent_id
	) cc ON cc.parent_id = t.id
`

func (s *pgStore) Update(ctx context.Context, id uuid.UUID, req UpdateTicketReq) (*Ticket, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	// Lock the row first (FOR UPDATE is incompatible with GROUP BY in PostgreSQL).
	var lockID uuid.UUID
	if err := tx.QueryRow(ctx, `SELECT id FROM tickets WHERE id = $1 FOR UPDATE`, id).Scan(&lockID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("lock ticket for update: %w", err)
	}

	row := tx.QueryRow(ctx, ticketSelectSQL+"WHERE t.id = $1"+ticketGroupBySQL, id)
	current, err := scanTicket(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("get ticket for update: %w", err)
	}

	if req.Status != nil {
		if err := validateTransition(current.Status, *req.Status); err != nil {
			return nil, err
		}
	}

	newStatus := current.Status
	if req.Status != nil {
		newStatus = *req.Status
	}
	newAssigneeID := current.AssigneeID
	if req.AssigneeID != nil {
		newAssigneeID = *req.AssigneeID
	}
	newAssigneeType := current.AssigneeType
	if req.AssigneeType != nil {
		newAssigneeType = *req.AssigneeType
	}
	newRiskTier := current.RiskTier
	if req.RiskTier != nil {
		newRiskTier = *req.RiskTier
	}
	if newRiskTier == "" {
		newRiskTier = RiskLow
	}

	var newDomain *string
	if req.Domain != nil {
		d := string(*req.Domain)
		newDomain = &d
	} else if current.Domain != "" {
		d := string(current.Domain)
		newDomain = &d
	}

	newAgentCtx := current.AgentContext
	if req.AgentContext != nil {
		newAgentCtx = req.AgentContext
	}

	newMetadata := current.Metadata
	if req.Metadata != nil {
		newMetadata = req.Metadata
	}

	newVisibility := current.Visibility
	if req.Visibility != nil {
		newVisibility = *req.Visibility
	}
	newMode := current.CompletionMode
	if req.CompletionMode != nil {
		newMode = *req.CompletionMode
	}
	newSpec := current.CompletionSpec
	if req.CompletionSpec != nil {
		newSpec = req.CompletionSpec
	}
	newRewardXP := current.RewardXP
	if req.RewardXP != nil {
		newRewardXP = *req.RewardXP
	}
	newDueAt := current.DueAt
	if req.DueAt != nil {
		newDueAt = req.DueAt
	}
	newCallbackURL := current.CallbackURL
	if req.CallbackURL != nil {
		newCallbackURL = *req.CallbackURL
	}
	newTitle := current.Title
	if req.Title != nil {
		newTitle = *req.Title
	}
	newDescription := current.Description
	if req.Description != nil {
		newDescription = *req.Description
	}
	newTitleName := current.TitleName
	if req.TitleName != nil {
		newTitleName = *req.TitleName
	}

	// Project move: validate the target exists and shares the current project's
	// workspace (tags, title_name uniqueness and visibility are all scoped by
	// workspace/project — a cross-workspace move would silently break them).
	newProjectID := current.ProjectID
	if req.ProjectID != nil && (current.ProjectID == nil || *current.ProjectID != *req.ProjectID) {
		var targetWS uuid.UUID
		if err := tx.QueryRow(ctx, `SELECT workspace_id FROM projects WHERE id = $1`, *req.ProjectID).Scan(&targetWS); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return nil, ErrProjectNotFound
			}
			return nil, fmt.Errorf("lookup target project: %w", err)
		}
		if current.ProjectID != nil {
			var currentWS uuid.UUID
			if err := tx.QueryRow(ctx, `SELECT workspace_id FROM projects WHERE id = $1`, *current.ProjectID).Scan(&currentWS); err != nil {
				return nil, fmt.Errorf("lookup current project: %w", err)
			}
			if currentWS != targetWS {
				return nil, ErrCrossWorkspaceMove
			}
		}
		newProjectID = req.ProjectID
	}
	var titleNamePtr *string
	if newTitleName != "" {
		titleNamePtr = &newTitleName
	}

	agentCtxJSON, err := marshalNullableJSON(newAgentCtx)
	if err != nil {
		return nil, fmt.Errorf("marshal agent_context: %w", err)
	}
	metaJSON, err := marshalNullableJSON(newMetadata)
	if err != nil {
		return nil, fmt.Errorf("marshal metadata: %w", err)
	}
	specJSON, err := marshalNullableJSON(newSpec)
	if err != nil {
		return nil, fmt.Errorf("marshal completion_spec: %w", err)
	}
	if specJSON == nil {
		specJSON = []byte("{}")
	}

	var callbackURL *string
	if newCallbackURL != "" {
		callbackURL = &newCallbackURL
	}

	// cancellation_note: only set when transitioning to cancelled and note is provided.
	var cancellationNote *string
	if newStatus == StatusCancelled && req.CancellationNote != nil {
		cancellationNote = req.CancellationNote
	} else {
		cancellationNote = current.CancellationNote
	}

	now := time.Now().UTC()
	var completedAt *time.Time
	if newStatus == StatusCompleted {
		if current.CompletedAt != nil {
			completedAt = current.CompletedAt
		} else {
			completedAt = &now
		}
	}

	_, err = tx.Exec(ctx, `
		UPDATE tickets SET
			title_name=$2, title=$3, description=$4,
			status=$5, assignee_id=$6, assignee_type=$7, domain=$8,
			agent_context=$9, metadata=$10,
			visibility=$11, completion_mode=$12, completion_spec=$13,
			reward_xp=$14, due_at=$15, callback_url=$16,
			updated_at=$17, completed_at=$18,
			cancellation_note=$19, risk_tier=$20,
			project_id=$21
		WHERE id=$1`,
		id,
		titleNamePtr, newTitle, newDescription,
		string(newStatus), newAssigneeID, string(newAssigneeType), newDomain,
		agentCtxJSON, metaJSON,
		string(newVisibility), string(newMode), specJSON,
		newRewardXP, newDueAt, callbackURL,
		now, completedAt,
		cancellationNote, string(newRiskTier),
		newProjectID,
	)
	if err != nil {
		if isUniqueViolation(err, "tickets_project_title_name_key") {
			return nil, ErrDuplicateTitleName
		}
		return nil, fmt.Errorf("update ticket: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit tx: %w", err)
	}

	return s.Get(ctx, id)
}

// ticketWhere builds the shared WHERE clause + bound args from a
// ListTicketsFilter, used by both List and Aggregate so the two stay in lockstep
// (same filters, same authz visibility predicate). It returns the clause
// ("WHERE 1=1 AND …"), the args, and the next positional placeholder index —
// callers that append more parameters (LIMIT/OFFSET) continue numbering from it.
func ticketWhere(f ListTicketsFilter) (string, []any, int) {
	args := []any{}
	where := "WHERE 1=1"
	i := 1

	if f.Status != nil {
		where += fmt.Sprintf(" AND t.status=$%d", i)
		args = append(args, string(*f.Status))
		i++
	}
	if f.Type != nil {
		where += fmt.Sprintf(" AND t.type=$%d", i)
		args = append(args, string(*f.Type))
		i++
	}
	if f.Priority != nil {
		where += fmt.Sprintf(" AND t.priority=$%d", i)
		args = append(args, string(*f.Priority))
		i++
	}
	if f.Domain != nil {
		where += fmt.Sprintf(" AND t.domain=$%d", i)
		args = append(args, string(*f.Domain))
		i++
	}
	if f.AssigneeType != nil {
		where += fmt.Sprintf(" AND t.assignee_type=$%d", i)
		args = append(args, string(*f.AssigneeType))
		i++
	}
	if f.AssigneeID != nil {
		where += fmt.Sprintf(" AND t.assignee_id=$%d", i)
		args = append(args, *f.AssigneeID)
		i++
	}
	if f.RequesterID != nil {
		where += fmt.Sprintf(" AND t.requester_id=$%d", i)
		args = append(args, *f.RequesterID)
		i++
	}
	if f.ProjectID != nil {
		where += fmt.Sprintf(" AND t.project_id=$%d", i)
		args = append(args, *f.ProjectID)
		i++
	}
	if f.WorkspaceID != nil {
		// tickets has no workspace_id column — derive via projects.
		where += fmt.Sprintf(" AND t.project_id IN (SELECT id FROM projects WHERE workspace_id=$%d)", i)
		args = append(args, *f.WorkspaceID)
		i++
	}
	if f.ParentID != nil {
		where += fmt.Sprintf(" AND t.parent_id=$%d", i)
		args = append(args, *f.ParentID)
		i++
	}
	if f.TopLevelOnly {
		where += " AND t.parent_id IS NULL"
	}
	if f.TitleName != nil {
		where += fmt.Sprintf(" AND t.title_name=$%d", i)
		args = append(args, *f.TitleName)
		i++
	}
	if f.Query != nil {
		// Case-insensitive substring match across title, title_name, description.
		// LIKE metacharacters in the user term are escaped so it matches literally
		// (backslash is ILIKE's default escape char): "50%" finds literal "50%",
		// not "50<anything>". The same placeholder is reused three times; args
		// carries one wrapped term.
		esc := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(*f.Query)
		where += fmt.Sprintf(" AND (t.title ILIKE $%d OR t.title_name ILIKE $%d OR t.description ILIKE $%d)", i, i, i)
		args = append(args, "%"+esc+"%")
		i++
	}
	if len(f.TagIDs) > 0 {
		// Subquery (not a join predicate) keeps GROUP BY t.id intact and matches
		// tickets carrying ANY of the requested tags.
		where += fmt.Sprintf(" AND t.id IN (SELECT ticket_id FROM ticket_tags WHERE tag_id = ANY($%d))", i)
		args = append(args, f.TagIDs)
		i++
	}
	if f.VisibleFilter != nil {
		// enforce mode: restrict to SpiceDB-visible tickets, honoring per-ticket
		// visibility. Empty (non-nil) slices → ANY('{}') is false → ticket excluded.
		// Callers (List handler) MUST pass non-nil slices; a nil slice encodes as SQL
		// NULL, and t.id = ANY(NULL) is NULL (unknown), not false.
		where += fmt.Sprintf(
			" AND ((t.visibility='project' AND t.id = ANY($%d)) OR (t.visibility='restricted' AND t.id = ANY($%d)))",
			i, i+1)
		args = append(args, f.VisibleFilter.Project, f.VisibleFilter.Restricted)
		i += 2
	}
	return where, args, i
}

// aggColumns whitelists the group_by keys the analytics endpoint accepts, each
// mapped to a fixed column expression. group_by is NEVER interpolated raw — only
// a value from this map reaches the SQL, so a caller cannot inject SQL via the
// column name.
var aggColumns = map[string]string{
	"status":        "t.status",
	"type":          "t.type",
	"priority":      "t.priority",
	"domain":        "t.domain",
	"risk_tier":     "t.risk_tier",
	"assignee_id":   "t.assignee_id",
	"assignee_type": "t.assignee_type",
}

// Aggregate returns COUNT(*) grouped by one whitelisted column, honoring the same
// filters + SpiceDB visibility predicate as List (via ticketWhere). Buckets are
// ordered by descending count. An unknown groupBy yields ErrInvalidGroupBy.
func (s *pgStore) Aggregate(ctx context.Context, groupBy string, f ListTicketsFilter) ([]AggBucket, error) {
	col, ok := aggColumns[groupBy]
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrInvalidGroupBy, groupBy)
	}
	where, args, _ := ticketWhere(f)
	q := "SELECT " + col + " AS k, COUNT(*) AS n FROM tickets t " + where +
		" GROUP BY " + col + " ORDER BY n DESC, k ASC"
	rows, err := s.db.Query(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("aggregate tickets: %w", err)
	}
	defer rows.Close()
	var out []AggBucket
	for rows.Next() {
		var k *string
		var n int
		if err := rows.Scan(&k, &n); err != nil {
			return nil, fmt.Errorf("scan aggregate: %w", err)
		}
		key := ""
		if k != nil {
			key = *k
		}
		out = append(out, AggBucket{Key: key, Count: n})
	}
	return out, rows.Err()
}

func (s *pgStore) List(ctx context.Context, f ListTicketsFilter) ([]*Ticket, int, error) {
	where, args, i := ticketWhere(f)

	countQuery := "SELECT COUNT(*) FROM tickets t " + where
	var total int
	if err := s.db.QueryRow(ctx, countQuery, args...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("count tickets: %w", err)
	}

	limit := f.Limit
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	offset := f.Offset
	if offset < 0 {
		offset = 0
	}

	sortClause := SortClause(f.SortField, f.SortOrder)
	listArgs := append(args, limit, offset)
	rows, err := s.db.Query(ctx,
		ticketSelectSQL+where+ticketGroupBySQL+" "+sortClause+
			fmt.Sprintf(" LIMIT $%d OFFSET $%d", i, i+1),
		listArgs...,
	)
	if err != nil {
		return nil, 0, fmt.Errorf("list tickets: %w", err)
	}
	defer rows.Close()

	var tickets []*Ticket
	for rows.Next() {
		t, err := scanTicket(rows)
		if err != nil {
			return nil, 0, fmt.Errorf("scan ticket: %w", err)
		}
		tickets = append(tickets, t)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("rows error: %w", err)
	}

	if len(tickets) > 0 {
		ids := make([]uuid.UUID, 0, len(tickets))
		for _, t := range tickets {
			ids = append(ids, t.ID)
		}
		refs, err := s.ListRefIDsForTickets(ctx, ids)
		if err != nil {
			return nil, 0, fmt.Errorf("load refs: %w", err)
		}
		for _, t := range tickets {
			t.Refs = refs[t.ID]
		}
	}

	return tickets, total, nil
}

type scanner interface {
	Scan(dest ...any) error
}

func scanTicket(s scanner) (*Ticket, error) {
	t := &Ticket{}
	var (
		titleName        *string
		sourceBot        *string
		domain           *string
		agentCtxBytes    []byte
		metaBytes        []byte
		specBytes        []byte
		callbackURL      *string
		tagsBytes        []byte
		cancellationNote *string
	)

	err := s.Scan(
		&t.ID, &titleName, &t.Title, &t.Description, &t.Type, &t.Status, &t.Priority, &t.RiskTier,
		&t.AssigneeType, &t.AssigneeID, &t.RequesterID, &sourceBot,
		&domain, &agentCtxBytes, &metaBytes,
		&t.ProjectID, &t.ParentID, &t.Visibility, &t.CompletionMode,
		&specBytes, &t.RewardXP, &t.DueAt, &callbackURL,
		&t.CreatedAt, &t.UpdatedAt, &t.CompletedAt, &cancellationNote,
		&tagsBytes,
		&t.ChildCount,
		&t.ProjectName, &t.WorkspaceName, &t.ParentTitle,
	)
	if err != nil {
		return nil, err
	}

	if titleName != nil {
		t.TitleName = *titleName
	}
	if sourceBot != nil {
		t.SourceBot = *sourceBot
	}
	if cancellationNote != nil {
		t.CancellationNote = cancellationNote
	}
	if domain != nil {
		t.Domain = Domain(*domain)
	}
	if callbackURL != nil {
		t.CallbackURL = *callbackURL
	}
	if len(agentCtxBytes) > 0 {
		if err := json.Unmarshal(agentCtxBytes, &t.AgentContext); err != nil {
			return nil, fmt.Errorf("unmarshal agent_context: %w", err)
		}
	}
	if len(metaBytes) > 0 {
		if err := json.Unmarshal(metaBytes, &t.Metadata); err != nil {
			return nil, fmt.Errorf("unmarshal metadata: %w", err)
		}
	}
	if len(specBytes) > 0 {
		if err := json.Unmarshal(specBytes, &t.CompletionSpec); err != nil {
			return nil, fmt.Errorf("unmarshal completion_spec: %w", err)
		}
	}
	if len(tagsBytes) > 0 && string(tagsBytes) != "[]" {
		if err := json.Unmarshal(tagsBytes, &t.Tags); err != nil {
			return nil, fmt.Errorf("unmarshal tags: %w", err)
		}
	}

	return t, nil
}

// valid transitions; terminal states have no outgoing edges
var transitions = map[Status][]Status{
	StatusPending:         {StatusAssigned, StatusCancelled, StatusPendingApproval},
	StatusAssigned:        {StatusInProgress, StatusPending, StatusCancelled, StatusPendingApproval},
	StatusInProgress:      {StatusCompleted, StatusFailed, StatusPending, StatusPendingApproval},
	StatusCompleted:       {},
	StatusFailed:          {StatusPending},
	StatusCancelled:       {},
	StatusPendingApproval: {StatusAssigned, StatusCancelled, StatusPendingRevision},
	StatusPendingRevision: {StatusInProgress, StatusCancelled, StatusPendingApproval},
}

func validateTransition(from, to Status) error {
	allowed, ok := transitions[from]
	if !ok {
		return fmt.Errorf("unknown status: %s", from)
	}
	for _, s := range allowed {
		if s == to {
			return nil
		}
	}
	return fmt.Errorf("%w: %s → %s", ErrInvalidTransition, from, to)
}

func marshalNullableJSON(v any) ([]byte, error) {
	if v == nil {
		return nil, nil
	}
	return json.Marshal(v)
}

// isUniqueViolation returns true when err is a PostgreSQL unique_violation (23505)
// on the named constraint.
func isUniqueViolation(err error, constraintName string) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505" && pgErr.ConstraintName == constraintName
}

// isUniqueViolationAny returns true when err is any PostgreSQL unique_violation
// (23505), regardless of which constraint was violated. Used by refs methods
// where the only unique key is the primary key (ticket_id, gopedia_l1_id).
func isUniqueViolationAny(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}

var (
	ErrNotFound           = errors.New("ticket not found")
	ErrInvalidTransition  = errors.New("invalid status transition")
	ErrDuplicateTitleName = errors.New("title_name already exists in this project")
	ErrInvalidGroupBy     = errors.New("invalid group_by")
	ErrProjectNotFound    = errors.New("target project not found")
	ErrCrossWorkspaceMove = errors.New("target project is in a different workspace")
)

func (s *pgStore) ListSubtasks(ctx context.Context, parentID uuid.UUID) ([]*Ticket, error) {
	q := `
		WITH RECURSIVE subtree AS (
			SELECT t.id FROM tickets t WHERE t.parent_id = $1
			UNION ALL
			SELECT t.id FROM tickets t JOIN subtree st ON t.parent_id = st.id
		)
		` + ticketSelectSQL + `
		JOIN subtree st ON t.id = st.id
		` + ticketGroupBySQL + `
		ORDER BY t.created_at ASC`

	rows, err := s.db.Query(ctx, q, parentID)
	if err != nil {
		return nil, fmt.Errorf("list subtasks: %w", err)
	}
	defer rows.Close()

	var list []*Ticket
	for rows.Next() {
		t, err := scanTicket(rows)
		if err != nil {
			return nil, err
		}
		list = append(list, t)
	}
	return list, rows.Err()
}

func (s *pgStore) AllSubtasksTerminal(ctx context.Context, parentID uuid.UUID) (bool, error) {
	var count int
	err := s.db.QueryRow(ctx, `
		SELECT COUNT(*) FROM tickets
		WHERE parent_id = $1 AND status NOT IN ('completed','cancelled','failed')`,
		parentID,
	).Scan(&count)
	if err != nil {
		return false, fmt.Errorf("all subtasks terminal: %w", err)
	}
	return count == 0, nil
}

func (s *pgStore) SetTicketTags(ctx context.Context, ticketID uuid.UUID, tagIDs []uuid.UUID) error {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback(ctx)

	if _, err := tx.Exec(ctx, "DELETE FROM ticket_tags WHERE ticket_id=$1", ticketID); err != nil {
		return fmt.Errorf("delete ticket_tags: %w", err)
	}
	for _, tagID := range tagIDs {
		if _, err := tx.Exec(ctx,
			"INSERT INTO ticket_tags (ticket_id, tag_id) VALUES ($1,$2) ON CONFLICT DO NOTHING",
			ticketID, tagID,
		); err != nil {
			return fmt.Errorf("insert ticket_tag: %w", err)
		}
	}
	return tx.Commit(ctx)
}

func (s *pgStore) CreateAttempt(ctx context.Context, a *Attempt) error {
	_, err := s.db.Exec(ctx, `
		INSERT INTO ticket_attempts (id, ticket_id, user_id, answer, is_correct, ai_feedback, attempted_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7)`,
		a.ID, a.TicketID, a.UserID, a.Answer, a.IsCorrect, a.AIFeedback, a.AttemptedAt,
	)
	if err != nil {
		return fmt.Errorf("create attempt: %w", err)
	}
	return nil
}

func (s *pgStore) ListAttempts(ctx context.Context, ticketID uuid.UUID) ([]*Attempt, error) {
	rows, err := s.db.Query(ctx, `
		SELECT id, ticket_id, user_id, answer, is_correct, ai_feedback, attempted_at
		FROM ticket_attempts WHERE ticket_id=$1 ORDER BY attempted_at ASC`,
		ticketID,
	)
	if err != nil {
		return nil, fmt.Errorf("list attempts: %w", err)
	}
	defer rows.Close()

	var list []*Attempt
	for rows.Next() {
		a := &Attempt{}
		if err := rows.Scan(&a.ID, &a.TicketID, &a.UserID, &a.Answer, &a.IsCorrect, &a.AIFeedback, &a.AttemptedAt); err != nil {
			return nil, fmt.Errorf("scan attempt: %w", err)
		}
		list = append(list, a)
	}
	return list, rows.Err()
}

func (s *pgStore) CountAttempts(ctx context.Context, ticketID uuid.UUID, userID string) (int, error) {
	var count int
	err := s.db.QueryRow(ctx,
		"SELECT COUNT(*) FROM ticket_attempts WHERE ticket_id=$1 AND user_id=$2",
		ticketID, userID,
	).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("count attempts: %w", err)
	}
	return count, nil
}

func (s *pgStore) ListOverdue(ctx context.Context) ([]*Ticket, error) {
	rows, err := s.db.Query(ctx,
		ticketSelectSQL+
			`WHERE t.due_at IS NOT NULL
			   AND t.due_at < NOW()
			   AND t.status NOT IN ('completed', 'cancelled', 'failed', 'pending_approval', 'pending_revision')
			`+ticketGroupBySQL)
	if err != nil {
		return nil, fmt.Errorf("list overdue: %w", err)
	}
	defer rows.Close()

	var tickets []*Ticket
	for rows.Next() {
		t, err := scanTicket(rows)
		if err != nil {
			return nil, err
		}
		tickets = append(tickets, t)
	}
	return tickets, rows.Err()
}

// --- Refs ---

func (s *pgStore) AddRefs(ctx context.Context, ticketID uuid.UUID, gopediaL1IDs []string, createdBy string) ([]TicketRef, error) {
	if len(gopediaL1IDs) == 0 {
		return nil, nil
	}

	tx, err := s.db.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	// Lock the ticket row to serialize concurrent ref appends and read a stable max(position).
	var lockID uuid.UUID
	if err := tx.QueryRow(ctx,
		`SELECT id FROM tickets WHERE id=$1 FOR UPDATE`, ticketID,
	).Scan(&lockID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("lock ticket: %w", err)
	}

	var maxPos *int
	if err := tx.QueryRow(ctx,
		`SELECT MAX(position) FROM ticket_refs WHERE ticket_id=$1`, ticketID,
	).Scan(&maxPos); err != nil {
		return nil, fmt.Errorf("max pos: %w", err)
	}
	nextPos := 0
	if maxPos != nil {
		nextPos = *maxPos + 1
	}

	out := make([]TicketRef, 0, len(gopediaL1IDs))
	for i, id := range gopediaL1IDs {
		var r TicketRef
		err := tx.QueryRow(ctx, `
			INSERT INTO ticket_refs (ticket_id, gopedia_l1_id, position, created_by)
			VALUES ($1, $2, $3, $4)
			RETURNING ticket_id, gopedia_l1_id, position, created_at, created_by`,
			ticketID, id, nextPos+i, createdBy,
		).Scan(&r.TicketID, &r.GopediaL1ID, &r.Position, &r.CreatedAt, &r.CreatedBy)
		if err != nil {
			if isUniqueViolationAny(err) {
				return nil, ErrRefConflict
			}
			return nil, fmt.Errorf("insert ref %s: %w", id, err)
		}
		out = append(out, r)
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit: %w", err)
	}
	return out, nil
}

func (s *pgStore) ListRefs(ctx context.Context, ticketID uuid.UUID) ([]TicketRef, error) {
	rows, err := s.db.Query(ctx, `
		SELECT ticket_id, gopedia_l1_id, position, created_at, created_by
		  FROM ticket_refs
		 WHERE ticket_id=$1
		 ORDER BY position ASC`, ticketID)
	if err != nil {
		return nil, fmt.Errorf("list refs: %w", err)
	}
	defer rows.Close()
	var out []TicketRef
	for rows.Next() {
		var r TicketRef
		if err := rows.Scan(&r.TicketID, &r.GopediaL1ID, &r.Position, &r.CreatedAt, &r.CreatedBy); err != nil {
			return nil, fmt.Errorf("scan ref: %w", err)
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (s *pgStore) RemoveRef(ctx context.Context, ticketID uuid.UUID, gopediaL1ID string) error {
	_, err := s.db.Exec(ctx,
		`DELETE FROM ticket_refs WHERE ticket_id=$1 AND gopedia_l1_id=$2`,
		ticketID, gopediaL1ID)
	if err != nil {
		return fmt.Errorf("delete ref: %w", err)
	}
	return nil
}

func (s *pgStore) ReorderRefs(ctx context.Context, ticketID uuid.UUID, orderedL1IDs []string) error {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	// Verify the proposed set matches the existing set exactly.
	rows, err := tx.Query(ctx,
		`SELECT gopedia_l1_id FROM ticket_refs WHERE ticket_id=$1 FOR UPDATE`, ticketID)
	if err != nil {
		return fmt.Errorf("select existing refs: %w", err)
	}
	existing := map[string]bool{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return fmt.Errorf("scan existing ref: %w", err)
		}
		existing[id] = true
	}
	rows.Close()

	if len(existing) != len(orderedL1IDs) {
		return fmt.Errorf("reorder: id count mismatch (existing=%d, given=%d)", len(existing), len(orderedL1IDs))
	}
	seen := map[string]bool{}
	for _, id := range orderedL1IDs {
		if !existing[id] {
			return fmt.Errorf("reorder: %s not in existing set", id)
		}
		if seen[id] {
			return fmt.Errorf("reorder: %s duplicated", id)
		}
		seen[id] = true
	}

	for i, id := range orderedL1IDs {
		if _, err := tx.Exec(ctx,
			`UPDATE ticket_refs SET position=$1 WHERE ticket_id=$2 AND gopedia_l1_id=$3`,
			i, ticketID, id); err != nil {
			return fmt.Errorf("update ref position: %w", err)
		}
	}
	return tx.Commit(ctx)
}

func (s *pgStore) CountRefs(ctx context.Context, ticketID uuid.UUID) (int, error) {
	var n int
	err := s.db.QueryRow(ctx,
		`SELECT COUNT(*) FROM ticket_refs WHERE ticket_id=$1`, ticketID).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("count refs: %w", err)
	}
	return n, nil
}

func (s *pgStore) ListTicketIDsByRef(ctx context.Context, gopediaL1ID string) ([]uuid.UUID, error) {
	rows, err := s.db.Query(ctx,
		`SELECT ticket_id FROM ticket_refs WHERE gopedia_l1_id=$1`, gopediaL1ID)
	if err != nil {
		return nil, fmt.Errorf("list ticket ids by ref: %w", err)
	}
	defer rows.Close()
	var out []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("scan ticket id: %w", err)
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

func (s *pgStore) ListRefIDsForTickets(ctx context.Context, ticketIDs []uuid.UUID) (map[uuid.UUID][]string, error) {
	if len(ticketIDs) == 0 {
		return map[uuid.UUID][]string{}, nil
	}
	rows, err := s.db.Query(ctx, `
		SELECT ticket_id, gopedia_l1_id
		  FROM ticket_refs
		 WHERE ticket_id = ANY($1)
		 ORDER BY ticket_id, position ASC`, ticketIDs)
	if err != nil {
		return nil, fmt.Errorf("list ref ids for tickets: %w", err)
	}
	defer rows.Close()
	out := map[uuid.UUID][]string{}
	for rows.Next() {
		var tid uuid.UUID
		var rid string
		if err := rows.Scan(&tid, &rid); err != nil {
			return nil, fmt.Errorf("scan ref row: %w", err)
		}
		out[tid] = append(out[tid], rid)
	}
	return out, rows.Err()
}
