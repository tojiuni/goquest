package ticket_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/tojiuni/goquest/internal/ticket"
)

func TestValidateSort_KnownFields(t *testing.T) {
	for _, f := range []string{"created_at", "updated_at", "due_at", "priority",
		"title", "status", "type", "domain", "assignee_id"} {
		if err := ticket.ValidateSortField(f); err != nil {
			t.Errorf("expected %q to be allowed, got %v", f, err)
		}
	}
}

func TestValidateSort_RejectsArbitrary(t *testing.T) {
	err := ticket.ValidateSortField("password")
	assert.ErrorIs(t, err, ticket.ErrInvalidSortField)
}

func TestValidateSort_EmptyAllowed(t *testing.T) {
	err := ticket.ValidateSortField("")
	assert.NoError(t, err)
}
