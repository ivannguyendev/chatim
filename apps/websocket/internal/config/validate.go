package config

import (
	"errors"
	"fmt"
	"time"
)

const (
	minFrameMax         = 4 << 10
	maxHeartbeatGap     = 5 * time.Second
	minHeartbeatTimeout = 2 * maxHeartbeatGap
)

type rule struct {
	ok  bool
	msg string
}

func (c Config) Validate() error {
	stop := c.DrainDelay + c.RequestDeadline + CloseTimeout
	rules := []rule{
		{c.NodeID != "", "WS_NODE_ID is required"},
		{c.ListenAddr != "", "WS_ADDR is required"},
		{c.AdminAddr != "", "WS_ADMIN_ADDR is required"},
		{c.RedisAddr != "", "REDIS_ADDR is required"},
		{c.RedisDB >= 0, "REDIS_DB must not be negative"},
		{c.JWTKeysFile != "", "WS_JWT_KEYS_FILE is required"},
		{c.JWTIssuer != "", "WS_JWT_ISSUER is required"},
		{c.JWTAudience != "", "WS_JWT_AUDIENCE is required"},
		{c.JWTLeeway >= 0, "WS_JWT_LEEWAY must not be negative"},
		positive("WS_AUTH_TIMEOUT", c.AuthTimeout),
		positive("WS_UNAUTH_MAX", c.UnauthMax),
		positive("WS_FRAME_MAX", c.FrameMax),
		positive("WS_HEARTBEAT_TIMEOUT", c.HeartbeatTimeout),
		positive("WS_COMMAND_RATE", c.CommandRate),
		positive("WS_COMMAND_BURST", c.CommandBurst),
		positive("WS_COMMAND_INFLIGHT", c.CommandInflight),
		positive("WS_SEND_QUEUE", c.SendQueue),
		positive("WS_WRITE_TIMEOUT", c.WriteTimeout),
		positive("CORE_REQUEST_DEADLINE", c.RequestDeadline),
		positive("WS_DRAIN_DELAY", c.DrainDelay),
		positive("WS_SHUTDOWN_BUDGET", c.ShutdownBudget),
		{c.FrameMax <= 0 || c.FrameMax >= minFrameMax, fmt.Sprintf("WS_FRAME_MAX must be at least %d", minFrameMax)},
		{c.HeartbeatTimeout <= 0 || c.HeartbeatTimeout >= minHeartbeatTimeout, fmt.Sprintf("WS_HEARTBEAT_TIMEOUT must be at least %v (two heartbeat gaps of %v)", minHeartbeatTimeout, maxHeartbeatGap)},
		{stop <= c.ShutdownBudget, fmt.Sprintf("WS_DRAIN_DELAY + CORE_REQUEST_DEADLINE + 1s (close) = %v must not exceed WS_SHUTDOWN_BUDGET %v", stop, c.ShutdownBudget)},
	}
	var errs []error
	for _, r := range rules {
		if !r.ok {
			errs = append(errs, errors.New(r.msg))
		}
	}
	return errors.Join(errs...)
}

func positive[T int | time.Duration](key string, v T) rule {
	return rule{v > 0, key + " must be positive"}
}
