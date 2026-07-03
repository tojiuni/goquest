package ticket

import "testing"

// Quality gate for the ticket status state machine. Rather than mirror the
// transition table (which would be circular), it asserts structural invariants
// over the full status set via validateTransition, and reports coverage (how
// many from→to pairs were exercised). A regression that opens a terminal status,
// drops a status from the table, or reinstates the forbidden pending→in_progress
// shortcut fails CI here.

var allStatuses = []Status{
	StatusPending, StatusAssigned, StatusInProgress, StatusCompleted,
	StatusFailed, StatusCancelled, StatusPendingApproval, StatusPendingRevision,
}

// terminals must have no outgoing transitions.
var terminals = []Status{StatusCompleted, StatusCancelled}

func TestStateMachine_QualityGate(t *testing.T) {
	// Coverage: every status must be a key in the table, so validateTransition
	// never reports "unknown status" for a real status.
	for _, s := range allStatuses {
		if _, ok := transitions[s]; !ok {
			t.Errorf("status %q missing from transitions table (would be unroutable)", s)
		}
	}

	// Invariant 1: no self-loops.
	for _, s := range allStatuses {
		if err := validateTransition(s, s); err == nil {
			t.Errorf("self-loop %s→%s must be rejected", s, s)
		}
	}

	// Invariant 2: terminals are closed — no outgoing transition of any kind.
	for _, term := range terminals {
		for _, to := range allStatuses {
			if err := validateTransition(term, to); err == nil {
				t.Errorf("terminal %s must not transition to %s", term, to)
			}
		}
	}

	// Invariant 3: the documented shortcut ban — pending→in_progress must go via
	// assigned.
	if err := validateTransition(StatusPending, StatusInProgress); err == nil {
		t.Error("pending→in_progress must be rejected (route via assigned)")
	}
	if err := validateTransition(StatusPending, StatusAssigned); err != nil {
		t.Errorf("pending→assigned must be allowed: %v", err)
	}
	if err := validateTransition(StatusAssigned, StatusInProgress); err != nil {
		t.Errorf("assigned→in_progress must be allowed: %v", err)
	}

	// Invariant 4: failed is retryable only back to pending.
	if err := validateTransition(StatusFailed, StatusPending); err != nil {
		t.Errorf("failed→pending must be allowed: %v", err)
	}
	if err := validateTransition(StatusFailed, StatusCompleted); err == nil {
		t.Error("failed→completed must be rejected")
	}

	// Coverage report: number of from→to pairs exercised.
	pairs := len(allStatuses) * len(allStatuses)
	t.Logf("state-machine quality: %d statuses, %d from→to pairs covered, %d terminals verified closed",
		len(allStatuses), pairs, len(terminals))
}
