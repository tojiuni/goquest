package bot

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

// --- buildQueryString ---

func TestBuildQueryString_Empty(t *testing.T) {
	got := buildQueryString(nil)
	if got != "" {
		t.Errorf("expected empty string for nil payload, got %q", got)
	}
	got = buildQueryString(map[string]any{})
	if got != "" {
		t.Errorf("expected empty string for empty payload, got %q", got)
	}
}

func TestBuildQueryString_SpecialChars(t *testing.T) {
	payload := map[string]any{
		"status": "in_progress",
		"q":      "hello world&foo=bar",
	}
	raw := buildQueryString(payload)
	if !strings.HasPrefix(raw, "?") {
		t.Errorf("query string should start with ?, got %q", raw)
	}
	parsed, err := url.ParseQuery(raw[1:])
	if err != nil {
		t.Fatalf("could not parse query string %q: %v", raw, err)
	}
	if parsed.Get("status") != "in_progress" {
		t.Errorf("expected status=in_progress, got %q", parsed.Get("status"))
	}
	if parsed.Get("q") != "hello world&foo=bar" {
		t.Errorf("special chars not encoded correctly, got %q", parsed.Get("q"))
	}
}

// --- capability dispatch ---

func newFakeService(statusCode int, body any) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(statusCode)
		_ = json.NewEncoder(w).Encode(body)
	}))
}

func callExecute(t *testing.T, h *Handler, req ExecuteRequest) ExecuteResponse {
	t.Helper()
	b, _ := json.Marshal(req)
	r := httptest.NewRequest(http.MethodPost, "/execute", bytes.NewReader(b))
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.Execute(w, r)

	var resp ExecuteResponse
	_ = json.NewDecoder(w.Body).Decode(&resp)
	return resp
}

func TestExecute_UnknownCapability(t *testing.T) {
	h := NewHandler("http://unused", "", "", nil)
	resp := callExecute(t, h, ExecuteRequest{Capability: "unknown.cap"})
	if resp.OK {
		t.Error("expected OK=false for unknown capability")
	}
	if !strings.Contains(resp.Error, "unknown capability") {
		t.Errorf("expected 'unknown capability' in error, got %q", resp.Error)
	}
}

func TestExecute_Health(t *testing.T) {
	h := NewHandler("http://unused", "", "", nil)
	resp := callExecute(t, h, ExecuteRequest{Capability: "ticket.health", TaskID: "test-123"})
	if !resp.OK {
		t.Errorf("expected OK=true for ticket.health, got error: %s", resp.Error)
	}
	if resp.TaskID != "test-123" {
		t.Errorf("expected TaskID=test-123, got %q", resp.TaskID)
	}
}

func TestExecute_GetTicket_MissingID(t *testing.T) {
	h := NewHandler("http://unused", "", "", nil)
	resp := callExecute(t, h, ExecuteRequest{
		Capability: "ticket.get",
		Payload:    map[string]any{},
	})
	if resp.OK {
		t.Error("expected OK=false when id is missing")
	}
}

func TestExecute_ListTickets_CallsService(t *testing.T) {
	ticketList := map[string]any{"tickets": []any{}, "total": 0}
	srv := newFakeService(http.StatusOK, ticketList)
	defer srv.Close()

	h := NewHandler(srv.URL, "", "", &http.Client{})
	resp := callExecute(t, h, ExecuteRequest{
		Capability: "ticket.list",
		Payload:    map[string]any{"limit": 10},
	})
	if !resp.OK {
		t.Errorf("expected OK=true, got error: %s", resp.Error)
	}
}

func TestExecute_CreateTicket_ServiceError(t *testing.T) {
	srv := newFakeService(http.StatusBadRequest, map[string]string{"error": "title required"})
	defer srv.Close()

	h := NewHandler(srv.URL, "", "", &http.Client{})
	resp := callExecute(t, h, ExecuteRequest{
		Capability: "ticket.create",
		Payload:    map[string]any{"title": ""},
	})
	if resp.OK {
		t.Error("expected OK=false for service 400 error")
	}
}

