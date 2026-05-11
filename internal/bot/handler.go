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
	// Project resolution precedence:
	//   1. payload.project_id (already populated by remapPayload above)
	//   2. payload.workspace_id → first project by created_at (ASC) in that workspace
	//   3. env GOQUEST_DEFAULT_PROJECT_ID
	if createReq.ProjectID == nil {
		if wsRaw, ok := req.Payload["workspace_id"].(string); ok && wsRaw != "" {
			pid, err := h.resolveDefaultProjectForWorkspace(r, wsRaw)
			if err != nil {
				return nil, fmt.Errorf("resolve project for workspace %s: %w", wsRaw, err)
			}
			createReq.ProjectID = &pid
		} else if h.defaultProjectID != "" {
			pid, err := uuid.Parse(h.defaultProjectID)
			if err == nil {
				createReq.ProjectID = &pid
			}
		}
	}
	return h.callService(r, "POST", "/api/v1/tickets", createReq)
}

// resolveDefaultProjectForWorkspace asks goquest-service for the workspace's
// projects and returns the earliest-created one. Used by ticket.create when
// the caller supplies workspace_id but not project_id.
func (h *Handler) resolveDefaultProjectForWorkspace(r *http.Request, workspaceID string) (uuid.UUID, error) {
	if _, err := uuid.Parse(workspaceID); err != nil {
		return uuid.Nil, fmt.Errorf("invalid workspace_id: %w", err)
	}
	resp, err := h.callService(r, "GET", "/api/v1/workspaces/"+workspaceID+"/projects", nil)
	if err != nil {
		return uuid.Nil, err
	}
	envelope, ok := resp.(map[string]any)
	if !ok {
		return uuid.Nil, fmt.Errorf("unexpected projects response shape")
	}
	rawList, _ := envelope["projects"].([]any)
	if len(rawList) == 0 {
		return uuid.Nil, fmt.Errorf("workspace has no projects")
	}
	// Pick the earliest-created project. The service returns DESC by created_at,
	// so scan for the row with the smallest created_at timestamp.
	var (
		bestID  uuid.UUID
		bestTS  string
		bestSet bool
	)
	for _, item := range rawList {
		obj, ok := item.(map[string]any)
		if !ok {
			continue
		}
		idStr, _ := obj["id"].(string)
		createdStr, _ := obj["created_at"].(string)
		if idStr == "" {
			continue
		}
		pid, err := uuid.Parse(idStr)
		if err != nil {
			continue
		}
		if !bestSet || createdStr < bestTS {
			bestID = pid
			bestTS = createdStr
			bestSet = true
		}
	}
	if !bestSet {
		return uuid.Nil, fmt.Errorf("no usable project in workspace")
	}
	return bestID, nil
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
		var resultMap map[string]any
		if m, ok := result.(map[string]any); ok {
			resultMap = m
		} else {
			resultMap = map[string]any{"result": result}
		}
		// Merge result into the current agent_context per SOUL.md §10.
		// Service-side Update replaces agent_context wholesale, so the bot
		// is responsible for fetching the current value and overlaying.
		merged := resultMap
		if cur, err := h.callService(r, "GET", "/api/v1/tickets/"+id, nil); err == nil {
			if curMap, ok := cur.(map[string]any); ok {
				if existing, ok := curMap["agent_context"].(map[string]any); ok && len(existing) > 0 {
					merged = make(map[string]any, len(existing)+len(resultMap))
					for k, v := range existing {
						merged[k] = v
					}
					for k, v := range resultMap {
						merged[k] = v
					}
				}
			}
		}
		updateReq.AgentContext = merged
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

	// Per SOUL.md §14, assigning transitions pending → assigned.
	// Only set Domain when the caller supplied a non-empty value — sending
	// an empty enum string to goquest-service triggers a 500 SQL error.
	agentType := ticket.AssigneeAgent
	assigned := ticket.StatusAssigned
	updateReq := ticket.UpdateTicketReq{
		Status:       &assigned,
		AssigneeType: &agentType,
		AssigneeID:   &assigneeID,
	}
	if domain != "" {
		dom := ticket.Domain(domain)
		updateReq.Domain = &dom
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
