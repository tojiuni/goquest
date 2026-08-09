package tojihub

import (
	"strings"
	"testing"
	"time"

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