func TestExecute_AssignTicket_DomainToAssigneeID(t *testing.T) {
	var capturedBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedBody, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"id":"some-uuid"}`))
	}))
	defer srv.Close()

	h := NewHandler(srv.URL, "", "", &http.Client{})
	resp := callExecute(t, h, ExecuteRequest{
		Capability: "ticket.assign",
		Payload: map[string]any{
			"id":     "aaaaaaaa-0000-0000-0000-000000000000",
			"domain": "infra",
		},
	})
	if !resp.OK {
		t.Fatalf("expected OK=true, got: %s", resp.Error)
	}

	var body map[string]any
	_ = json.Unmarshal(capturedBody, &body)
	if body["assignee_id"] != "hermes-infra" {
		t.Errorf("expected assignee_id=hermes-infra, got %v", body["assignee_id"])
	}
	if body["status"] != "assigned" {
		t.Errorf("expected status=assigned (per SOUL.md state machine), got %v", body["status"])
	}
	if body["domain"] != "infra" {
		t.Errorf("expected domain=infra in PATCH body, got %v", body["domain"])
	}
}

// Repro: when the caller omits "domain" but supplies an explicit assignee_id,
// the bot previously sent domain="" which goquest-service rejected as an
// invalid ticket_domain enum (SQL 22P02 → 500).
func TestExecute_AssignTicket_OmitsDomainWhenEmpty(t *testing.T) {
	var capturedBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedBody, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"id":"some-uuid"}`))
	}))
	defer srv.Close()

	h := NewHandler(srv.URL, "", "", &http.Client{})
	resp := callExecute(t, h, ExecuteRequest{
		Capability: "ticket.assign",
		Payload: map[string]any{
			"id":          "aaaaaaaa-0000-0000-0000-000000000000",
			"assignee_id": "hermes-code",
		},
	})
	if !resp.OK {
		t.Fatalf("expected OK=true, got: %s", resp.Error)
	}
	var body map[string]any
	_ = json.Unmarshal(capturedBody, &body)
	if _, present := body["domain"]; present {
		t.Errorf("domain must be omitted when caller did not provide it, got body[domain]=%v", body["domain"])
	}
	if body["status"] != "assigned" {
		t.Errorf("expected status=assigned, got %v", body["status"])
	}
}

// Repro: ticket.complete previously replaced agent_context with the result
// payload, losing pre-existing keys. Per SOUL.md §10 it must MERGE.
func TestExecute_CompleteTicket_MergesAgentContext(t *testing.T) {
	var patched map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.Method {
		case http.MethodGet:
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id": "aaaaaaaa-0000-0000-0000-000000000000",
				"agent_context": map[string]any{
					"platform": "iOS",
					"version":  "18.2",
				},
			})
		case http.MethodPatch:
			b, _ := io.ReadAll(r.Body)
			_ = json.Unmarshal(b, &patched)
			_, _ = w.Write([]byte(`{"id":"some-uuid"}`))
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	}))
	defer srv.Close()

	h := NewHandler(srv.URL, "", "", &http.Client{})
	resp := callExecute(t, h, ExecuteRequest{
		Capability: "ticket.complete",
		Payload: map[string]any{
			"id": "aaaaaaaa-0000-0000-0000-000000000000",
			"result": map[string]any{
				"fix_commit": "abc1234",
				"rollout":    "staged",
			},
		},
	})
	if !resp.OK {
		t.Fatalf("expected OK=true, got: %s", resp.Error)
	}
	ctx, ok := patched["agent_context"].(map[string]any)
	if !ok {
		t.Fatalf("patched agent_context missing or wrong shape: %v", patched["agent_context"])
	}
	for _, k := range []string{"platform", "version", "fix_commit", "rollout"} {
		if _, ok := ctx[k]; !ok {
			t.Errorf("merged agent_context missing key %q (got %v)", k, ctx)
		}
	}
	if ctx["platform"] != "iOS" || ctx["fix_commit"] != "abc1234" {
		t.Errorf("merge values wrong: %v", ctx)
	}
}

func TestExecute_InvalidJSON(t *testing.T) {
	h := NewHandler("http://unused", "", "", nil)
	r := httptest.NewRequest(http.MethodPost, "/execute", strings.NewReader("{invalid json"))
	w := httptest.NewRecorder()
	h.Execute(w, r)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for invalid JSON, got %d", w.Code)
	}
}
