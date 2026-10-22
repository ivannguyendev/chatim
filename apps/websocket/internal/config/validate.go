package config

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/ivannguyendev/chatim/pkg/wspublish"
)

const (
	minFrameMax         = 4 << 10
	maxHeartbeatGap     = 5 * time.Second
	minHeartbeatTimeout = 2 * maxHeartbeatGap
	minConsumeBytes     = 1 << 20
)

type rule struct {
	ok  bool
	msg string
}

func (c Config) Validate() error {
	stop := c.DrainDelay + c.RequestDeadline + FeedStopTimeout + CloseTimeout
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
		{stop <= c.ShutdownBudget, fmt.Sprintf("WS_DRAIN_DELAY + CORE_REQUEST_DEADLINE + %v (feed) + %v (close) = %v must not exceed WS_SHUTDOWN_BUDGET %v", FeedStopTimeout, CloseTimeout, stop, c.ShutdownBudget)},
		{c.NATSURL != "", "NATS_URL is required"},
		{wspublish.ValidStream(c.EventStream) == nil, "EVT_STREAM must be upper-case letters, digits and _"},
		{wspublish.ValidStream(c.PublishStream) == nil, "WS_PUBLISH_STREAM must be upper-case letters, digits and _"},
		{c.EventStream != c.PublishStream, "WS_PUBLISH_STREAM must differ from EVT_STREAM"},
		{wspublish.ValidRoot(c.EventRoot) == nil, "EVT_SUBJECT_ROOT must be dot-separated tokens of a-z, 0-9 and _"},
		{wspublish.ValidRoot(c.PublishRoot) == nil, "WS_PUBLISH_ROOT must be dot-separated tokens of a-z, 0-9 and _"},
		{firstToken(c.EventRoot) != firstToken(c.PublishRoot), "WS_PUBLISH_ROOT must not start with the first token of EVT_SUBJECT_ROOT"},
		positive("EVT_STREAM_REPLICAS", c.StreamReplicas),
		positive("WS_FANOUT_WORKERS", c.FanoutWorkers),
		positive("WS_FANOUT_QUEUE", c.FanoutQueue),
		positive("WS_REPLAY_RATE", c.ReplayRate),
		positive("WS_CONSUME_BATCH", c.ConsumeBatch),
		positive("WS_ROOM_IDS_INFLIGHT", c.RoomIDsInflight),
		positive("WS_OPEN_PENDING", c.OpenPending),
		{c.ConsumeBytes >= minConsumeBytes, fmt.Sprintf("WS_CONSUME_BYTES must be at least %d (the NATS default max payload)", minConsumeBytes)},
	}
	var errs []error
	for _, r := range rules {
		if !r.ok {
			errs = append(errs, errors.New(r.msg))
		}
	}
	return errors.Join(errs...)
}

func firstToken(root string) string {
	first, _, _ := strings.Cut(root, ".")
	return first
}

func positive[T int | time.Duration](key string, v T) rule {
	return rule{v > 0, key + " must be positive"}
}
