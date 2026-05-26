package handler

import (
	"testing"

	"github.com/google/uuid"
	"github.com/tojiuni/goquest/internal/ticket"
)

func TestVisibleSubtask(t *testing.T) {
	p := uuid.New() // project-visibility, in proj set
	r := uuid.New() // restricted-visibility, in restr set
	x := uuid.New() // project-visibility, NOT in proj set

	projSet := map[uuid.UUID]bool{p: true}
	restrSet := map[uuid.UUID]bool{r: true}

	cases := []struct {
		id   uuid.UUID
		vis  ticket.Visibility
		want bool
	}{
		{p, ticket.VisibilityProject, true},
		{r, ticket.VisibilityRestricted, true},
		{x, ticket.VisibilityProject, false},
		{r, ticket.VisibilityProject, false}, // restricted ticket not in proj set
	}
	for _, c := range cases {
		if got := visibleSubtask(c.id, c.vis, projSet, restrSet); got != c.want {
			t.Errorf("visibleSubtask(%s,%s)=%v want %v", c.id, c.vis, got, c.want)
		}
	}
}

func TestUUIDSet(t *testing.T) {
	a, b := uuid.New(), uuid.New()
	s := uuidSet([]uuid.UUID{a, b})
	if !s[a] || !s[b] || s[uuid.New()] {
		t.Errorf("uuidSet membership wrong: %v", s)
	}
}
