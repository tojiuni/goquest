package handler_test

import (
	"context"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"

	"github.com/tojiuni/goquest/internal/directory"
	"github.com/tojiuni/goquest/internal/handler"
)

type fakeLookuper struct {
	result []directory.Actor
	err    error
}

func (f *fakeLookuper) Lookup(_ context.Context, _ []string) ([]directory.Actor, error) {
	return f.result, f.err
}

func TestUsersLookup_ReturnsResolvedActors(t *testing.T) {
	h := handler.NewUsersHandler(&fakeLookuper{result: []directory.Actor{{ID: "a", DisplayName: "Alice"}}})
	r := chi.NewRouter()
	r.Get("/users/lookup", h.Lookup)

	req := httptest.NewRequest("GET", "/users/lookup?ids=a,b", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	assert.Equal(t, 200, rec.Code)
	assert.JSONEq(t, `[{"id":"a","display_name":"Alice","email":""}]`, rec.Body.String())
}

func TestUsersLookup_NoIDs_ReturnsEmptyArray(t *testing.T) {
	h := handler.NewUsersHandler(&fakeLookuper{result: []directory.Actor{{ID: "a"}}})
	r := chi.NewRouter()
	r.Get("/users/lookup", h.Lookup)

	req := httptest.NewRequest("GET", "/users/lookup", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	assert.Equal(t, 200, rec.Code)
	assert.JSONEq(t, `[]`, rec.Body.String())
}

func TestUsersLookup_LookuperReturnsNilWithoutError_ReturnsEmptyArray(t *testing.T) {
	// The unconfigured (noopLookuper) fallback returns (nil, nil) — a bare
	// nil slice must not serialize as JSON `null`; callers expect an array
	// to iterate even when lymphhub isn't wired up yet.
	h := handler.NewUsersHandler(&fakeLookuper{result: nil, err: nil})
	r := chi.NewRouter()
	r.Get("/users/lookup", h.Lookup)

	req := httptest.NewRequest("GET", "/users/lookup?ids=a", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	assert.Equal(t, 200, rec.Code)
	assert.JSONEq(t, `[]`, rec.Body.String())
}

func TestUsersLookup_LookuperError_ReturnsEmptyNot500(t *testing.T) {
	// lymphhub being down must never fail the whole request — this is a
	// UX nicety, not load-bearing (spec §C).
	h := handler.NewUsersHandler(&fakeLookuper{err: assertErr{}})
	r := chi.NewRouter()
	r.Get("/users/lookup", h.Lookup)

	req := httptest.NewRequest("GET", "/users/lookup?ids=a", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	assert.Equal(t, 200, rec.Code)
	assert.JSONEq(t, `[]`, rec.Body.String())
}

type assertErr struct{}

func (assertErr) Error() string { return "lookuper failed" }
