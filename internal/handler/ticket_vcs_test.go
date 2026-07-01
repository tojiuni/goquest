package handler

import (
	"encoding/json"
	"testing"

	"github.com/google/uuid"
	"github.com/tojiuni/goquest/internal/ticket"
	"github.com/tojiuni/goquest/internal/vcs"
)

func TestTicketWithLinksJSON(t *testing.T) {
	tk := &ticket.Ticket{ID: uuid.New(), TitleName: "goq-foo", Title: "Do X"}
	wrapped := ticketWithLinks{
		Ticket: tk,
		VcsLinks: []vcs.VcsLink{
			{Provider: "github", RepoFullName: "tojiuni/goquest", Kind: vcs.KindPR, ExternalRef: "12", State: vcs.StateMerged},
		},
	}
	b, err := json.Marshal(wrapped)
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatal(err)
	}
	// promoted ticket field is present
	if got["title_name"] != "goq-foo" {
		t.Fatalf("promoted ticket field missing: %v", got["title_name"])
	}
	// vcs_links embedded with one entry
	links, ok := got["vcs_links"].([]any)
	if !ok || len(links) != 1 {
		t.Fatalf("vcs_links not embedded correctly: %v", got["vcs_links"])
	}
}

func TestTicketWithLinksOmitsEmpty(t *testing.T) {
	b, _ := json.Marshal(ticketWithLinks{Ticket: &ticket.Ticket{ID: uuid.New(), TitleName: "goq-foo"}})
	var got map[string]any
	_ = json.Unmarshal(b, &got)
	if _, present := got["vcs_links"]; present {
		t.Fatalf("empty vcs_links should be omitted, got %v", got["vcs_links"])
	}
}
