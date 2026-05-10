package handler

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"

	"github.com/google/uuid"
	"github.com/tojiuni/goquest/internal/magic"
)

type MagicSvc interface {
	Request(ctx context.Context, email string, ticketID *uuid.UUID) (string, error)
	Exchange(ctx context.Context, plain string) (*magic.ExchangeResult, error)
}

// EmailDelivery is satisfied by an SMTP/notify package (Phase H4). When nil,
// the handler returns the magic-link URL in the response body for dev/testing.
type EmailDelivery interface {
	SendMagicLink(ctx context.Context, email, magicURL string) error
}

type MagicHandler struct {
	svc       MagicSvc
	deliver   EmailDelivery
	publicURL string // e.g. https://goquest.toji.homes
}

func NewMagicHandler(svc MagicSvc, deliver EmailDelivery, publicURL string) *MagicHandler {
	return &MagicHandler{svc: svc, deliver: deliver, publicURL: publicURL}
}

type requestBody struct {
	Email    string     `json:"email"`
	TicketID *uuid.UUID `json:"ticket_id,omitempty"`
}

// POST /auth/magic-link/request — public route, rate-limited at infra layer.
// Returns 202 Accepted. In dev mode (no email deliverer), the link is included
// in the body — production must wire a real EmailDelivery.
func (h *MagicHandler) Request(w http.ResponseWriter, r *http.Request) {
	var req requestBody
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
		return
	}

	plain, err := h.svc.Request(r.Context(), req.Email, req.TicketID)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	link := h.publicURL + "/auth/magic-link?token=" + plain
	if h.deliver != nil {
		if dErr := h.deliver.SendMagicLink(r.Context(), req.Email, link); dErr != nil {
			log.Printf("warn: magic-link email send failed for %s: %v", req.Email, dErr)
			// Do not leak the link to caller in production paths.
			writeJSON(w, http.StatusAccepted, map[string]any{"sent": false, "error": "email failed"})
			return
		}
		writeJSON(w, http.StatusAccepted, map[string]any{"sent": true})
		return
	}

	// Dev fallback — exposes the link in the response.
	writeJSON(w, http.StatusAccepted, map[string]any{
		"sent":      false,
		"dev_link":  link,
		"dev_token": plain,
	})
}

type exchangeBody struct {
	Token string `json:"token"`
}

// POST /auth/magic-link/exchange — public route. Exchanges a request token for
// a short-lived customer access token (ct_*).
func (h *MagicHandler) Exchange(w http.ResponseWriter, r *http.Request) {
	var req exchangeBody
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
		return
	}
	res, err := h.svc.Exchange(r.Context(), req.Token)
	if err != nil {
		switch {
		case errors.Is(err, magic.ErrNotFound):
			writeError(w, http.StatusNotFound, "token not found")
		case errors.Is(err, magic.ErrExpired):
			writeError(w, http.StatusGone, "token expired")
		case errors.Is(err, magic.ErrUsed):
			writeError(w, http.StatusGone, "token already used")
		default:
			writeError(w, http.StatusBadRequest, err.Error())
		}
		return
	}
	writeJSON(w, http.StatusOK, res)
}
