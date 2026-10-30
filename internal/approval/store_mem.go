package approval

import (
	"context"
	"sort"
	"sync"
	"time"

	"github.com/google/uuid"
)

// MemStore is an in-memory Store used by service and handler tests.
type MemStore struct {
	mu     sync.Mutex
	rows   map[uuid.UUID]*Approval
	events map[uuid.UUID][]Event
	order  []uuid.UUID
}

func NewMemStore() *MemStore {
	return &MemStore{rows: map[uuid.UUID]*Approval{}, events: map[uuid.UUID][]Event{}}
}

func (m *MemStore) Create(_ context.Context, req NewReq) (*Approval, error) {
	if err := req.Validate(); err != nil {
		return nil, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, id := range m.order {
		o := m.rows[id]
		if o.Status == StatusPending && o.TicketID == req.TicketID && o.Kind == req.Kind && o.Subject == req.Subject {
			return nil, ErrDuplicatePending
		}
	}
	if req.Kind == KindMerge && req.SupersedesID == nil {
		ids, err := m.supersedeOpenMerge(req.TicketID, req.Subject.Repo, req.Subject.PR, req.Subject.HeadSHA, req.RequestedBy)
		if err != nil {
			return nil, err
		}
		if len(ids) > 0 {
			req.SupersedesID = &ids[0]
		}
	}
	now := time.Now().UTC()
	a := &Approval{
		ID: uuid.New(), TicketID: req.TicketID, Kind: req.Kind, Subject: req.Subject,
		Status: StatusPending, RequestedBy: req.RequestedBy, RequestedByLabel: req.RequestedByLabel,
		RequestedAt: now, RequestNote: req.RequestNote, Evidence: append([]EvidenceItem{}, req.Evidence...),
		WithinDirection: req.WithinDirection, SupersedesID: req.SupersedesID, Legacy: req.Legacy,
		SourceCommentID: req.SourceCommentID, CreatedAt: now,
	}
	m.rows[a.ID] = a
	m.order = append(m.order, a.ID)
	return clone(a, nil), nil
}

func (m *MemStore) Get(_ context.Context, id uuid.UUID) (*Approval, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	a, ok := m.rows[id]
	if !ok {
		return nil, ErrNotFound
	}
	return clone(a, m.events[id]), nil
}

func (m *MemStore) List(_ context.Context, f ListFilter) ([]*Approval, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []*Approval
	for _, id := range m.order {
		a := m.rows[id]
		if f.TicketID != nil && a.TicketID != *f.TicketID {
			continue
		}
		if f.Status != "" && a.Status != f.Status {
			continue
		}
		if f.Kind != "" && a.Kind != f.Kind {
			continue
		}
		if f.Repo != "" && a.Subject.Repo != f.Repo {
			continue
		}
		if f.RequestedBy != "" && a.RequestedBy != f.RequestedBy {
			continue
		}
		out = append(out, clone(a, nil))
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	return page(out, f.Offset, f.Limit), nil
}

func (m *MemStore) Transition(_ context.Context, id uuid.UUID, from, to Status, actor, note string, d *Decision) (*Approval, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.transition(id, from, to, actor, note, d)
}

func (m *MemStore) transition(id uuid.UUID, from, to Status, actor, note string, d *Decision) (*Approval, error) {
	a, ok := m.rows[id]
	if !ok {
		return nil, ErrNotFound
	}
	if !CanTransition(from, to) {
		return nil, ErrInvalidTransition
	}
	if a.Status != from {
		return nil, ErrStale
	}
	now := time.Now().UTC()
	if d != nil {
		by, basis, n := d.DecidedBy, d.Basis, d.Note
		a.DecidedBy, a.Basis, a.DecisionNote, a.DecidedAt = &by, &basis, &n, &now
		a.DelegationRuleID = d.DelegationRuleID
		if d.UserRef != "" {
			u := d.UserRef
			a.UserRef = &u
		}
	}
	a.Status = to
	m.events[id] = append(m.events[id], Event{ID: uuid.New(), ApprovalID: id, From: from, To: to, Actor: actor, Note: note, CreatedAt: now})
	return clone(a, m.events[id]), nil
}

func (m *MemStore) SupersedeOpenMerge(_ context.Context, ticketID uuid.UUID, repo string, pr int, newHeadSHA, actor string) ([]uuid.UUID, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.supersedeOpenMerge(ticketID, repo, pr, newHeadSHA, actor)
}

func (m *MemStore) supersedeOpenMerge(ticketID uuid.UUID, repo string, pr int, newHeadSHA, actor string) ([]uuid.UUID, error) {
	var ids []uuid.UUID
	for _, id := range m.order {
		a := m.rows[id]
		if a.TicketID != ticketID || a.Kind != KindMerge || a.Subject.Repo != repo || a.Subject.PR != pr {
			continue
		}
		if a.Subject.HeadSHA == newHeadSHA || (a.Status != StatusPending && a.Status != StatusApproved) {
			continue
		}
		if _, err := m.transition(id, a.Status, StatusSuperseded, actor, "head_sha changed", nil); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, nil
}

func clone(a *Approval, ev []Event) *Approval {
	c := *a
	c.Evidence = append([]EvidenceItem{}, a.Evidence...)
	c.Events = append([]Event(nil), ev...)
	return &c
}

func page(in []*Approval, offset, limit int) []*Approval {
	if offset > len(in) {
		return nil
	}
	in = in[offset:]
	if limit > 0 && limit < len(in) {
		in = in[:limit]
	}
	return in
}
