package handler

import (
	"encoding/json"
	"io"
	"net/http"

	"github.com/tojiuni/goquest/internal/resolve"
	"github.com/tojiuni/goquest/internal/vcs"
)

type VCSHandler struct {
	svc          *vcs.Service
	gh           vcs.Provider
	resolver     ProjectResolver
	githubSecret string
}

func NewVCSHandler(svc *vcs.Service, gh vcs.Provider, resolver ProjectResolver, githubSecret string) *VCSHandler {
	return &VCSHandler{svc: svc, gh: gh, resolver: resolver, githubSecret: githubSecret}
}

// GitHubWebhook verifies the signature, parses, and links. Public (no API key).
func (h *VCSHandler) GitHubWebhook(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(io.LimitReader(r.Body, 5<<20)) // 5MB cap
	if err != nil {
		http.Error(w, "read error", http.StatusBadRequest)
		return
	}
	if err := h.gh.Verify(r, body, h.githubSecret); err != nil {
		http.Error(w, "invalid signature", http.StatusUnauthorized)
		return
	}
	events, err := h.gh.Parse(r.Header.Get("X-GitHub-Event"), body)
	if err != nil {
		http.Error(w, "parse error", http.StatusBadRequest)
		return
	}
	if h.svc != nil {
		for _, ev := range events {
			if perr := h.svc.Process(r.Context(), h.gh.Name(), ev); perr != nil {
				// log and continue; a failed link should not 500 the webhook
				continue
			}
		}
	}
	w.WriteHeader(http.StatusOK)
}

type registerRepoReq struct {
	Provider       string `json:"provider"`
	RepoFullName   string `json:"repo_full_name"`
	Workspace      string `json:"workspace"`
	WorkspaceSlug  string `json:"workspace_slug"`
	Project        string `json:"project"`
	AutoTransition *bool  `json:"auto_transition"`
	MergeTarget    string `json:"merge_target"`
}

// RegisterRepo maps (provider, repo) → project. Protected (inside API-key group).
func (h *VCSHandler) RegisterRepo(w http.ResponseWriter, r *http.Request) {
	var req registerRepoReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad body", http.StatusBadRequest)
		return
	}
	pid, err := h.resolver.ResolveProjectID(r.Context(), resolve.Ref{
		WorkspaceSlug: req.WorkspaceSlug, WorkspaceName: req.Workspace, ProjectName: req.Project})
	if err != nil || pid == nil {
		http.Error(w, "project not resolved", http.StatusBadRequest)
		return
	}
	repo := &vcs.VcsRepo{
		Provider: orDefault(req.Provider, "github"), RepoFullName: req.RepoFullName,
		ProjectID: *pid, AutoTransition: req.AutoTransition == nil || *req.AutoTransition,
		MergeTarget: orDefault(req.MergeTarget, "completed"), Active: true,
	}
	if err := h.svc.RegisterRepo(r.Context(), repo); err != nil {
		http.Error(w, "register failed", http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusCreated, repo)
}

func (h *VCSHandler) ListRepos(w http.ResponseWriter, r *http.Request) {
	repos, err := h.svc.ListRepos(r.Context())
	if err != nil {
		http.Error(w, "list failed", http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, repos)
}

func orDefault(v, def string) string {
	if v == "" {
		return def
	}
	return v
}
