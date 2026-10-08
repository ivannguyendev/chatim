package config

import (
	"errors"
	"math"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/actor"
	"github.com/ivannguyendev/chatim/apps/core/internal/dedupe"
	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/effects"
	"github.com/ivannguyendev/chatim/apps/core/internal/eventmark"
	"github.com/ivannguyendev/chatim/apps/core/internal/flush"
	"github.com/ivannguyendev/chatim/apps/core/internal/mutate"
	"github.com/ivannguyendev/chatim/apps/core/internal/publish"
	"github.com/ivannguyendev/chatim/apps/core/internal/reconcile"
	"github.com/ivannguyendev/chatim/apps/core/internal/slot"
	"github.com/ivannguyendev/chatim/apps/core/internal/work"
	"github.com/ivannguyendev/chatim/pkg/envconfig"
	"github.com/ivannguyendev/chatim/pkg/grpcserver"
)

const (
	CloseTimeout     = time.Second
	defaultAdminAddr = ":9090"
)

type Config struct {
	CoreID                string
	GRPCAddr              string
	AdvertiseAddr         string
	AdminAddr             string
	MongoURI              string
	MongoDB               string
	MongoUser             string
	MongoPassword         string
	MongoAuthSource       string
	RedisAddr             string
	RedisDB               int
	RedisPassword         string
	RedisDedupeAddr       string
	RedisDedupeDB         int
	RedisDedupePassword   string
	NATSURL               string
	ConnectTimeout        time.Duration
	RequestDeadline       time.Duration
	SlowRPC               time.Duration
	QueueWait             time.Duration
	MaxInflight           int
	DrainDelay            time.Duration
	GRPCShutdown          time.Duration
	PublisherDrain        time.Duration
	ShutdownBudget        time.Duration
	Flush                 flush.Config
	Actor                 actor.Config
	Dedupe                dedupe.Config
	CIDBatch              dedupe.BatchConfig
	Publish               publish.Config
	Stream                publish.StreamConfig
	Work                  work.StreamConfig
	Slot                  slot.Config
	ReconcileEnabled      bool
	Reconcile             reconcile.Config
	EffectDelay           time.Duration
	EffectRoomCache       int
	Effects               effects.Config
	AckMarks              eventmark.Config
	LockedMessageKinds    []domain.Kind
	Limits                mutate.Limits
	ReactionCountDelay    time.Duration
	MemberCountCheckDelay time.Duration
}

type StopPlan struct {
	DrainDelay time.Duration
	GRPC       time.Duration
	Reconciler time.Duration
	Workers    time.Duration
	Router     time.Duration
	CIDBatch   time.Duration
	Flusher    time.Duration
	Publisher  time.Duration
	Slots      time.Duration
	Close      time.Duration
}

func AdminAddr() string { return envconfig.String("CORE_ADMIN_ADDR", defaultAdminAddr) }

func Load() (Config, error) {
	var p parser
	c := Config{
		CoreID:              p.coreID(),
		GRPCAddr:            envconfig.String("CORE_GRPC_ADDR", ":9000"),
		AdminAddr:           AdminAddr(),
		MongoURI:            envconfig.String("MONGO_URI", ""),
		MongoDB:             envconfig.String("MONGO_DB", "chatim"),
		MongoUser:           envconfig.String("MONGO_USER", ""),
		MongoPassword:       p.secret("MONGO_PASSWORD"),
		MongoAuthSource:     envconfig.String("MONGO_AUTH_SOURCE", "admin"),
		RedisAddr:           envconfig.String("REDIS_ADDR", "chatim-redis:6379"),
		RedisDB:             p.index("REDIS_DB", 0),
		RedisPassword:       p.secret("REDIS_PASSWORD"),
		RedisDedupeAddr:     envconfig.String("REDIS_DEDUPE_ADDR", "chatim-redis-dedupe:6379"),
		RedisDedupeDB:       p.index("REDIS_DEDUPE_DB", 0),
		RedisDedupePassword: p.secret("REDIS_DEDUPE_PASSWORD"),
		NATSURL:             envconfig.String("NATS_URL", "nats://chatim-nats:4222"),
		ConnectTimeout:      p.span("CORE_CONNECT_TIMEOUT", 10*time.Second),
		RequestDeadline:     p.span("CORE_REQUEST_DEADLINE", 3*time.Second),
		SlowRPC:             p.span("CORE_SLOW_RPC", grpcserver.DefaultSlowRPC),
		QueueWait:           p.span("CORE_QUEUE_WAIT", 25*time.Millisecond),
		MaxInflight:         p.count("CORE_MAX_INFLIGHT", 2048),
		DrainDelay:          p.span("CORE_DRAIN_DELAY", 2*time.Second),
		GRPCShutdown:        p.span("CORE_GRPC_SHUTDOWN", 5*time.Second),
		PublisherDrain:      p.span("CORE_PUBLISHER_DRAIN", 5*time.Second),
		ShutdownBudget:      p.span("CORE_SHUTDOWN_BUDGET", 28*time.Second),
		LockedMessageKinds:  p.kinds("MESSAGE_LOCKED_KINDS"),
		Limits: mutate.Limits{
			Emojis:      p.listOr("REACTION_EMOJIS", mutate.DefaultEmojis),
			PinLimit:    p.count("PIN_LIMIT", mutate.DefaultPinLimit),
			MemberBatch: p.count("MEMBER_BATCH_MAX", mutate.DefaultMemberBatch),
		},
	}
	c.AdvertiseAddr = p.advertiseAddr(c.CoreID, c.GRPCAddr)
	p.components(&c)
	if err := errors.Join(p.errs...); err != nil {
		return Config{}, err
	}
	if err := c.validate(); err != nil {
		return Config{}, err
	}
	return c, nil
}

func (c Config) StopPlan() StopPlan {
	return StopPlan{
		DrainDelay: c.DrainDelay,
		GRPC:       c.GRPCShutdown,
		Reconciler: c.Reconcile.Drain + CloseTimeout,
		Workers:    c.Effects.Drain + CloseTimeout,
		Router:     c.RequestDeadline,
		CIDBatch:   2 * c.Dedupe.Timeout,
		Flusher:    c.Flush.InsertTimeout,
		Publisher:  c.PublisherDrain,
		Slots:      slot.ReleaseTimeout,
		Close:      CloseTimeout,
	}
}

func (s StopPlan) total() time.Duration {
	var sum time.Duration
	for _, d := range []time.Duration{s.DrainDelay, s.GRPC, s.Reconciler, s.Workers, s.Router, s.CIDBatch, s.Flusher, s.Publisher, s.Slots, s.Close} {
		if d > math.MaxInt64-sum {
			return math.MaxInt64
		}
		sum += d
	}
	return sum
}

func (s StopPlan) fitsWithin(budget time.Duration) bool { return s.total() < budget }
