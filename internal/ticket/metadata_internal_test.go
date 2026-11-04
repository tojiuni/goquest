package ticket

import "testing"

func TestServerOwnedMetadata(t *testing.T) {
	from := func(m map[string]any) any { return m[MetaPendingApprovalFrom] }
	cur := func(status Status, meta map[string]any) *Ticket { return &Ticket{Status: status, Metadata: meta} }

	// entering pending_approval records where the ticket came from
	got := serverOwnedMetadata(cur(StatusInProgress, map[string]any{"k": 1}), false, map[string]any{"k": 1}, StatusPendingApproval)
	if from(got) != "in_progress" || got["k"] != 1 {
		t.Errorf("enter from in_progress: %v", got)
	}
	got = serverOwnedMetadata(cur(StatusAssigned, nil), false, nil, StatusPendingApproval)
	if from(got) != "assigned" {
		t.Errorf("enter from assigned with no metadata: %v", got)
	}
	// the caller's own map is never mutated
	in := map[string]any{"k": 1}
	serverOwnedMetadata(cur(StatusInProgress, in), false, in, StatusPendingApproval)
	if _, ok := in[MetaPendingApprovalFrom]; ok {
		t.Error("input map was mutated")
	}

	// a client replacing metadata cannot forge the key, or clear it while pending
	got = serverOwnedMetadata(cur(StatusAssigned, nil), true, map[string]any{MetaPendingApprovalFrom: "in_progress"}, StatusAssigned)
	if _, ok := got[MetaPendingApprovalFrom]; ok {
		t.Errorf("forged key kept: %v", got)
	}
	pend := cur(StatusPendingApproval, map[string]any{MetaPendingApprovalFrom: "assigned"})
	got = serverOwnedMetadata(pend, true, map[string]any{MetaPendingApprovalFrom: "in_progress", "x": 2}, StatusPendingApproval)
	if from(got) != "assigned" || got["x"] != 2 {
		t.Errorf("client changed the server-owned value: %v", got)
	}
	got = serverOwnedMetadata(pend, true, map[string]any{}, StatusPendingApproval)
	if from(got) != "assigned" {
		t.Errorf("client cleared the server-owned value: %v", got)
	}

	// leaving pending_approval drops it, except into pending_revision (the loop keeps the origin)
	for _, to := range []Status{StatusAssigned, StatusCompleted, StatusCancelled} {
		if got := serverOwnedMetadata(pend, false, pend.Metadata, to); got != nil {
			if _, ok := got[MetaPendingApprovalFrom]; ok {
				t.Errorf("leaving to %s must drop the key: %v", to, got)
			}
		}
	}
	got = serverOwnedMetadata(pend, false, pend.Metadata, StatusPendingRevision)
	if from(got) != "assigned" {
		t.Errorf("pending_revision keeps the origin: %v", got)
	}
	rev := cur(StatusPendingRevision, map[string]any{MetaPendingApprovalFrom: "in_progress"})
	got = serverOwnedMetadata(rev, false, rev.Metadata, StatusPendingApproval)
	if from(got) != "in_progress" {
		t.Errorf("resubmitting after a revision must keep the original origin, not 'pending_revision': %v", got)
	}

	// an unrelated update leaves metadata untouched (same map, no copy)
	same := map[string]any{"a": 1}
	if got := serverOwnedMetadata(cur(StatusInProgress, same), false, same, StatusInProgress); len(got) != 1 || got["a"] != 1 {
		t.Errorf("unrelated update changed metadata: %v", got)
	}
}
