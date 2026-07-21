package directory

import (
	"context"
	"sync"
	"time"
)

type cacheEntry struct {
	actor   Actor
	expires time.Time
}

// CachedLookuper wraps a Lookuper with an in-process TTL cache so a page
// with many distinct requester/assignee ids costs one upstream call for the
// cache misses, not one call per row. Unknown ids are never cached — a
// negative lookup might become positive on the next call (new hire, actor
// created after this process started), and the cost of re-checking a miss is
// the same as the cost of the original miss.
type CachedLookuper struct {
	inner Lookuper
	ttl   time.Duration

	mu      sync.Mutex
	entries map[string]cacheEntry
}

func NewCachedLookuper(inner Lookuper, ttl time.Duration) *CachedLookuper {
	return &CachedLookuper{inner: inner, ttl: ttl, entries: make(map[string]cacheEntry)}
}

func (c *CachedLookuper) Lookup(ctx context.Context, ids []string) ([]Actor, error) {
	now := time.Now()
	hits := make([]Actor, 0, len(ids))
	var missIDs []string

	c.mu.Lock()
	for _, id := range ids {
		if e, ok := c.entries[id]; ok && now.Before(e.expires) {
			hits = append(hits, e.actor)
		} else {
			missIDs = append(missIDs, id)
		}
	}
	c.mu.Unlock()

	if len(missIDs) == 0 {
		return hits, nil
	}

	fresh, err := c.inner.Lookup(ctx, missIDs)
	if err != nil {
		return nil, err
	}

	c.mu.Lock()
	for _, a := range fresh {
		c.entries[a.ID] = cacheEntry{actor: a, expires: now.Add(c.ttl)}
	}
	c.mu.Unlock()

	return append(hits, fresh...), nil
}
