// Command vcs-reconcile applies a declarative VCS-repo connection manifest:
// for each repo it ensures the target goquest project exists, registers the
// repo->project mapping via the goquest API, and ensures a GitHub webhook to
// the goquest VCS endpoint. It is idempotent — re-running only fills gaps.
//
// Usage:
//
//	go run ./cmd/vcs-reconcile --manifest deploy/vcs-repos.yaml [--dry-run] [--skip-webhooks]
//
// Env:
//
//	GOQUEST_URL, GOQUEST_TOKEN, WEBHOOK_SECRET,
//	GITHUB_TOKEN_<OWNER>  (owner upper-cased, non-alnum -> '_'; e.g. GITHUB_TOKEN_TOJIUNI)
package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"regexp"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

type manifest struct {
	WebhookURL string   `yaml:"webhook_url"`
	Events     []string `yaml:"events"`
	Defaults   struct {
		Workspace      string `yaml:"workspace"`
		AutoTransition *bool  `yaml:"auto_transition"`
		MergeTarget    string `yaml:"merge_target"`
	} `yaml:"defaults"`
	Repos []repoEntry `yaml:"repos"`
}

type repoEntry struct {
	Repo           string `yaml:"repo"`
	Project        string `yaml:"project"`
	Workspace      string `yaml:"workspace"`
	AutoTransition *bool  `yaml:"auto_transition"`
	MergeTarget    string `yaml:"merge_target"`
}

var nonAlnum = regexp.MustCompile(`[^A-Z0-9]`)

func githubTokenEnv(owner string) string {
	return "GITHUB_TOKEN_" + nonAlnum.ReplaceAllString(strings.ToUpper(owner), "_")
}

func main() {
	manifestPath := flag.String("manifest", "deploy/vcs-repos.yaml", "path to the VCS-repos manifest")
	dryRun := flag.Bool("dry-run", false, "log intended actions without calling any API")
	skipWebhooks := flag.Bool("skip-webhooks", false, "register repos in goquest but do not touch GitHub webhooks")
	flag.Parse()

	raw, err := os.ReadFile(*manifestPath)
	if err != nil {
		fatal("read manifest: %v", err)
	}
	var m manifest
	if err := yaml.Unmarshal(raw, &m); err != nil {
		fatal("parse manifest: %v", err)
	}
	if m.WebhookURL == "" || len(m.Events) == 0 {
		fatal("manifest must set webhook_url and events")
	}

	gq := &goquestClient{
		base:  strings.TrimRight(os.Getenv("GOQUEST_URL"), "/"),
		token: os.Getenv("GOQUEST_TOKEN"),
		http:  &http.Client{Timeout: 30 * time.Second},
	}
	if gq.base == "" || gq.token == "" {
		fatal("GOQUEST_URL and GOQUEST_TOKEN are required")
	}
	secret := os.Getenv("WEBHOOK_SECRET")
	if secret == "" && !*skipWebhooks && !*dryRun {
		fatal("WEBHOOK_SECRET is required unless --skip-webhooks/--dry-run")
	}

	var failures int
	for _, r := range m.Repos {
		if err := reconcileRepo(gq, &m, r, secret, *dryRun, *skipWebhooks); err != nil {
			fmt.Fprintf(os.Stderr, "  ✗ %s: %v\n", r.Repo, err)
			failures++
		}
	}
	fmt.Printf("\nreconciled %d repo(s), %d failure(s)\n", len(m.Repos), failures)
	if failures > 0 {
		os.Exit(1)
	}
}

func reconcileRepo(gq *goquestClient, m *manifest, r repoEntry, secret string, dryRun, skipWebhooks bool) error {
	owner, name, ok := splitRepo(r.Repo)
	if !ok {
		return fmt.Errorf("repo must be owner/name")
	}
	ws := firstNonEmpty(r.Workspace, m.Defaults.Workspace)
	if ws == "" {
		return fmt.Errorf("no workspace (set defaults.workspace or per-repo workspace)")
	}
	if r.Project == "" {
		return fmt.Errorf("no project")
	}
	autoTransition := true
	if m.Defaults.AutoTransition != nil {
		autoTransition = *m.Defaults.AutoTransition
	}
	if r.AutoTransition != nil {
		autoTransition = *r.AutoTransition
	}
	mergeTarget := firstNonEmpty(r.MergeTarget, m.Defaults.MergeTarget, "completed")

	fmt.Printf("• %s -> %s/%s\n", r.Repo, ws, r.Project)
	if dryRun {
		fmt.Printf("    [dry-run] ensure project, register repo, ensure webhook %s\n", m.WebhookURL)
		return nil
	}

	// 1) ensure the project exists
	if _, err := gq.ensureProject(ws, r.Project); err != nil {
		return fmt.Errorf("ensure project: %w", err)
	}

	// 2) register repo -> project (idempotent upsert server-side)
	if err := gq.registerRepo(owner+"/"+name, ws, r.Project, autoTransition, mergeTarget); err != nil {
		return fmt.Errorf("register repo: %w", err)
	}
	fmt.Printf("    registered (auto_transition=%v merge_target=%s)\n", autoTransition, mergeTarget)

	// 3) ensure GitHub webhook
	if skipWebhooks {
		return nil
	}
	tokEnv := githubTokenEnv(owner)
	ghTok := os.Getenv(tokEnv)
	if ghTok == "" {
		return fmt.Errorf("no GitHub token in $%s for owner %q", tokEnv, owner)
	}
	gh := &githubClient{token: ghTok, http: &http.Client{Timeout: 30 * time.Second}}
	action, err := gh.ensureWebhook(owner, name, m.WebhookURL, m.Events, secret)
	if err != nil {
		return fmt.Errorf("ensure webhook: %w", err)
	}
	fmt.Printf("    webhook %s\n", action)
	return nil
}

