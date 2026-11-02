package handler_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/tojiuni/goquest/internal/handler"
)

func TestOpenAPI_JSONServes(t *testing.T) {
	h, err := handler.NewOpenAPIHandler()
	if err != nil {
		t.Fatalf("NewOpenAPIHandler: %v", err)
	}
	rec := httptest.NewRecorder()
	h.JSON(rec, httptest.NewRequest(http.MethodGet, "/api/v1/openapi.json", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	var doc map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &doc); err != nil {
		t.Fatalf("body is not valid JSON: %v", err)
	}
	if doc["openapi"] == nil {
		t.Fatalf("missing openapi field")
	}
	paths, ok := doc["paths"].(map[string]any)
	if !ok || paths["/tickets/by-name/{title_name}"] == nil {
		t.Fatalf("spec missing the by-name path; paths=%v", doc["paths"])
	}
	if paths["/tickets/{id}/comments"] == nil {
		t.Fatalf("spec missing the comments path; paths=%v", doc["paths"])
	}
}

func TestOpenAPI_YAMLServes(t *testing.T) {
	h, err := handler.NewOpenAPIHandler()
	if err != nil {
		t.Fatalf("NewOpenAPIHandler: %v", err)
	}
	rec := httptest.NewRecorder()
	h.YAML(rec, httptest.NewRequest(http.MethodGet, "/api/v1/openapi.yaml", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/yaml" {
		t.Errorf("Content-Type = %q, want application/yaml", ct)
	}
	if rec.Body.Len() == 0 {
		t.Fatalf("empty yaml body")
	}
}

func TestOpenAPI_ApprovalPathsAreAgentFacing(t *testing.T) {
	h, err := handler.NewOpenAPIHandler()
	if err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	h.JSON(rec, httptest.NewRequest(http.MethodGet, "/api/v1/openapi.json", nil))
	var doc struct {
		Paths      map[string]map[string]any `json:"paths"`
		Components struct {
			Schemas map[string]any `json:"schemas"`
		} `json:"components"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &doc); err != nil {
		t.Fatal(err)
	}
	for path, methods := range map[string][]string{
		"/tickets/{id}/approvals":                {"get", "post"},
		"/tickets/{id}/approvals/{aid}":          {"get"},
		"/tickets/{id}/approvals/{aid}/withdraw": {"post"},
		"/tickets/{id}/approvals/{aid}/decision": {"post"},
	} {
		for _, m := range methods {
			if doc.Paths[path][m] == nil {
				t.Errorf("spec missing %s %s", m, path)
			}
		}
	}
	// delegation rules move approval authority: person-only, never agent-facing
	for p := range doc.Paths {
		if strings.Contains(p, "delegation-rules") {
			t.Errorf("delegation rules must not be in the agent-facing spec: %s", p)
		}
	}
	for _, s := range []string{"Approval", "ApprovalSubject", "ApprovalEvidence"} {
		if doc.Components.Schemas[s] == nil {
			t.Errorf("missing schema %s", s)
		}
	}
}
