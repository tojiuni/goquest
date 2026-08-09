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
