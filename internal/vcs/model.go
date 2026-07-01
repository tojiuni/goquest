package vcs

import (
	"time"

	"github.com/google/uuid"
)

type EventKind string

const (
	BranchCreated EventKind = "branch_created"
	BranchDeleted EventKind = "branch_deleted"
	CommitPushed  EventKind = "commit_pushed"
	PROpened      EventKind = "pr_opened"
	PRUpdated     EventKind = "pr_updated"
	PRMerged      EventKind = "pr_merged"
	PRClosed      EventKind = "pr_closed"
)

type LinkKind string

const (
	KindBranch LinkKind = "branch"
	KindCommit LinkKind = "commit"
	KindPR     LinkKind = "pull_request"
)

type LinkState string

const (
	StateOpen    LinkState = "open"
	StateMerged  LinkState = "merged"
	StateClosed  LinkState = "closed"
	StateDeleted LinkState = "deleted"
)

// VcsEvent is the provider-agnostic normalized form a Provider.Parse emits.
type VcsEvent struct {
	Kind      EventKind
	Repo      string // full name, e.g. "tojiuni/goquest"
	Branch    string // short ref name
	CommitSHA string
	CommitMsg string
	PRNumber  int
	PRTitle   string
	PRBody    string
	PRState   string
	PRMerged  bool
	PRBaseRef string // PR base/target branch (head/source ref is carried in Branch)
	URL       string
	Actor     string
}

// VcsRepo maps a git repo to a goquest project for title_name resolution.
type VcsRepo struct {
	ID             uuid.UUID `json:"id"`
	Provider       string    `json:"provider"`
	RepoFullName   string    `json:"repo_full_name"`
	ProjectID      uuid.UUID `json:"project_id"`
	AutoTransition bool      `json:"auto_transition"`
	MergeTarget    string    `json:"merge_target"` // "completed" | "pending_approval"
	Active         bool      `json:"active"`
	CreatedAt      time.Time `json:"created_at"`
}

// VcsLink is one git object (branch/commit/PR) linked to a ticket.
type VcsLink struct {
	ID           uuid.UUID `json:"id"`
	TicketID     uuid.UUID `json:"ticket_id"`
	Provider     string    `json:"provider"`
	RepoFullName string    `json:"repo_full_name"`
	Kind         LinkKind  `json:"kind"`
	ExternalRef  string    `json:"external_ref"`
	URL          string    `json:"url"`
	State        LinkState `json:"state"`
	Title        string    `json:"title"`
	SourceBranch string    `json:"source_branch,omitempty"`
	TargetBranch string    `json:"target_branch,omitempty"`
	Actor        string    `json:"actor"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}
