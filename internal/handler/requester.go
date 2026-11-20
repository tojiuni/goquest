package handler

import (
	"context"
	"net/http"
	"strings"

	"github.com/google/uuid"
	"github.com/tojiuni/goquest/internal/auth"
	"github.com/tojiuni/goquest/internal/directory"
	"github.com/tojiuni/goquest/internal/project"
	"github.com/tojiuni/goquest/internal/ticket"
	"github.com/tojiuni/goquest/internal/workspace"
)

type requesterIdentity struct {
	ActorID     string `json:"actor_id"`
	DisplayName string `json:"display_name"`
	Email       string `json:"email,omitempty"`
}

type requesterProjects interface {
	Get(context.Context, uuid.UUID) (*project.Project, error)
}
type requesterMembers interface {
	GetMember(context.Context, uuid.UUID, string) (*workspace.Member, error)
}
type requesterTenantAccounts interface {
	ResolveRequester(context.Context, uuid.UUID, string) (*directory.Actor, error)
}
type tenantRequesterAccounts struct{ lookup directory.Lookuper }

func NewTenantRequesterAccounts(host string, lookup directory.Lookuper) requesterTenantAccounts {
	if strings.TrimSpace(host) == "" || lookup == nil {
		return nil
	}
	return tenantRequesterAccounts{lookup: lookup}
}

func (a tenantRequesterAccounts) ResolveRequester(ctx context.Context, _ uuid.UUID, raw string) (*directory.Actor, error) {
	actors, err := a.lookup.Lookup(ctx, []string{raw})
	if err != nil {
		return nil, err
	}
	if len(actors) != 1 || actors[0].ID == "" || (actors[0].ID != raw && !strings.EqualFold(actors[0].Email, raw)) {
		return nil, nil
	}
	return &actors[0], nil
}

type RequesterResolver struct {
	projects requesterProjects
	members  requesterMembers
	accounts requesterTenantAccounts
}

func NewRequesterResolver(p requesterProjects, m requesterMembers, d requesterTenantAccounts) *RequesterResolver {
	return &RequesterResolver{p, m, d}
}

func (rr *RequesterResolver) resolve(r *http.Request, t *ticket.Ticket, raw string) requesterIdentity {
	result := requesterIdentity{DisplayName: raw}
	if rr == nil || rr.accounts == nil || t == nil || t.ProjectID == nil || rr.projects == nil {
		return result
	}
	p, err := rr.projects.Get(r.Context(), *t.ProjectID)
	if err != nil || p == nil {
		return result
	}
	a, err := rr.accounts.ResolveRequester(r.Context(), p.WorkspaceID, raw)
	if err != nil || a == nil || a.ID == "" {
		return result
	}
	result.ActorID = a.ID
	if a.DisplayName != "" {
		result.DisplayName = a.DisplayName
	}
	if rr.members != nil && auth.RequesterID(r) != "" {
		member, err := rr.members.GetMember(r.Context(), p.WorkspaceID, auth.RequesterID(r))
		if err == nil && member != nil {
			result.Email = a.Email
		}
	}
	return result
}
