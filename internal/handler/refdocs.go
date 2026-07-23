package handler

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/tojiuni/goquest/internal/auth"
	"github.com/tojiuni/goquest/internal/brainclient"
	"github.com/tojiuni/goquest/internal/githubapp"
)

// BrainLookuper resolves a ticket's reference documents via brain. Satisfied
// by *brainclient.Client; a narrow interface so handler tests can fake it.
type BrainLookuper interface {
	TicketDocs(ctx context.Context, ticketID, callerJWT string) ([]brainclient.RelatedDoc, error)
}

// ContentFetcher fetches a file's raw content from a GitHub repo. Satisfied
// by *githubapp.Client.
type ContentFetcher interface {
	Content(ctx context.Context, owner, repo, path string) (string, error)
}

// RefDocsHandler serves the goquest-mediated ticket reference document
// endpoints: GET /tickets/{id}/docs (brain-backed list) and
// GET /docs/content (GitHub-backed content), so the browser never talks to
// brain or GitHub directly (goq-refdoc-goquest-brain-client).
type RefDocsHandler struct {
	brain       BrainLookuper
	github      ContentFetcher
	staff       auth.StaffChecker
	genesoOwner string
	genesoRepo  string
}

func NewRefDocsHandler(brain BrainLookuper, github ContentFetcher, staff auth.StaffChecker, genesoOwner, genesoRepo string) *RefDocsHandler {
	return &RefDocsHandler{brain: brain, github: github, staff: staff, genesoOwner: genesoOwner, genesoRepo: genesoRepo}
}

// bearerJWT extracts the raw JWT from an "Authorization: Bearer <jwt>"
// header, or "" if absent/not bearer-shaped (e.g. an X-API-Key caller with
// no JWT at all).
func bearerJWT(r *http.Request) string {
	h := r.Header.Get("Authorization")
	const prefix = "Bearer "
	if !strings.HasPrefix(h, prefix) {
		return ""
	}
	return strings.TrimPrefix(h, prefix)
}

type docJSON struct {
	Title      string `json:"title"`
	SourcePath string `json:"source_path"`
}

// TicketDocs handles GET /tickets/{id}/docs. RequireTicketView has already
// authorized the caller and stashed the ticket.
func (h *RefDocsHandler) TicketDocs(w http.ResponseWriter, r *http.Request) {
	tk := auth.TicketFrom(r)
	if tk == nil {
		writeError(w, http.StatusInternalServerError, "ticket context missing (RequireTicketView not applied)")
		return
	}

	jwt := bearerJWT(r)
	if jwt == "" {
		writeError(w, http.StatusUnauthorized, "brain-backed ticket docs require a user session token")
		return
	}

	related, err := h.brain.TicketDocs(r.Context(), tk.ID.String(), jwt)
	if err != nil {
		if errors.Is(err, brainclient.ErrJWTDecode) {
			// A malformed/forged JWT is a client auth problem, not a brain
			// availability problem — don't conflate it with 502 (goq-refdoc-
			// goquest-brain-client final review, Fix 2).
			writeError(w, http.StatusUnauthorized, "invalid session token")
			return
		}
		writeError(w, http.StatusBadGateway, "brain unavailable: "+err.Error())
		return
	}

	docs := make([]docJSON, 0, len(related))
	for _, d := range related {
		docs = append(docs, docJSON{Title: d.Title, SourcePath: d.SourcePath})
	}
	writeJSON(w, http.StatusOK, map[string]any{"docs": docs})
}

// Content handles GET /docs/content?source_path=<path>. Sits in goquest's
// existing authenticated route group (no ticket context needed — the path
// alone is the lookup key, matching the spec's design).
func (h *RefDocsHandler) Content(w http.ResponseWriter, r *http.Request) {
	// Unlike TicketDocs (ReBAC-filtered by brain via the forwarded caller
	// JWT), this endpoint's source_path is a bare lookup key with no scoping
	// of its own — so genuinely external contexts (magic-link customers,
	// external org members with no goquest workspace of their own) must be
	// excluded here explicitly. An org claim alone is NOT sufficient to
	// exclude: a caller can be staff AND hold an org claim (e.g. a dual-role
	// account), and that staff identity must not be overridden by the org
	// claim (goq-org-claim-staff-content-comment-bypass — same principle as
	// goq-org-claim-staff-visibility-bypass / goq-org-ticket-view-visibility-bypass).
	if auth.IsExternalOnly(r.Context(), r, h.staff) {
		writeError(w, http.StatusForbidden, "internal document content is not available to external callers")
		return
	}

	sourcePath := r.URL.Query().Get("source_path")
	if sourcePath == "" {
		writeError(w, http.StatusBadRequest, "source_path is required")
		return
	}
	// ref_ids in production carry a mix of "geneso:"-prefixed and bare paths
	// (see brain/spec/2026-07-22-brain-ticket-docs-rpc-design.md §background) —
	// accept both.
	sourcePath = strings.TrimPrefix(sourcePath, "geneso:")

	content, err := h.github.Content(r.Context(), h.genesoOwner, h.genesoRepo, sourcePath)
	if err != nil {
		if errors.Is(err, githubapp.ErrNotFound) {
			writeError(w, http.StatusNotFound, "document not found")
			return
		}
		writeError(w, http.StatusBadGateway, "github fetch failed: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"content": content, "source_path": sourcePath})
}
