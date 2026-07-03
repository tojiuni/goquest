package idempotency

import (
	"context"
	"time"

	"github.com/redis/go-redis/v9"
)

// RedisStore implements Store on top of Redis using SET NX for the atomic claim.
type RedisStore struct {
	rdb *redis.Client
}

// NewRedisStore returns a RedisStore. A nil client makes every Claim fail open
// (the middleware then processes without idempotency).
func NewRedisStore(rdb *redis.Client) *RedisStore { return &RedisStore{rdb: rdb} }

func (s *RedisStore) Claim(ctx context.Context, key string, ttl time.Duration) (bool, []byte, error) {
	if s.rdb == nil {
		return false, nil, redis.ErrClosed
	}
	ok, err := s.rdb.SetNX(ctx, key, marker, ttl).Result()
	if err != nil {
		return false, nil, err
	}
	if ok {
		return true, nil, nil
	}
	val, err := s.rdb.Get(ctx, key).Bytes()
	if err == redis.Nil {
		// The key expired between SetNX and Get; try to claim once more.
		ok2, err2 := s.rdb.SetNX(ctx, key, marker, ttl).Result()
		if err2 != nil {
			return false, nil, err2
		}
		return ok2, nil, nil
	}
	if err != nil {
		return false, nil, err
	}
	return false, val, nil
}

func (s *RedisStore) Save(ctx context.Context, key string, val []byte, ttl time.Duration) error {
	if s.rdb == nil {
		return redis.ErrClosed
	}
	return s.rdb.Set(ctx, key, val, ttl).Err()
}

func (s *RedisStore) Release(ctx context.Context, key string) error {
	if s.rdb == nil {
		return redis.ErrClosed
	}
	return s.rdb.Del(ctx, key).Err()
}
