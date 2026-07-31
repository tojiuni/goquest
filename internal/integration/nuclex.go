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
	return e.emit(ctx, t, "ticket.completed", "info", "update_task")
}

func (e *NuclexEmitter) EmitFailed(ctx context.Context, t *ticket.Ticket) error {
	return e.emit(ctx, t, "ticket.failed", "warning", "notify_only")
}

func (e *NuclexEmitter) EmitPendingApproval(ctx context.Context, t *ticket.Ticket) error {
	return e.emit(ctx, t, "ticket.pending_approval", "warning", "notify_only")
}

func (e *NuclexEmitter) EmitCreated(ctx context.Context, t *ticket.Ticket) error {
	return e.emit(ctx, t, "ticket.created", "info", "")
}

// EmitMentioned announces that mentionedActorID was @mentioned on t. A
// secondary notification channel next to the in-app mention inbox — callers
// treat failures as best-effort (the stored mention is the source of truth).
func (e *NuclexEmitter) EmitMentioned(ctx context.Context, t *ticket.Ticket, mentionedActorID string) error {
	return e.emitWith(ctx, t, "ticket.mentioned", "info", "notify_only",
		map[string]any{"mentioned_actor_id": mentionedActorID})
}

func (e *NuclexEmitter) emit(ctx context.Context, t *ticket.Ticket, eventType, severity, suggestedAction string) error {
	return e.emitWith(ctx, t, eventType, severity, suggestedAction, nil)
}

func (e *NuclexEmitter) emitWith(ctx context.Context, t *ticket.Ticket, eventType, severity, suggestedAction string, extra map[string]any) error {
	ctx2 := map[string]any{
		"ticket_id": t.ID.String(),
		"title":     t.Title,
		"domain":    string(t.Domain),
	}
	for k, v := range extra {
		ctx2[k] = v
	}

	if t.Metadata != nil {
		if vid, ok := t.Metadata["vikunja_task_id"]; ok {
			ctx2["vikunja_task_id"] = vid
		}
		if rs, ok := t.Metadata["result_summary"]; ok {
			ctx2["result_summary"] = rs
		}
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
