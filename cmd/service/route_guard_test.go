package main

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

// TestTicketIDGroupHasViewGuard ensures the /tickets/{id} route group applies
// RequireTicketView. Every sub-route under that group inherits the gate; removing
// the r.Use(requireTicketView) line reintroduces the authorization leak and fails
// this test.
func TestTicketIDGroupHasViewGuard(t *testing.T) {
	src, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatal(err)
	}
	re := regexp.MustCompile(`r\.Route\("/tickets/\{id\}",\s*func\(r chi\.Router\)\s*\{`)
	loc := re.FindIndex(src)
	if loc == nil {
		t.Fatal(`/tickets/{id} route block not found`)
	}
	// Within the block header region, the guard must appear before sub-routes.
	head := string(src[loc[1]:])
	if len(head) > 400 {
		head = head[:400]
	}
	if !strings.Contains(head, "r.Use(requireTicketView)") {
		t.Error("/tickets/{id} group is missing r.Use(requireTicketView) — view gate leak")
	}
}
