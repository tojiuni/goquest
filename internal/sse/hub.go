// Package sse fans out Redis stream events to connected HTTP clients.
package sse

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"
)

const (
	heartbeatInterval = 25 * time.Second
	readBlockTimeout  = 5 * time.Second
)

// Event is forwarded to each connected client.
type Event struct {
	Stream string `json:"stream"`
	Data   string `json:"data"`
}

// client is a single connected SSE subscriber.
type client struct {
	ch     chan Event
	userID string
	// filter narrows which events this subscriber receives. Both empty = no filter.
	filterWorkspaceID string
	filterTicketID    string
}

// Hub subscribes to one or more Redis streams and broadcasts events to HTTP clients.
type Hub struct {
	rdb     *redis.Client
	streams []string

	mu      sync.RWMutex
	clients map[*client]struct{}
}

// New creates a Hub that reads from the given Redis streams.
func New(rdb *redis.Client, streams ...string) *Hub {
	return &Hub{
		rdb:     rdb,
		streams: streams,
		clients: make(map[*client]struct{}),
	}
}

// Run starts reading from Redis streams and broadcasting to clients. Blocks until ctx is cancelled.
func (h *Hub) Run(ctx context.Context) error {
	// Build initial "$" positions so we only receive new messages.
	lastIDs := make([]string, len(h.streams))
	for i := range lastIDs {
		lastIDs[i] = "$"
	}

	for {
		args := &redis.XReadArgs{
			Streams: buildStreamsArg(h.streams, lastIDs),
			Block:   readBlockTimeout,
			Count:   100,
		}

		results, err := h.rdb.XRead(ctx, args).Result()
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			if err == redis.Nil {
				continue // block timeout, no messages — normal
			}
			log.Printf("sse hub xread: %v", err)
			continue
		}

		for _, stream := range results {
			idx := streamIndex(h.streams, stream.Stream)
			for _, msg := range stream.Messages {
				lastIDs[idx] = msg.ID
				data, _ := msg.Values["data"].(string)
				h.broadcast(Event{Stream: stream.Stream, Data: data})
			}
		}
	}
}

func (h *Hub) broadcast(ev Event) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	for c := range h.clients {
		if !c.matches(ev) {
			continue
		}
		select {
		case c.ch <- ev:
		default: // slow client — drop rather than block
		}
	}
}

func (h *Hub) subscribe(userID, filterWorkspace, filterTicket string) *client {
	c := &client{
		ch:                make(chan Event, 32),
		userID:            userID,
		filterWorkspaceID: filterWorkspace,
		filterTicketID:    filterTicket,
	}
	h.mu.Lock()
	h.clients[c] = struct{}{}
	h.mu.Unlock()
	return c
}

// matches returns true if the event should be delivered to this client given
// its filter. Filtering uses substring match on the JSON payload — sufficient
// because Redis stream events serialise IDs as plain strings. A more precise
// filter would parse the JSON envelope per event.
func (c *client) matches(ev Event) bool {
	if c.filterTicketID == "" && c.filterWorkspaceID == "" {
		return true
	}
	if c.filterTicketID != "" && contains(ev.Data, c.filterTicketID) {
		return true
	}
	if c.filterWorkspaceID != "" && contains(ev.Data, c.filterWorkspaceID) {
		return true
	}
	return false
}

func contains(s, substr string) bool {
	if len(substr) == 0 {
		return true
	}
	for i := 0; i+len(substr) <= len(s); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

func (h *Hub) unsubscribe(c *client) {
	h.mu.Lock()
	delete(h.clients, c)
	h.mu.Unlock()
}

// ServeHTTP implements http.Handler — upgrades the connection to an SSE stream.
func (h *Hub) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming not supported", http.StatusInternalServerError)
		return
	}

	userID := r.Header.Get("X-User-ID") // set by auth middleware upstream

	// Phase H3: clients can narrow events to a single ticket or workspace via
	//   ?filter=ticket:<uuid>   — single-ticket stream (customer detail page)
	//   ?filter=workspace:<uuid> — workspace inbox stream
	// Empty filter receives all events (legacy / staff dashboard).
	var filterWorkspace, filterTicket string
	if f := r.URL.Query().Get("filter"); f != "" {
		const wsP, tP = "workspace:", "ticket:"
		if len(f) > len(wsP) && f[:len(wsP)] == wsP {
			filterWorkspace = f[len(wsP):]
		} else if len(f) > len(tP) && f[:len(tP)] == tP {
			filterTicket = f[len(tP):]
		}
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no") // disable nginx buffering

	c := h.subscribe(userID, filterWorkspace, filterTicket)
	defer h.unsubscribe(c)

	heartbeat := time.NewTicker(heartbeatInterval)
	defer heartbeat.Stop()

	for {
		select {
		case <-r.Context().Done():
			return
		case <-heartbeat.C:
			fmt.Fprintf(w, ": heartbeat\n\n")
			flusher.Flush()
		case ev, ok := <-c.ch:
			if !ok {
				return
			}
			b, _ := json.Marshal(ev)
			fmt.Fprintf(w, "data: %s\n\n", b)
			flusher.Flush()
		}
	}
}

func buildStreamsArg(streams, lastIDs []string) []string {
	arg := make([]string, 0, len(streams)*2)
	arg = append(arg, streams...)
	arg = append(arg, lastIDs...)
	return arg
}

func streamIndex(streams []string, name string) int {
	for i, s := range streams {
		if s == name {
			return i
		}
	}
	return 0
}
