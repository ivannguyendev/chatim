package config

import (
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/ivannguyendev/chatim/pkg/envconfig"
)

type Config struct {
	CoreID          string
	GRPCAddr        string
	AdvertiseAddr   string
	AdminAddr       string
	MongoURI        string
	MongoDB         string
	RedisAddr       string
	RedisDB         int
	NATSURL         string
	StreamName      string
	SubjectRoot     string
	LiveRoot        string
	StreamReplicas  int
	FlushWindow     time.Duration
	FlushMaxBatch   int
	FlushShards     int
	Mailbox         int
	ActorIdle       time.Duration
	RequestDeadline time.Duration
	MaxInflight     int
	DrainDelay      time.Duration
	GRPCShutdown    time.Duration
	PublisherDrain  time.Duration
	ShutdownBudget  time.Duration
}

func Load() (Config, error) {
	var p parser
	c := Config{
		CoreID:          p.coreID(),
		GRPCAddr:        envconfig.String("CORE_GRPC_ADDR", ":9000"),
		AdminAddr:       envconfig.String("CORE_ADMIN_ADDR", ":9090"),
		MongoURI:        envconfig.String("MONGO_URI", ""),
		MongoDB:         envconfig.String("MONGO_DB", "chatim"),
		RedisAddr:       envconfig.String("REDIS_ADDR", "chatim-redis:6379"),
		RedisDB:         p.integer("REDIS_DB", 0),
		NATSURL:         envconfig.String("NATS_URL", "nats://chatim-nats:4222"),
		StreamName:      envconfig.String("EVT_STREAM", "CHATIM_EVT"),
		SubjectRoot:     envconfig.String("EVT_SUBJECT_ROOT", "evt"),
		LiveRoot:        envconfig.String("EVT_LIVE_ROOT", "live"),
		StreamReplicas:  p.integer("EVT_STREAM_REPLICAS", 1),
		FlushWindow:     p.duration("FLUSH_WINDOW", 2*time.Millisecond),
		FlushMaxBatch:   p.integer("FLUSH_MAX_BATCH", 256),
		FlushShards:     p.integer("FLUSH_SHARDS", 4),
		Mailbox:         p.integer("ACTOR_MAILBOX", 1024),
		ActorIdle:       p.duration("ACTOR_IDLE", 5*time.Minute),
		RequestDeadline: p.duration("CORE_REQUEST_DEADLINE", 3*time.Second),
		MaxInflight:     p.integer("CORE_MAX_INFLIGHT", 2048),
		DrainDelay:      p.duration("CORE_DRAIN_DELAY", 2*time.Second),
		GRPCShutdown:    p.duration("CORE_GRPC_SHUTDOWN", 10*time.Second),
		PublisherDrain:  p.duration("CORE_PUBLISHER_DRAIN", 5*time.Second),
		ShutdownBudget:  p.duration("CORE_SHUTDOWN_BUDGET", 25*time.Second),
	}
	c.AdvertiseAddr = envconfig.String("CORE_ADVERTISE_ADDR", c.CoreID+":9000")
	if err := errors.Join(p.errs...); err != nil {
		return Config{}, err
	}
	if err := c.validate(); err != nil {
		return Config{}, err
	}
	return c, nil
}

type parser struct {
	errs []error
}

func (p *parser) coreID() string {
	if id := os.Getenv("CORE_ID"); id != "" {
		return id
	}
	host, err := os.Hostname()
	if err != nil {
		p.errs = append(p.errs, fmt.Errorf("CORE_ID unset and hostname unavailable: %w", err))
	}
	return host
}

func (p *parser) integer(key string, def int) int {
	v, err := envconfig.Int(key, def)
	p.errs = append(p.errs, err)
	return v
}

func (p *parser) duration(key string, def time.Duration) time.Duration {
	v, err := envconfig.Duration(key, def)
	p.errs = append(p.errs, err)
	return v
}
