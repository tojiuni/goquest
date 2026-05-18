package ticket

import (
	"testing"
)

func TestValidateTransition(t *testing.T) {
	cases := []struct {
		from    Status
		to      Status
		wantErr bool
	}{
		// valid transitions
		{StatusPending, StatusAssigned, false},
		{StatusPending, StatusCancelled, false},
		{StatusAssigned, StatusInProgress, false},
		{StatusAssigned, StatusPending, false},
		{StatusAssigned, StatusCancelled, false},
		{StatusInProgress, StatusCompleted, false},
		{StatusInProgress, StatusFailed, false},
		{StatusInProgress, StatusPending, false},
		{StatusFailed, StatusPending, false},
		// terminal → anything: error
		{StatusCompleted, StatusPending, true},
		{StatusCompleted, StatusCancelled, true},
		{StatusCancelled, StatusPending, true},
		{StatusCancelled, StatusAssigned, true},
		// invalid forward leap
		{StatusPending, StatusCompleted, true},
		{StatusPending, StatusFailed, true},
		{StatusAssigned, StatusCompleted, true},
		// new pending_approval / pending_revision transitions
		{StatusPending, StatusPendingApproval, false},
		{StatusAssigned, StatusPendingApproval, false},
		{StatusInProgress, StatusPendingApproval, false},
		{StatusPendingApproval, StatusAssigned, false},
		{StatusPendingApproval, StatusCancelled, false},
		{StatusPendingApproval, StatusPendingRevision, false},
		{StatusPendingRevision, StatusInProgress, false},
		{StatusPendingRevision, StatusCancelled, false},
		{StatusPendingRevision, StatusPendingApproval, false},
		// invalid from pending_approval (use Approve endpoint, not PATCH)
		{StatusPendingApproval, StatusPending, true},
		{StatusPendingApproval, StatusCompleted, true},
	}

	for _, tc := range cases {
		err := validateTransition(tc.from, tc.to)
		if tc.wantErr && err == nil {
			t.Errorf("validateTransition(%s→%s): expected error, got nil", tc.from, tc.to)
		}
		if !tc.wantErr && err != nil {
			t.Errorf("validateTransition(%s→%s): unexpected error: %v", tc.from, tc.to, err)
		}
	}
}
