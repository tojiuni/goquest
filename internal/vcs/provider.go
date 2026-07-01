package vcs

import (
	"errors"
	"net/http"
)

var ErrBadSignature = errors.New("vcs: bad signature")

// Provider normalizes a git host's webhooks. Adapters live per host; the linker
// pipeline is provider-agnostic.
type Provider interface {
	Name() string
	Verify(r *http.Request, body []byte, secret string) error
	Parse(eventType string, body []byte) ([]VcsEvent, error)
}
