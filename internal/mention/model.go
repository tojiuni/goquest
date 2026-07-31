package mention

import (
	"time"

	"github.com/google/uuid"
)

type Source string

const (
	SourceDescription Source = "description"
	SourceComment     Source = "comment"
)

// Mention records that mentionedActorID was @mentioned in a ticket's
// description or a specific comment. CommentID is nil for a description
// mention. SeenAt is nil until the mentioned actor views/dismisses it.
type Mention struct {
	ID               uuid.UUID  `json:"id"`
	TicketID         uuid.UUID  `json:"ticket_id"`
	CommentID        *uuid.UUID `json:"comment_id,omitempty"`
	MentionedActorID string     `json:"mentioned_actor_id"`
	Source           Source     `json:"source"`
	CreatedAt        time.Time  `json:"created_at"`
	CreatedBy        string     `json:"created_by"`
	SeenAt           *time.Time `json:"seen_at,omitempty"`
}

type CreateReq struct {
	TicketID         uuid.UUID
	CommentID        *uuid.UUID
	MentionedActorID string
	Source           Source
	CreatedBy        string
}
