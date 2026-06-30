package handler

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"net/http"

	"gopkg.in/yaml.v3"
)

//go:embed openapi_spec.yaml
var openapiYAML []byte

// OpenAPIHandler serves the hand-written ticket OpenAPI spec in both YAML (raw)
// and JSON (converted at startup) forms.
type OpenAPIHandler struct {
	json []byte
}

// NewOpenAPIHandler converts the embedded YAML spec to JSON once at startup.
// yaml.v3 decodes mappings into map[string]interface{}, so the result marshals
// to JSON cleanly without a dedicated YAML→JSON dependency.
func NewOpenAPIHandler() (*OpenAPIHandler, error) {
	var doc any
	if err := yaml.Unmarshal(openapiYAML, &doc); err != nil {
		return nil, fmt.Errorf("parse openapi yaml: %w", err)
	}
	j, err := json.Marshal(doc)
	if err != nil {
		return nil, fmt.Errorf("marshal openapi json: %w", err)
	}
	return &OpenAPIHandler{json: j}, nil
}

func (h *OpenAPIHandler) JSON(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(h.json)
}

func (h *OpenAPIHandler) YAML(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/yaml")
	_, _ = w.Write(openapiYAML)
}
