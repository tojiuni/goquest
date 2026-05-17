package queue

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/tojiuni/goquest/internal/ticket"
)

const (
	StreamCreated   = "goquest.tickets.created"
	StreamAssigned  = "goquest.tickets.assigned"
	StreamUpdated   = "goquest.tickets.updated"
	StreamCompleted = "goquest.tickets.completed"
)

type Publisher struct {
	rdb *redis.Client
}

func NewPublisher(rdb *redis.Client) *Publisher {
	return &Publisher{rdb: rdb}
}

type TicketEvent struct {
	TicketID     string `json:"ticket_id"`
	Title        string `json:"title"`
	Domain       string `json:"domain"`
	AssigneeID   string `json:"assignee_id"`
	Status       string `json:"status"`
	Priority     string `json:"priority"`
	AgentContext string `json:"agent_context"`
	SourceBot    string `json:"source_bot"`
	RequesterID  string `json:"requester_id"`
	ParentID     string `json:"parent_id,omitempty"`
	EventTime    string `json:"event_time"`
}

func (p *Publisher) PublishCreated(ctx context.Context, t *ticket.Ticket) error {
	return p.publish(ctx, StreamCreated, t)
}

func (p *Publisher) PublishAssigned(ctx context.Context, t *ticket.Ticket) error {
	return p.publish(ctx, StreamAssigned, t)
}

func (p *Publisher) PublishUpdated(ctx context.Context, t *ticket.Ticket) error {
	return p.publish(ctx, StreamUpdated, t)
}

func (p *Publisher) PublishCompleted(ctx context.Context, t *ticket.Ticket) error {
	return p.publish(ctx, StreamCompleted, t)
}

func (p *Publisher) publish(ctx context.Context, stream string, t *ticket.Ticket) error {
	agentCtxStr := ""
	if t.AgentContext != nil {
		b, err := json.Marshal(t.AgentContext)
		if err == nil {
			agentCtxStr = string(b)
		}
	}

	ev := TicketEvent{
		TicketID:     t.ID.String(),
		Title:        t.Title,
		Domain:       string(t.Domain),
		AssigneeID:   t.AssigneeID,
		Status:       string(t.Status),
		Priority:     string(t.Priority),
		AgentContext: agentCtxStr,
		SourceBot:    t.SourceBot,
		RequesterID:  t.RequesterID,
		EventTime:    time.Now().UTC().Format(time.RFC3339),
	}

	if t.ParentID != nil {
		ev.ParentID = t.ParentID.String()
	}

	b, err := json.Marshal(ev)
	if err != nil {
		return fmt.Errorf("marshal event: %w", err)
	}

	args := &redis.XAddArgs{
		Stream: stream,
		Values: map[string]any{"data": string(b)},
	}
	if err := p.rdb.XAdd(ctx, args).Err(); err != nil {
		return fmt.Errorf("xadd %s: %w", stream, err)
	}
	return nil
}
