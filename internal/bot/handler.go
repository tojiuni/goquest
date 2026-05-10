package bot

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"

	"github.com/google/uuid"
	"github.com/tojiuni/goquest/internal/ticket"
)

type ExecuteRequest struct {
	Capability string         `json:"capability"`
	Payload    map[string]any `json:"payload"`
	AuthLevel  string         `json:"auth_level"`
	TaskID     string         `json:"task_id,omitempty"`
}

type ExecuteResponse struct {
	OK     bool   `json:"ok"`
	Result any    `json:"result,omitempty"`
	Error  string `json:"error,omitempty"`
	TaskID string `json:"task_id,omitempty"`
}

type Handler struct {
	serviceURL       string
	apiKey           string
	defaultProjectID string
	client           *http.Client
}

func NewHandler(serviceURL, apiKey, defaultProjectID string, client *http.Client) *Handler {
	if client == nil {
		client = http.DefaultClient
	}
	return &Handler{
		serviceURL:       serviceURL,
		apiKey:           apiKey,
		defaultProjectID: defaultProjectID,
		client:           client,
	}
}

func (h *Handler) Execute(w http.ResponseWriter, r *http.Request) {
	var req ExecuteRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, ExecuteResponse{OK: false, Error: "invalid request: " + err.Error()})
		return
	}

	var (
		result any
		err    error
	)

	switch req.Capability {
	case "ticket.create":
		result, err = h.createTicket(r, req)
	case "ticket.get":
		result, err = h.getTicket(r, req)
	case "ticket.list":
		result, err = h.listTickets(r, req)
	case "ticket.update":
		result, err = h.updateTicket(r, req)
	case "ticket.cancel":
		result, err = h.cancelTicket(r, req)
	case "ticket.complete":
		result, err = h.completeTicket(r, req)
	case "ticket.assign":
		result, err = h.assignTicket(r, req)
	case "ticket.subtasks":
		result, err = h.listSubtasks(r, req)
	case "project.list":
		result, err = h.listProjects(r, req)
	case "ticket.health":
		writeJSON(w, http.StatusOK, ExecuteResponse{OK: true, Result: map[string]string{"status": "ok"}, TaskID: req.TaskID})
		return
	default:
		writeJSON(w, http.StatusBadRequest, ExecuteResponse{
			OK:     false,
			Error:  fmt.Sprintf("unknown capability: %s", req.Capability),
			TaskID: req.TaskID,
		})
		return
	}

	if err != nil {
		writeJSON(w, http.StatusInternalServerError, ExecuteResponse{OK: false, Error: err.Error(), TaskID: req.TaskID})
		return
	}

	writeJSON(w, http.StatusOK, ExecuteResponse{OK: true, Result: result, TaskID: req.TaskID})
}

func (h *Handler) createTicket(r *http.Request, req ExecuteRequest) (any, error) {
	var createReq ticket.CreateTicketReq
	if err := remapPayload(req.Payload, &createReq); err != nil {
		return nil, fmt.Errorf("invalid payload: %w", err)
	}
	// inject default project_id when not provided by the caller
	if createReq.ProjectID == nil && h.defaultProjectID != "" {
		pid, err := uuid.Parse(h.defaultProjectID)
		if err == nil {
			createReq.ProjectID = &pid
		}
	}
	return h.callService(r, "POST", "/api/v1/tickets", createReq)
}

func (h *Handler) getTicket(r *http.Request, req ExecuteRequest) (any, error) {
	id, ok := req.Payload["id"].(string)
	if !ok || id == "" {
		return nil, fmt.Errorf("payload.id is required")
	}
	return h.callService(r, "GET", "/api/v1/tickets/"+id, nil)
}

func (h *Handler) listTickets(r *http.Request, req ExecuteRequest) (any, error) {
	params := buildQueryString(req.Payload)
	return h.callService(r, "GET", "/api/v1/tickets"+params, nil)
}