// ---- goquest API client ----

type goquestClient struct {
	base  string
	token string
	http  *http.Client
	wsID  map[string]string // slug -> id (cached)
}

func (c *goquestClient) do(method, path string, body any, out any) (int, error) {
	var rdr io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rdr = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, c.base+path, rdr)
	if err != nil {
		return 0, err
	}
	req.Header.Set("X-API-Key", c.token)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	if out != nil && len(data) > 0 && resp.StatusCode < 300 {
		_ = json.Unmarshal(data, out)
	}
	if resp.StatusCode >= 300 {
		return resp.StatusCode, fmt.Errorf("%s %s -> %d: %s", method, path, resp.StatusCode, strings.TrimSpace(string(data)))
	}
	return resp.StatusCode, nil
}

func (c *goquestClient) workspaceID(slug string) (string, error) {
	if c.wsID == nil {
		var out struct {
			Workspaces []struct{ ID, Slug string } `json:"workspaces"`
		}
		if _, err := c.do("GET", "/api/v1/workspaces", nil, &out); err != nil {
			return "", err
		}
		c.wsID = map[string]string{}
		for _, w := range out.Workspaces {
			c.wsID[w.Slug] = w.ID
		}
	}
	id, ok := c.wsID[slug]
	if !ok {
		return "", fmt.Errorf("workspace slug %q not found", slug)
	}
	return id, nil
}

// ensureProject returns the project id, creating the project if it does not exist.
func (c *goquestClient) ensureProject(wsSlug, name string) (string, error) {
	wid, err := c.workspaceID(wsSlug)
	if err != nil {
		return "", err
	}
	var list struct {
		Projects []struct{ ID, Name string } `json:"projects"`
	}
	if _, err := c.do("GET", "/api/v1/workspaces/"+wid+"/projects", nil, &list); err != nil {
		return "", err
	}
	for _, p := range list.Projects {
		if p.Name == name {
			return p.ID, nil
		}
	}
	var created struct{ ID string }
	if _, err := c.do("POST", "/api/v1/workspaces/"+wid+"/projects", map[string]string{"name": name}, &created); err != nil {
		return "", err
	}
	fmt.Printf("    created project %q\n", name)
	return created.ID, nil
}

func (c *goquestClient) registerRepo(fullName, wsSlug, project string, autoTransition bool, mergeTarget string) error {
	body := map[string]any{
		"provider":        "github",
		"repo_full_name":  fullName,
		"workspace_slug":  wsSlug,
		"project":         project,
		"auto_transition": autoTransition,
		"merge_target":    mergeTarget,
	}
	_, err := c.do("POST", "/api/v1/vcs/repos", body, nil)
	return err
}

// ---- GitHub API client ----

type githubClient struct {
	token string
	http  *http.Client
}

func (g *githubClient) req(method, path string, body any) (int, []byte, error) {
	var rdr io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rdr = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, "https://api.github.com"+path, rdr)
	if err != nil {
		return 0, nil, err
	}
	req.Header.Set("Authorization", "Bearer "+g.token)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	req.Header.Set("User-Agent", "goquest-vcs-reconcile")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := g.http.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, data, nil
}

// ensureWebhook creates the webhook if none points at hookURL; returns a short
// action word ("created" or "exists").
func (g *githubClient) ensureWebhook(owner, repo, hookURL string, events []string, secret string) (string, error) {
	code, data, err := g.req("GET", fmt.Sprintf("/repos/%s/%s/hooks", owner, repo), nil)
	if err != nil {
		return "", err
	}
	if code >= 300 {
		return "", fmt.Errorf("list hooks -> %d: %s", code, strings.TrimSpace(string(data)))
	}
	var hooks []struct {
		ID     int64             `json:"id"`
		Config map[string]string `json:"config"`
	}
	if err := json.Unmarshal(data, &hooks); err != nil {
		return "", fmt.Errorf("decode hooks: %w", err)
	}
	for _, h := range hooks {
		if h.Config["url"] == hookURL {
			return fmt.Sprintf("exists (id %d)", h.ID), nil
		}
	}
	payload := map[string]any{
		"name":   "web",
		"active": true,
		"events": events,
		"config": map[string]string{
			"url":          hookURL,
			"content_type": "json",
			"secret":       secret,
			"insecure_ssl": "0",
		},
	}
	code, data, err = g.req("POST", fmt.Sprintf("/repos/%s/%s/hooks", owner, repo), payload)
	if err != nil {
		return "", err
	}
	if code >= 300 {
		return "", fmt.Errorf("create hook -> %d: %s", code, strings.TrimSpace(string(data)))
	}
	var created struct{ ID int64 }
	_ = json.Unmarshal(data, &created)
	return fmt.Sprintf("created (id %d)", created.ID), nil
}

// ---- helpers ----

func splitRepo(s string) (owner, name string, ok bool) {
	i := strings.IndexByte(s, '/')
	if i <= 0 || i == len(s)-1 {
		return "", "", false
	}
	return s[:i], s[i+1:], true
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

func fatal(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "vcs-reconcile: "+format+"\n", args...)
	os.Exit(2)
}
