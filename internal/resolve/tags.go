package resolve

import (
	"context"

	"github.com/google/uuid"
	"github.com/tojiuni/goquest/internal/project"
	"github.com/tojiuni/goquest/internal/tag"
)

type projectGetter interface {
	Get(ctx context.Context, id uuid.UUID) (*project.Project, error)
}

type tagGetOrCreator interface {
	GetOrCreate(ctx context.Context, workspaceID uuid.UUID, creatorID, name string) (*tag.Tag, error)
}

// TagResolver turns tag names into tag UUIDs within the workspace that owns a
// project, creating missing tags on demand.
type TagResolver struct {
	pj  projectGetter
	tag tagGetOrCreator
}

func NewTagResolver(pj projectGetter, t tagGetOrCreator) *TagResolver {
	return &TagResolver{pj: pj, tag: t}
}

// ResolveTagIDs resolves names to tag IDs in the project's workspace. Names are
// get-or-created; duplicate names (case-insensitively producing the same tag)
// collapse to a single ID. An empty/nil names slice returns an empty result.
func (r *TagResolver) ResolveTagIDs(ctx context.Context, projectID uuid.UUID, creatorID string, names []string) ([]uuid.UUID, error) {
	if len(names) == 0 {
		return nil, nil
	}
	proj, err := r.pj.Get(ctx, projectID)
	if err != nil {
		return nil, err
	}
	seen := map[uuid.UUID]struct{}{}
	out := make([]uuid.UUID, 0, len(names))
	for _, name := range names {
		t, terr := r.tag.GetOrCreate(ctx, proj.WorkspaceID, creatorID, name)
		if terr != nil {
			return nil, terr
		}
		if _, dup := seen[t.ID]; dup {
			continue
		}
		seen[t.ID] = struct{}{}
		out = append(out, t.ID)
	}
	return out, nil
}
