package spicedb

import (
	"regexp"
	"testing"
)

var spicedbObjectID = regexp.MustCompile(`^([a-zA-Z0-9/_|\-=+]+|\*)$`)

func TestSubjectID_SpiceDBSafe(t *testing.T) {
	for _, raw := range []string{"lyckabc@toji.homes", "370549762107174099", "a.b+c@x.y", ""} {
		got := subjectID(raw)
		if raw != "" && !spicedbObjectID.MatchString(got) {
			t.Errorf("subjectID(%q)=%q not SpiceDB-safe", raw, got)
		}
		if got != subjectID(raw) {
			t.Errorf("subjectID(%q) not deterministic", raw)
		}
	}
	// distinct inputs → distinct outputs (collision-free)
	if subjectID("a@b.com") == subjectID("a_b_com") {
		t.Error("encoding collision")
	}
}

func TestTouch_EncodesOnlyUserSubject(t *testing.T) {
	// user subject is encoded
	u := touch(TypeTicket, "tid", "requester", TypeUser, "lyckabc@toji.homes", "")
	if u.Relationship.Subject.Object.ObjectId == "lyckabc@toji.homes" {
		t.Error("user subject id not encoded")
	}
	if !spicedbObjectID.MatchString(u.Relationship.Subject.Object.ObjectId) {
		t.Error("encoded user subject not safe")
	}
	// resource subject (project) is NOT encoded (UUID already safe)
	p := touch(TypeProject, "pid", "workspace", TypeWorkspace, "ws-uuid-123", "")
	if p.Relationship.Subject.Object.ObjectId != "ws-uuid-123" {
		t.Errorf("non-user subject must not be encoded, got %q", p.Relationship.Subject.Object.ObjectId)
	}
}
