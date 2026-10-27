package iamsync

import (
	"context"
	"errors"
	"time"

	"github.com/redis/go-redis/v9"
)

const SharedKey = "chatim:iam:revocations"

type RedisClient interface {
	Get(ctx context.Context, key string) *redis.StringCmd
	Set(ctx context.Context, key string, value any, ttl time.Duration) *redis.StatusCmd
}

type redisShared struct {
	client RedisClient
}

func NewRedisShared(client RedisClient) Shared { return redisShared{client: client} }

func (r redisShared) Get(ctx context.Context) ([]byte, error) {
	value, err := r.client.Get(ctx, SharedKey).Bytes()
	if errors.Is(err, redis.Nil) {
		return nil, nil
	}
	return value, err
}

func (r redisShared) Put(ctx context.Context, value []byte, ttl time.Duration) error {
	return r.client.Set(ctx, SharedKey, value, ttl).Err()
}
