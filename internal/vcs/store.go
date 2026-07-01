package vcs

import (
	"context"
	"errors"

	"github.com/google/uuid"
)

var ErrNotFound = errors.New("vcs: not found")

type Store interface {
	CreateRepo(ctx context.Context, repo *VcsRepo) error
	GetRepo(ctx context.Context, provider, repoFullName string) (*VcsRepo, error)
	ListRepos(ctx context.Context) ([]*VcsRepo, error)
	UpsertLink(ctx context.Context, l *VcsLink) (*VcsLink, error)
	GetBranchLink(ctx context.Context, provider, repoFullName, branch string) (*VcsLink, error)
	LinksForTicket(ctx context.Context, ticketID uuid.UUID) ([]VcsLink, error)
}
