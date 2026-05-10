package template

import (
	"context"
	"errors"

	"github.com/google/uuid"
)

var ErrNotFound = errors.New("template not found")

type Store interface {
	Create(ctx context.Context, t *Template) error
	List(ctx context.Context, projectID uuid.UUID) ([]*Template, error)
	Get(ctx context.Context, id uuid.UUID) (*Template, error)
	Delete(ctx context.Context, id uuid.UUID) error
}
