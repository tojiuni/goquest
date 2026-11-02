package handler

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/tojiuni/goquest/internal/approval"
	"github.com/tojiuni/goquest/internal/auth"
)

// DelegationRuleSvc is the subset of approval.Service the rule routes need.
type DelegationRuleSvc interface {
	CreateRule(ctx context.Context, ws uuid.UUID, in approval.CreateRuleInput, creator approval.Actor) (*approval.DelegationRule, error)
	ListRules(ctx context.Context, ws uuid.UUID) ([]*approval.DelegationRule, error)
	RevokeRule(ctx context.Context, ws, id uuid.UUID, by approval.Actor) error
}

// DelegationRuleHandler serves /workspaces/{wid}/delegation-rules. Creating or
// revoking a rule moves approval authority, so the routes are mounted behind
// the workspace-owner gate and the service additionally requires a person
// (PrincipalHuman); an agent or API key holding an owner role still cannot.
type DelegationRuleHandler struct{ svc DelegationRuleSvc }

func NewDelegationRuleHandler(svc DelegationRuleSvc) *DelegationRuleHandler {
	return &DelegationRuleHandler{svc: svc}
}

func (h *DelegationRuleHandler) actor(r *http.Request) approval.Actor {
	return approval.Actor{ID: auth.RequesterID(r), Kind: actorKind(auth.PrincipalKindFrom(r))}
}

func (h *DelegationRuleHandler) Create(w http.ResponseWriter, r *http.Request) {
	ws, err := parseUUID(chi.URLParam(r, "wid"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid workspace id")
		return
	}
	var body struct {
		Principal  string          `json:"principal"`
		Kinds      []approval.Kind `json:"kinds"`
		Repos      []string        `json:"repos"`
		Conditions map[string]any  `json:"conditions"`
		UserRef    string          `json:"user_ref"`
		ExpiresAt  *time.Time      `json:"expires_at"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10)).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
		return
	}
	rule, err := h.svc.CreateRule(r.Context(), ws, approval.CreateRuleInput{
		Principal: body.Principal, Kinds: body.Kinds, Repos: body.Repos,
		Conditions: body.Conditions, UserRef: body.UserRef, ExpiresAt: body.ExpiresAt,
	}, h.actor(r))
	if err != nil {
		h.writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, rule)
}

func (h *DelegationRuleHandler) List(w http.ResponseWriter, r *http.Request) {
	ws, err := parseUUID(chi.URLParam(r, "wid"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid workspace id")
		return
	}
	rules, err := h.svc.ListRules(r.Context(), ws)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if rules == nil {
		rules = []*approval.DelegationRule{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"delegation_rules": rules})
}

func (h *DelegationRuleHandler) Revoke(w http.ResponseWriter, r *http.Request) {
	ws, err := parseUUID(chi.URLParam(r, "wid"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid workspace id")
		return
	}
	id, err := parseUUID(chi.URLParam(r, "rid"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid rule id")
		return
	}
	if err := h.svc.RevokeRule(r.Context(), ws, id, h.actor(r)); err != nil {
		h.writeErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *DelegationRuleHandler) writeErr(w http.ResponseWriter, err error) {
	var denied *approval.DeniedError
	switch {
	case errors.As(err, &denied):
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "a person logged in to the UI is required", "reason": string(denied.Reason)})
	case errors.Is(err, approval.ErrInvalidRule):
		writeError(w, http.StatusUnprocessableEntity, err.Error())
	case errors.Is(err, approval.ErrRuleNotFound):
		writeError(w, http.StatusNotFound, "delegation rule not found")
	default:
		writeError(w, http.StatusInternalServerError, err.Error())
	}
}
