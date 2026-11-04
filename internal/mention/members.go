package mention

import (
	"context"
	"log"

	"github.com/google/uuid"
)

type projectOnlyKey struct{}

// WithProjectMembersOnly narrows mention eligibility to explicit project
// members for this call. Used for restricted tickets, which a plain workspace
// member cannot open: notifying them would reveal that the ticket exists.
func WithProjectMembersOnly(ctx context.Context) context.Context {
	return context.WithValue(ctx, projectOnlyKey{}, true)
}

// IsProjectMembersOnly reports whether ctx was narrowed by WithProjectMembersOnly.
func IsProjectMembersOnly(ctx context.Context) bool {
	v, _ := ctx.Value(projectOnlyKey{}).(bool)
	return v
}

// EligibleLister decides who may be @mentioned on a ticket: the project's
// explicit members plus, unless the call is narrowed with
// WithProjectMembersOnly, the members of the project's workspace.
//
// Access to tickets in goquest is granted at the workspace level (people and
// session accounts are added to the workspace, not one by one to each
// project), so project_members alone is almost always empty and would silently
// drop nearly every mention.
type EligibleLister struct {
	Project     func(ctx context.Context, projectID uuid.UUID) ([]string, error)
	WorkspaceOf func(ctx context.Context, projectID uuid.UUID) (uuid.UUID, error)
	Workspace   func(ctx context.Context, workspaceID uuid.UUID) ([]string, error)
}

// ListMembers implements MemberLister.
func (l EligibleLister) ListMembers(ctx context.Context, projectID uuid.UUID) ([]*ProjectMember, error) {
	ids, err := l.Project(ctx, projectID)
	if err != nil {
		return nil, err
	}
	seen := make(map[string]bool, len(ids))
	var out []*ProjectMember
	add := func(id string) {
		if id != "" && !seen[id] {
			seen[id] = true
			out = append(out, &ProjectMember{UserID: id})
		}
	}
	for _, id := range ids {
		add(id)
	}
	if IsProjectMembersOnly(ctx) || l.WorkspaceOf == nil || l.Workspace == nil {
		return out, nil
	}
	// Fail toward fewer mentions: if the workspace cannot be read, only the
	// explicit project members are eligible rather than failing the comment.
	wid, err := l.WorkspaceOf(ctx, projectID)
	if err != nil {
		log.Printf("mention: resolve project workspace: %v", err)
		return out, nil
	}
	wids, err := l.Workspace(ctx, wid)
	if err != nil {
		log.Printf("mention: list workspace members: %v", err)
		return out, nil
	}
	for _, id := range wids {
		add(id)
	}
	return out, nil
}
