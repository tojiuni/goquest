package integration

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/redis/go-redis/v9"
	"github.com/tojiuni/goquest/internal/ticket"
)

type NuclexEvent struct {
	SourceBot       string         `json:"source_bot"`
	EventType       string         `json:"event_type"`
	Severity        string         `json:"severity"`
	Context         map[string]any `json:"context"`
	SuggestedAction string         `json:"suggested_action"`
}

type NuclexEmitter struct {
	rdb    *redis.Client
	stream string
}

func NewNuclexEmitter(rdb *redis.Client, stream string) *NuclexEmitter {
	return &NuclexEmitter{rdb: rdb, stream: stream}
}

func (e *NuclexEmitter) EmitCompleted(ctx context.Context, t *ticket.Ticket) error {
	return e.emit(ctx, t, "ticket.completed", "info", "notify_only")
}

func (e *NuclexEmitter) EmitFailed(ctx context.Context, t *ticket.Ticket) error {
	return e.emit(ctx, t, "ticket.failed", "warning", "update_task")
}

func (e *NuclexEmitter) emit(ctx context.Context, t *ticket.Ticket, eventType, severity, suggestedAction string) error {
	ctx2 := map[string]any{
		"ticket_id": t.ID.String(),
		"title":     t.Title,
		"domain":    string(t.Domain),
	}
	if t.VikunjaTaskID != nil {
		ctx2["vikunja_task_id"] = *t.VikunjaTaskID
	}

	ev := NuclexEvent{
		SourceBot:       "goquest",
		EventType:       eventType,
		Severity:        severity,
		Context:         ctx2,
		SuggestedAction: suggestedAction,
	}

	b, err := json.Marshal(ev)
	if err != nil {
		return fmt.Errorf("marshal nuclex event: %w", err)
	}

	if err := e.rdb.XAdd(ctx, &redis.XAddArgs{
		Stream: e.stream,
		Values: map[string]any{"data": string(b)},
	}).Err(); err != nil {
		return fmt.Errorf("xadd %s: %w", e.stream, err)
	}
	return nil
}
