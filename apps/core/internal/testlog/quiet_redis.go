package testlog

import (
	"context"

	"github.com/redis/go-redis/v9"
)

type discardRedisLog struct{}

func (discardRedisLog) Printf(context.Context, string, ...any) {}

func SilenceRedis() { redis.SetLogger(discardRedisLog{}) }
