package tojihub

import (
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/tojiuni/goquest/internal/calendarsync"
)

func TestToICal_BasicFields(t *testing.T) {
	ev := calendarsync.Event{
		Principal: "user@example.com",
		ObjectID:  "goquest-abc123.ics",
		Summary:   "ship the thing",
		DTStart:   time.Date(2026, 8, 20, 9, 30, 0, 0, time.UTC),
	}

	out := toICal(ev)

	for _, want := range []string{
		"BEGIN:VCALENDAR",
		"BEGIN:VEVENT",
		"UID:goquest-abc123",
		"SUMMARY:ship the thing",
		"DTSTART:20260820T093000Z",
		"END:VEVENT",
		"END:VCALENDAR",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("expected iCal output to contain %q, got:\n%s", want, out)
		}
	}
}

func TestToICal_EscapesSpecialCharacters(t *testing.T) {
	ev := calendarsync.Event{
		ObjectID: "goquest-x.ics",
		Summary:  `fix a; b, c\d`,
		DTStart:  time.Now().UTC(),
	}

	out := toICal(ev)

	if !strings.Contains(out, `SUMMARY:fix a\; b\, c\\d`) {
		t.Errorf("expected escaped summary, got:\n%s", out)
	}
}

// TestToICal_HasRequiredProperties asserts PRODID (VCALENDAR, RFC 5545 §3.6)
// and DTSTAMP (VEVENT, RFC 5545 §3.6.1) are present — both REQUIRED by the
// spec, and strict CalDAV servers reject objects without them.
func TestToICal_HasRequiredProperties(t *testing.T) {
	ev := calendarsync.Event{
		ObjectID: "goquest-req.ics",
		Summary:  "needs required props",
		DTStart:  time.Now().UTC(),
	}

	out := toICal(ev)

	if !strings.Contains(out, "PRODID:") {
		t.Errorf("expected PRODID in output, got:\n%s", out)
	}
	if !strings.Contains(out, "DTSTAMP:") {
		t.Errorf("expected DTSTAMP in output, got:\n%s", out)
	}
}

// TestToICal_IncludesGoquestTicketMetadata asserts every X-GOQUEST-* property
// is emitted with the expected value/format when the corresponding Event
// field is populated.
func TestToICal_IncludesGoquestTicketMetadata(t *testing.T) {
	ticketID := uuid.New()
	projectID := uuid.New()
	start := time.Date(2026, 8, 15, 8, 0, 0, 0, time.UTC)
	due := time.Date(2026, 8, 20, 9, 30, 0, 0, time.UTC)
	ev := calendarsync.Event{
		ObjectID:     "goquest-meta.ics",
		Summary:      "with metadata",
		DTStart:      due,
		TicketID:     ticketID,
		Status:       "in_progress",
		ProjectID:    &projectID,
		AssigneeID:   "assignee@example.com",
		AssigneeType: "user",
		RequesterID:  "requester@example.com",
		StartAt:      &start,
		DueAt:        &due,
	}

	out := toICal(ev)

	for _, want := range []string{
		"X-GOQUEST-TICKET-ID:" + ticketID.String(),
		"X-GOQUEST-STATUS:in_progress",
		"X-GOQUEST-PROJECT-ID:" + projectID.String(),
		"X-GOQUEST-ASSIGNEE-ID:assignee@example.com",
		"X-GOQUEST-ASSIGNEE-TYPE:user",
		"X-GOQUEST-REQUESTER-ID:requester@example.com",
		"X-GOQUEST-START-AT:20260815T080000Z",
		"X-GOQUEST-DUE-AT:20260820T093000Z",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("expected iCal output to contain %q, got:\n%s", want, out)
		}
	}
}

// TestToICal_OmitsNullableGoquestMetadataWhenUnset asserts the
// nullable/optional X-GOQUEST-* properties (PROJECT-ID, ASSIGNEE-ID,
// START-AT, DUE-AT) are omitted entirely when their source Event field is
// nil/empty, while the always-emitted properties still appear.
func TestToICal_OmitsNullableGoquestMetadataWhenUnset(t *testing.T) {
	ev := calendarsync.Event{
		ObjectID:     "goquest-nometa.ics",
		Summary:      "no optional metadata",
		DTStart:      time.Now().UTC(),
		TicketID:     uuid.New(),
		Status:       "pending",
		ProjectID:    nil,
		AssigneeID:   "",
		AssigneeType: "unassigned",
		RequesterID:  "requester@example.com",
		StartAt:      nil,
		DueAt:        nil,
	}

	out := toICal(ev)

	for _, notWant := range []string{
		"X-GOQUEST-PROJECT-ID:",
		"X-GOQUEST-ASSIGNEE-ID:",
		"X-GOQUEST-START-AT:",
		"X-GOQUEST-DUE-AT:",
	} {
		if strings.Contains(out, notWant) {
			t.Errorf("expected iCal output NOT to contain %q, got:\n%s", notWant, out)
		}
	}
	for _, want := range []string{
		"X-GOQUEST-TICKET-ID:",
		"X-GOQUEST-STATUS:pending",
		"X-GOQUEST-REQUESTER-ID:requester@example.com",
		"X-GOQUEST-ASSIGNEE-TYPE:unassigned",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("expected iCal output to contain %q, got:\n%s", want, out)
		}
	}
}

// TestToICal_NormalizesCRLF asserts a summary containing "\r\n" produces
// only the escaped "\n" form in the content line, with no raw CR byte
// surviving (RFC 5545 line-ending rules).
func TestToICal_NormalizesCRLF(t *testing.T) {
	ev := calendarsync.Event{
		ObjectID: "goquest-crlf.ics",
		Summary:  "line1\r\nline2",
		DTStart:  time.Now().UTC(),
	}

	out := toICal(ev)

	if !strings.Contains(out, `SUMMARY:line1\nline2`) {
		t.Errorf("expected escaped SUMMARY line, got:\n%s", out)
	}
	// Strip the CRLF line terminators the builder itself emits before
	// checking for a stray bare CR left over from the input.
	body := strings.ReplaceAll(out, "\r\n", "\n")
	if strings.Contains(body, "\r") {
		t.Errorf("expected no raw CR byte from input in output, got:\n%q", out)
	}
}