func (h *Handler) updateTicket(r *http.Request, req ExecuteRequest) (any, error) {
	id, ok := req.Payload["id"].(string)
	if !ok || id == "" {
		return nil, fmt.Errorf("payload.id is required")
	}
	updatePayload := make(map[string]any, len(req.Payload))
	for k, v := range req.Payload {
		if k != "id" {
			updatePayload[k] = v
		}
	}
	var updateReq ticket.UpdateTicketReq
	if err := remapPayload(updatePayload, &updateReq); err != nil {
		return nil, fmt.Errorf("invalid payload: %w", err)
	}
	return h.callService(r, "PATCH", "/api/v1/tickets/"+id, updateReq)
}

func (h *Handler) cancelTicket(r *http.Request, req ExecuteRequest) (any, error) {
	id, ok := req.Payload["id"].(string)
	if !ok || id == "" {
		return nil, fmt.Errorf("payload.id is required")
	}
	return h.callService(r, "DELETE", "/api/v1/tickets/"+id, nil)
}

func (h *Handler) completeTicket(r *http.Request, req ExecuteRequest) (any, error) {
	id, ok := req.Payload["id"].(string)
	if !ok || id == "" {
		return nil, fmt.Errorf("payload.id is required")
	}
	status := ticket.StatusCompleted
	updateReq := ticket.UpdateTicketReq{Status: &status}
	if result, ok := req.Payload["result"]; ok {
		if m, ok := result.(map[string]any); ok {
			updateReq.AgentContext = m
		} else {
			updateReq.AgentContext = map[string]any{"result": result}
		}
	}
	return h.callService(r, "PATCH", "/api/v1/tickets/"+id, updateReq)
}

func (h *Handler) assignTicket(r *http.Request, req ExecuteRequest) (any, error) {
	id, ok := req.Payload["id"].(string)
	if !ok || id == "" {
		return nil, fmt.Errorf("payload.id is required")
	}
	domain, _ := req.Payload["domain"].(string)
	assigneeID, _ := req.Payload["assignee_id"].(string)
	if assigneeID == "" && domain != "" {
		assigneeID = "hermes-" + domain
	}

	agentType := ticket.AssigneeAgent
	dom := ticket.Domain(domain)
	updateReq := ticket.UpdateTicketReq{
		AssigneeType: &agentType,
		AssigneeID:   &assigneeID,
		Domain:       &dom,
	}
	return h.callService(r, "PATCH", "/api/v1/tickets/"+id, updateReq)
}

func (h *Handler) listSubtasks(r *http.Request, req ExecuteRequest) (any, error) {
	id, ok := req.Payload["id"].(string)
	if !ok || id == "" {
		return nil, fmt.Errorf("payload.id is required")
	}
	return h.callService(r, "GET", "/api/v1/tickets/"+id+"/subtasks", nil)
}

func (h *Handler) listProjects(r *http.Request, req ExecuteRequest) (any, error) {
	wid, _ := req.Payload["workspace_id"].(string)
	if wid == "" {
		return nil, fmt.Errorf("payload.workspace_id is required")
	}
	return h.callService(r, "GET", "/api/v1/workspaces/"+wid+"/projects", nil)
}

func (h *Handler) callService(r *http.Request, method, path string, body any) (any, error) {
	var bodyReader io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("marshal body: %w", err)
		}
		bodyReader = bytes.NewReader(b)
	}

	outReq, err := http.NewRequestWithContext(r.Context(), method, h.serviceURL+path, bodyReader)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	outReq.Header.Set("Content-Type", "application/json")

	// Authenticate with goquest-service: prefer configured API key,
	// otherwise forward the incoming Authorization header (JWT path).
	if h.apiKey != "" {
		outReq.Header.Set("X-API-Key", h.apiKey)
	} else if auth := r.Header.Get("Authorization"); auth != "" {
		outReq.Header.Set("Authorization", auth)
	}

	resp, err := h.client.Do(outReq)
	if err != nil {
		return nil, fmt.Errorf("call service: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("service error %d: %s", resp.StatusCode, string(respBody))
	}

	var result any
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}
	return result, nil
}

func remapPayload(payload map[string]any, dst any) error {
	b, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	return json.Unmarshal(b, dst)
}

func buildQueryString(payload map[string]any) string {
	if len(payload) == 0 {
		return ""
	}
	q := url.Values{}
	for k, v := range payload {
		q.Set(k, fmt.Sprintf("%v", v))
	}
	return "?" + q.Encode()
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
