package queue

import (
	"context"
	"log"
	"time"

	"github.com/redis/go-redis/v9"
)

const StreamIncoming = "goquest.tickets.incoming"

type MessageHandler func(ctx context.Context, data string) error

type Consumer struct {
	rdb     *redis.Client
	stream  string
	handler MessageHandler
}

func NewConsumer(rdb *redis.Client, handler MessageHandler) *Consumer {
	return &Consumer{
		rdb:     rdb,
		stream:  StreamIncoming,
		handler: handler,
	}
}

// Run blocks, reading from the stream using XREAD with a blocking timeout.
// It uses "$" as the initial ID so it only processes new messages.
func (c *Consumer) Run(ctx context.Context) error {
	lastID := "$"
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		streams, err := c.rdb.XRead(ctx, &redis.XReadArgs{
			Streams: []string{c.stream, lastID},
			Count:   10,
			Block:   5 * time.Second,
		}).Result()
		if err != nil {
			if err == redis.Nil {
				// timeout, no messages — continue
				continue
			}
			// context cancelled or connection error
			if ctx.Err() != nil {
				return ctx.Err()
			}
			log.Printf("queue consumer xread error: %v", err)
			time.Sleep(2 * time.Second)
			continue
		}

		for _, stream := range streams {
			for _, msg := range stream.Messages {
				lastID = msg.ID
				data, ok := msg.Values["data"].(string)
				if !ok {
					log.Printf("queue consumer: unexpected message format, id=%s", msg.ID)
					continue
				}
				if err := c.handler(ctx, data); err != nil {
					log.Printf("queue consumer: handler error for msg %s: %v", msg.ID, err)
					// log and continue — no retry/DLQ in this first pass
				}
			}
		}
	}
}

// RunForStream runs a consumer on an arbitrary stream.
func RunForStream(ctx context.Context, rdb *redis.Client, stream string, handler MessageHandler) error {
	c := &Consumer{rdb: rdb, stream: stream, handler: handler}
	return c.Run(ctx)
}
