package directory

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type callCountingLookuper struct {
	calls   int
	byID    map[string]Actor
	lastIDs []string
}

func (l *callCountingLookuper) Lookup(_ context.Context, ids []string) ([]Actor, error) {
	l.calls++
	l.lastIDs = ids
	out := make([]Actor, 0, len(ids))
	for _, id := range ids {
		if a, ok := l.byID[id]; ok {
			out = append(out, a)
		}
	}
	return out, nil
}

func TestCachedLookuper_CachesHits(t *testing.T) {
	inner := &callCountingLookuper{byID: map[string]Actor{"a": {ID: "a", DisplayName: "Alice"}}}
	c := NewCachedLookuper(inner, time.Minute)

	first, err := c.Lookup(context.Background(), []string{"a"})
	require.NoError(t, err)
	assert.Equal(t, []Actor{{ID: "a", DisplayName: "Alice"}}, first)
	assert.Equal(t, 1, inner.calls)

	second, err := c.Lookup(context.Background(), []string{"a"})
	require.NoError(t, err)
	assert.Equal(t, []Actor{{ID: "a", DisplayName: "Alice"}}, second)
	assert.Equal(t, 1, inner.calls, "second lookup must be served entirely from cache")
}

func TestCachedLookuper_OnlyFetchesMisses(t *testing.T) {
	inner := &callCountingLookuper{byID: map[string]Actor{
		"a": {ID: "a", DisplayName: "Alice"},
		"b": {ID: "b", DisplayName: "Bob"},
	}}
	c := NewCachedLookuper(inner, time.Minute)

	_, err := c.Lookup(context.Background(), []string{"a"})
	require.NoError(t, err)

	got, err := c.Lookup(context.Background(), []string{"a", "b"})
	require.NoError(t, err)
	assert.ElementsMatch(t, []Actor{{ID: "a", DisplayName: "Alice"}, {ID: "b", DisplayName: "Bob"}}, got)
	assert.Equal(t, []string{"b"}, inner.lastIDs, "second call must only re-fetch the uncached id")
}

func TestCachedLookuper_ExpiresAfterTTL(t *testing.T) {
	inner := &callCountingLookuper{byID: map[string]Actor{"a": {ID: "a", DisplayName: "Alice"}}}
	c := NewCachedLookuper(inner, time.Millisecond)

	_, err := c.Lookup(context.Background(), []string{"a"})
	require.NoError(t, err)
	time.Sleep(5 * time.Millisecond)

	_, err = c.Lookup(context.Background(), []string{"a"})
	require.NoError(t, err)
	assert.Equal(t, 2, inner.calls, "expired entry must be re-fetched, not served stale")
}

func TestCachedLookuper_UnknownIDStaysUncached(t *testing.T) {
	// An id the inner Lookuper doesn't know about must not be cached as a
	// permanent miss — it might exist by the next call (e.g. a new hire).
	inner := &callCountingLookuper{byID: map[string]Actor{}}
	c := NewCachedLookuper(inner, time.Minute)

	_, err := c.Lookup(context.Background(), []string{"ghost"})
	require.NoError(t, err)
	_, err = c.Lookup(context.Background(), []string{"ghost"})
	require.NoError(t, err)
	assert.Equal(t, 2, inner.calls, "unknown id must be retried, not cached as a permanent miss")
}
