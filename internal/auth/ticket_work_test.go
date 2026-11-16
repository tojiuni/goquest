package auth_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/google/uuid"
	"github.com/tojiuni/goquest/internal/auth"
	"github.com/tojiuni/goquest/internal/spicedb"
	"github.com/tojiuni/goquest/internal/ticket"
)

// roleChecker models the schema: can_work = editor or manager, and manager
// implies work. Every permission it is not asked about is denied.
type roleChecker struct {
	*spicedb.NoopChecker
	editor, manager bool
	err             error
	asked           []string
}

func (c *roleChecker) Check(_ context.Context, objType, _, permission, _ string) (bool, error) {
	c.asked = append(c.asked, objType+"#"+permission)
	if c.err != nil {
		return false, c.err
	}
	if permission == spicedb.PermCanWork {
		return c.editor || c.manager, nil
	}
	return false, nil
}

func TestRequireTicketWork(t *testing.T) {
	id, pid := uuid.New(), uuid.New()
	tk := &ticket.Ticket{ID: id, ProjectID: &pid, Visibility: ticket.VisibilityProject}
	run := func(c *roleChecker, getter stubGetter) int {
		return serve(t, auth.RequireTicketWork(getter, c), reqWithUser(id, "u")).Code
	}

	if got := run(&roleChecker{editor: true}, stubGetter{t: tk}); got != http.StatusOK {
		t.Errorf("editor: want 200, got %d", got)
	}
	if got := run(&roleChecker{manager: true}, stubGetter{t: tk}); got != http.StatusOK {
		t.Errorf("manager works too: want 200, got %d", got)
	}
	if got := run(&roleChecker{}, stubGetter{t: tk}); got != http.StatusForbidden {
		t.Errorf("a plain member can_view only: want 403, got %d", got)
	}
	if got := run(&roleChecker{editor: true, err: context.DeadlineExceeded}, stubGetter{t: tk}); got != http.StatusInternalServerError {
		t.Errorf("checker error must deny (fail-closed): want 500, got %d", got)
	}
	if got := run(&roleChecker{editor: true}, stubGetter{err: ticket.ErrNotFound}); got != http.StatusNotFound {
		t.Errorf("unknown ticket: want 404, got %d", got)
	}
	// legacy ticket with no project has no SpiceDB gate, same as RequireTicketManage
	if got := run(&roleChecker{}, stubGetter{t: &ticket.Ticket{ID: id}}); got != http.StatusOK {
		t.Errorf("legacy ticket: want 200, got %d", got)
	}
	// shadow mode (NoopChecker grants) stays permissive
	if got := serve(t, auth.RequireTicketWork(stubGetter{t: tk}, &spicedb.NoopChecker{}), reqWithUser(id, "u")).Code; got != http.StatusOK {
		t.Errorf("shadow: want 200, got %d", got)
	}
	// it asks for exactly can_work on the ticket type, not manage
	c := &roleChecker{editor: true}
	run(c, stubGetter{t: tk})
	if len(c.asked) != 1 || c.asked[0] != spicedb.TypeTicket+"#can_work" {
		t.Errorf("asked %v, want one %s#can_work", c.asked, spicedb.TypeTicket)
	}
}
