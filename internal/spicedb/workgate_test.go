package spicedb

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func TestSchemaDefinesPermission(t *testing.T) {
	schema := `definition user {}
definition ticket {
  relation project: project
  permission can_manage = project->can_manage
  permission can_working_sounds_similar = project->can_edit
}
definition project {
  permission can_work = admin
}
`
	cases := map[string]struct {
		def, perm string
		want      bool
	}{
		"present on ticket":         {"ticket", "can_manage", true},
		"prefix of another name":    {"ticket", "can_working", false},
		"defined on another object": {"ticket", "can_work", false},
		"other object has it":       {"project", "can_work", true},
		"absent everywhere":         {"ticket", "can_edit", false},
		"unknown definition":        {"workspace", "can_manage", false},
	}
	for name, tc := range cases {
		if got := schemaDefinesPermission(schema, tc.def, tc.perm); got != tc.want {
			t.Errorf("%s: schemaDefinesPermission(%s, %s) = %v, want %v", name, tc.def, tc.perm, got, tc.want)
		}
	}
	if schemaDefinesPermission("", "ticket", PermCanWork) {
		t.Error("an empty schema defines nothing")
	}
}

// The schema we ship must define can_work on ticket, or the boot check would
// fall back to can_manage in every deploy.
func TestShippedSchemaDefinesCanWork(t *testing.T) {
	b, err := os.ReadFile("../migrate/schema/goquest.zed")
	if err != nil {
		t.Fatal(err)
	}
	if !schemaDefinesPermission(string(b), "ticket", PermCanWork) {
		t.Fatal("internal/migrate/schema/goquest.zed does not define ticket.can_work")
	}
	if !strings.Contains(string(b), "permission can_work = project->can_edit + can_manage") {
		t.Error("can_work must be project editors plus managers")
	}
}

func TestWorkPermissionDefaultsAndFallback(t *testing.T) {
	ch := &Checker{}
	if got := ch.WorkPermission(); got != PermCanWork {
		t.Fatalf("default work permission = %q, want %q", got, PermCanWork)
	}
	ch.UseManageForWork()
	if got := ch.WorkPermission(); got != "can_manage" {
		t.Fatalf("fallback work permission = %q, want can_manage", got)
	}
}

// workFake answers Check from a fixed grant set and records what was asked, so a
// test can see which permission the work gate actually consulted.
type workFake struct {
	PermissionChecker

	grants map[string]bool
	asked  []string
}

func (f *workFake) Check(_ context.Context, _, _, permission, userID string) (bool, error) {
	f.asked = append(f.asked, permission)
	return f.grants[permission+"/"+userID], nil
}

type fallbackWorkFake struct{ workFake }

func (f *fallbackWorkFake) WorkPermission() string { return "can_manage" }

// With the fallback, an editor holding can_work (but not can_manage) is refused,
// and the work gate never consults can_work at all. Managers still pass.
func TestCanWorkTicket_FallbackNeverGrantsEditorsManagerAccess(t *testing.T) {
	id := uuid.New()
	grants := map[string]bool{
		"can_work/editor":    true, // the editor grant that the fallback must ignore
		"can_manage/manager": true,
	}

	fb := &fallbackWorkFake{workFake{grants: grants}}
	ok, err := CanWorkTicket(context.Background(), fb, id, "editor")
	if err != nil || ok {
		t.Fatalf("editor under fallback: ok=%v err=%v, want denied", ok, err)
	}
	for _, p := range fb.asked {
		if p == PermCanWork {
			t.Fatalf("fallback consulted %s", PermCanWork)
		}
	}
	if ok, err := CanWorkTicket(context.Background(), fb, id, "manager"); err != nil || !ok {
		t.Fatalf("manager under fallback: ok=%v err=%v, want allowed", ok, err)
	}

	plain := &workFake{grants: grants}
	if ok, err := CanWorkTicket(context.Background(), plain, id, "editor"); err != nil || !ok {
		t.Fatalf("editor with can_work schema: ok=%v err=%v, want allowed", ok, err)
	}
}

// A removed project member must lose every project role, editor included.
func TestProjectMemberRolesCoverEveryProjectRole(t *testing.T) {
	want := map[string]bool{"admin": false, "editor": false, "member": false, "viewer": false}
	for _, r := range projectMemberRoles {
		if _, ok := want[r]; !ok {
			t.Errorf("unexpected role %q in projectMemberRoles", r)
		}
		want[r] = true
	}
	for r, seen := range want {
		if !seen {
			t.Errorf("DeleteProjectMember would leave the %q relation behind", r)
		}
	}
}
