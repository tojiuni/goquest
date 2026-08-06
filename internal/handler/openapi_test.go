package handler_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
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
