package config

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"runtime"
	"time"

	"github.com/ivannguyendev/chatim/pkg/envconfig"
	"github.com/ivannguyendev/chatim/pkg/redact"
	"github.com/ivannguyendev/chatim/pkg/wspublish"
)

const (
	CloseTimeout     = time.Second
	FeedStopTimeout  = 2 * time.Second
	defaultAdminAddr = ":9090"
)

type Config struct {
	NodeID            string
	ListenAddr        string
	AdminAddr         string
	RedisAddr         string
	RedisDB           int
	RedisPassword     string
	JWTKeysFile       string
	JWTIssuer         string
	JWTAudience       string
	JWTLeeway         time.Duration
	AuthTimeout       time.Duration
	UnauthMax         int
	FrameMax          int
	HeartbeatTimeout  time.Duration
	CommandRate       int
	CommandBurst      int
	CommandInflight   int
	SendQueue         int
	SendQueueBytes    int
	WriteTimeout      time.Duration
	RequestDeadline   time.Duration
	DrainDelay        time.Duration
	ShutdownBudget    time.Duration
	NATSURL           string
	EventStream       string
	EventRoot         string
	PublishRoot       string
	PublishStream     string
	StreamReplicas    int
	FanoutWorkers     int
	FanoutQueue       int
	ReplayRate        int
	ConsumeBatch      int
	ConsumeBytes      int
	RoomIDsInflight   int
	OpenPending       time.Duration
	ReplayMaxAge      time.Duration
	ReloadRate        int
	TypingRate        int
	TypingBurst       int
	TypingQueue       int
	TypingPendingMsgs int

	PresenceRedisAddr     string
	PresenceRedisDB       int
	PresenceRedisPassword string
	PresenceRedisTimeout  time.Duration
	PresenceRedisPool     int
	PresenceTTL           time.Duration
	PresenceStampEvery    time.Duration
	PresenceFlush         time.Duration
	PresenceBatch         int
	PresenceCache         time.Duration
	PresenceWatchMax      int
}

func AdminAddr() string { return envconfig.String("WS_ADMIN_ADDR", defaultAdminAddr) }

func Load() (Config, error) {
	var p parser
	c := Config{
		NodeID:            p.nodeID(),
		ListenAddr:        envconfig.String("WS_ADDR", ":8080"),
		AdminAddr:         AdminAddr(),
		RedisAddr:         envconfig.String("REDIS_ADDR", "chatim-redis:6379"),
		RedisDB:           p.number("REDIS_DB", 0),
		RedisPassword:     p.secret("REDIS_PASSWORD"),
		JWTKeysFile:       envconfig.String("WS_JWT_KEYS_FILE", ""),
		JWTIssuer:         envconfig.String("WS_JWT_ISSUER", ""),
		JWTAudience:       envconfig.String("WS_JWT_AUDIENCE", ""),
		JWTLeeway:         p.span("WS_JWT_LEEWAY", 30*time.Second),
		AuthTimeout:       p.span("WS_AUTH_TIMEOUT", 5*time.Second),
		UnauthMax:         p.number("WS_UNAUTH_MAX", 10000),
		FrameMax:          p.number("WS_FRAME_MAX", 128<<10),
		HeartbeatTimeout:  p.span("WS_HEARTBEAT_TIMEOUT", 15*time.Second),
		CommandRate:       p.number("WS_COMMAND_RATE", 50),
		CommandBurst:      p.number("WS_COMMAND_BURST", 100),
		CommandInflight:   p.number("WS_COMMAND_INFLIGHT", 32),
		SendQueue:         p.number("WS_SEND_QUEUE", 256),
		SendQueueBytes:    p.number("WS_SEND_QUEUE_BYTES", 1<<20),
		WriteTimeout:      p.span("WS_WRITE_TIMEOUT", 10*time.Second),
		RequestDeadline:   p.span("CORE_REQUEST_DEADLINE", 5*time.Second),
		DrainDelay:        p.span("WS_DRAIN_DELAY", 2*time.Second),
		ShutdownBudget:    p.span("WS_SHUTDOWN_BUDGET", 15*time.Second),
		NATSURL:           envconfig.String("NATS_URL", ""),
		EventStream:       envconfig.String("EVT_STREAM", "CHATIM_EVT"),
		EventRoot:         envconfig.String("EVT_SUBJECT_ROOT", "evt"),
		PublishRoot:       envconfig.String("WS_PUBLISH_ROOT", wspublish.DefaultRoot),
		PublishStream:     envconfig.String("WS_PUBLISH_STREAM", wspublish.DefaultStream),
		StreamReplicas:    p.number("EVT_STREAM_REPLICAS", 1),
		FanoutWorkers:     p.number("WS_FANOUT_WORKERS", runtime.NumCPU()),
		FanoutQueue:       p.number("WS_FANOUT_QUEUE", 4096),
		ReplayRate:        p.number("WS_REPLAY_RATE", 20000),
		ConsumeBatch:      p.number("WS_CONSUME_BATCH", 500),
		ConsumeBytes:      p.number("WS_CONSUME_BYTES", 8<<20),
		RoomIDsInflight:   p.number("WS_ROOM_IDS_INFLIGHT", 64),
		OpenPending:       p.span("WS_OPEN_PENDING", 10*time.Second),
		ReplayMaxAge:      p.span("WS_REPLAY_MAX_AGE", 2*time.Minute),
		ReloadRate:        p.number("WS_RELOAD_RATE", 1000),
		TypingRate:        p.number("WS_TYPING_RATE", 2),
		TypingBurst:       p.number("WS_TYPING_BURST", 4),
		TypingQueue:       p.number("WS_TYPING_QUEUE", 8192),
		TypingPendingMsgs: p.number("WS_TYPING_PENDING_MSGS", 65536),

		PresenceRedisAddr:     envconfig.String("WS_PRESENCE_REDIS_ADDR", "chatim-redis-presence:6379"),
		PresenceRedisDB:       p.number("WS_PRESENCE_REDIS_DB", 0),
		PresenceRedisPassword: p.secret("WS_PRESENCE_REDIS_PASSWORD"),
		PresenceRedisTimeout:  p.span("WS_PRESENCE_REDIS_TIMEOUT", 500*time.Millisecond),
		PresenceRedisPool:     p.number("WS_PRESENCE_REDIS_POOL", 64),
		PresenceTTL:           p.span("WS_PRESENCE_TTL", 15*time.Second),
		PresenceStampEvery:    p.span("WS_PRESENCE_STAMP_EVERY", 2*time.Second),
		PresenceFlush:         p.span("WS_PRESENCE_FLUSH", time.Second),
		PresenceBatch:         p.number("WS_PRESENCE_BATCH", 500),
		PresenceCache:         p.span("WS_PRESENCE_CACHE", 5*time.Second),
		PresenceWatchMax:      p.number("WS_PRESENCE_WATCH_MAX", 200),
	}
	if err := errors.Join(p.errs...); err != nil {
		return Config{}, err
	}
	if err := c.Validate(); err != nil {
		return Config{}, err
	}
	return c, nil
}

func (c Config) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("node_id", c.NodeID),
		slog.String("listen_addr", c.ListenAddr),
		slog.String("admin_addr", c.AdminAddr),
		slog.String("redis_addr", c.RedisAddr),
		slog.Int("redis_db", c.RedisDB),
		slog.Bool("redis_auth", c.RedisPassword != ""),
		slog.String("jwt_keys_file", c.JWTKeysFile),
		slog.String("jwt_issuer", c.JWTIssuer),
		slog.String("jwt_audience", c.JWTAudience),
		slog.Duration("jwt_leeway", c.JWTLeeway),
		slog.Duration("auth_timeout", c.AuthTimeout),
		slog.Int("unauth_max", c.UnauthMax),
		slog.Int("frame_max", c.FrameMax),
		slog.Duration("heartbeat_timeout", c.HeartbeatTimeout),
		slog.Int("command_rate", c.CommandRate),
		slog.Int("command_burst", c.CommandBurst),
		slog.Int("command_inflight", c.CommandInflight),
		slog.Int("send_queue", c.SendQueue),
		slog.Int("send_queue_bytes", c.SendQueueBytes),
		slog.Duration("write_timeout", c.WriteTimeout),
		slog.Duration("request_deadline", c.RequestDeadline),
		slog.Duration("drain_delay", c.DrainDelay),
		slog.Duration("shutdown_budget", c.ShutdownBudget),
		slog.String("nats_url", redact.URL(c.NATSURL)),
		slog.String("event_stream", c.EventStream),
		slog.String("event_root", c.EventRoot),
		slog.String("publish_root", c.PublishRoot),
		slog.String("publish_stream", c.PublishStream),
		slog.Int("stream_replicas", c.StreamReplicas),
		slog.Int("fanout_workers", c.FanoutWorkers),
		slog.Int("fanout_queue", c.FanoutQueue),
		slog.Int("replay_rate", c.ReplayRate),
		slog.Int("consume_batch", c.ConsumeBatch),
		slog.Int("consume_bytes", c.ConsumeBytes),
		slog.Int("room_ids_inflight", c.RoomIDsInflight),
		slog.Duration("open_pending", c.OpenPending),
		slog.Duration("replay_max_age", c.ReplayMaxAge),
		slog.Int("reload_rate", c.ReloadRate),
		slog.Int("typing_rate", c.TypingRate),
		slog.Int("typing_burst", c.TypingBurst),
		slog.Int("typing_queue", c.TypingQueue),
		slog.Int("typing_pending_msgs", c.TypingPendingMsgs),
		slog.String("presence_redis_addr", c.PresenceRedisAddr),
		slog.Int("presence_redis_db", c.PresenceRedisDB),
		slog.Bool("presence_redis_auth", c.PresenceRedisPassword != ""),
		slog.Duration("presence_redis_timeout", c.PresenceRedisTimeout),
		slog.Int("presence_redis_pool", c.PresenceRedisPool),
		slog.Duration("presence_ttl", c.PresenceTTL),
		slog.Duration("presence_stamp_every", c.PresenceStampEvery),
		slog.Duration("presence_flush", c.PresenceFlush),
		slog.Int("presence_batch", c.PresenceBatch),
		slog.Duration("presence_cache", c.PresenceCache),
		slog.Int("presence_watch_max", c.PresenceWatchMax),
	)
}

func (c Config) String() string { return c.LogValue().String() }

type parser struct {
	errs []error
}

func (p *parser) fail(err error) { p.errs = append(p.errs, err) }

func (p *parser) nodeID() string {
	if id := os.Getenv("WS_NODE_ID"); id != "" {
		return id
	}
	host, err := os.Hostname()
	if err != nil {
		p.fail(fmt.Errorf("WS_NODE_ID unset and hostname unavailable: %w", err))
	}
	return host
}

func (p *parser) number(key string, def int) int {
	v, err := envconfig.Int(key, def)
	if err != nil {
		p.fail(err)
	}
	return v
}

func (p *parser) span(key string, def time.Duration) time.Duration {
	v, err := envconfig.Duration(key, def)
	if err != nil {
		p.fail(err)
	}
	return v
}

func (p *parser) secret(key string) string {
	v, err := envconfig.Secret(key)
	if err != nil {
		p.fail(err)
	}
	return v
}
