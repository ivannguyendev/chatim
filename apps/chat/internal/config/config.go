package config

import (
	"errors"
	"math"
	"time"

	"github.com/ivannguyendev/chatim/apps/chat/internal/dedupe"
	"github.com/ivannguyendev/chatim/apps/chat/internal/event/projection"
	"github.com/ivannguyendev/chatim/apps/chat/internal/event/publish"
	"github.com/ivannguyendev/chatim/apps/chat/internal/event/readbatch"
	"github.com/ivannguyendev/chatim/apps/chat/internal/event/stateclear"
	"github.com/ivannguyendev/chatim/apps/chat/internal/event/sweep"
	"github.com/ivannguyendev/chatim/apps/chat/internal/event/timer"
	"github.com/ivannguyendev/chatim/apps/chat/internal/interaction"
	"github.com/ivannguyendev/chatim/apps/chat/internal/member"
	"github.com/ivannguyendev/chatim/apps/chat/internal/message"
	"github.com/ivannguyendev/chatim/apps/chat/internal/message/actor"
	"github.com/ivannguyendev/chatim/apps/chat/internal/message/flush"
	"github.com/ivannguyendev/chatim/apps/chat/internal/model/domain"
	"github.com/ivannguyendev/chatim/apps/chat/internal/platform/slot"
	"github.com/ivannguyendev/chatim/modules/media/mediaapp"
	"github.com/ivannguyendev/chatim/modules/mention/mentionapp"
	"github.com/ivannguyendev/chatim/pkg/core/envconfig"
	"github.com/ivannguyendev/chatim/pkg/core/grpcserver"
)

const (
	CloseTimeout     = time.Second
	defaultAdminAddr = ":9090"
)

type Config struct {
	NodeID                 string
	GRPCAddr               string
	AdvertiseAddr          string
	AdminAddr              string
	MongoURI               string
	MongoDB                string
	MongoUser              string
	MongoPassword          string
	MongoAuthSource        string
	RedisAddr              string
	RedisDB                int
	RedisPassword          string
	RedisDedupeAddr        string
	RedisDedupeDB          int
	RedisDedupePassword    string
	NATSURL                string
	ConnectTimeout         time.Duration
	RequestDeadline        time.Duration
	SlowRPC                time.Duration
	QueueWait              time.Duration
	MaxInflight            int
	DrainDelay             time.Duration
	GRPCShutdown           time.Duration
	PublisherDrain         time.Duration
	ShutdownBudget         time.Duration
	Flush                  flush.Config
	Actor                  actor.Config
	Dedupe                 dedupe.Config
	CIDBatch               dedupe.BatchConfig
	Publish                publish.Config
	Stream                 publish.StreamConfig
	Timer                  timer.StreamConfig
	Slot                   slot.Config
	StateClear             stateclear.Config
	ReadBatch              readbatch.Config
	Sweep                  sweep.Config
	Projection             projection.Config
	Mention                mentionapp.Config
	Media                  mediaapp.Config
	LockedMessageKinds     []domain.Kind
	Messages               message.Limits
	Interactions           interaction.Limits
	Members                member.Limits
	MemberCountCheckDelay  time.Duration
	MessageCountCheckDelay time.Duration
	RoomList               RoomListConfig
}

type RoomListConfig struct {
	JoinMax          int
	TailCacheBytes   int
	InfoCacheEntries int
}

type StopPlan struct {
	DrainDelay  time.Duration
	GRPC        time.Duration
	Sweeper     time.Duration
	Projections time.Duration
	Mention     time.Duration
	Media       time.Duration
	Timer       time.Duration
	Router      time.Duration
	CIDBatch    time.Duration
	Flusher     time.Duration
	Publisher   time.Duration
	StateClear  time.Duration
	Slots       time.Duration
	Close       time.Duration
}

func AdminAddr() string { return envconfig.String("CHAT_ADMIN_ADDR", defaultAdminAddr) }

func Load() (Config, error) {
	var p parser
	c := Config{
		NodeID:              p.nodeID(),
		GRPCAddr:            envconfig.String("CHAT_GRPC_ADDR", ":9000"),
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
		ConnectTimeout:      p.span("CHAT_CONNECT_TIMEOUT", 10*time.Second),
		RequestDeadline:     p.span("CHAT_REQUEST_DEADLINE", 3*time.Second),
		SlowRPC:             p.span("CHAT_SLOW_RPC", grpcserver.DefaultSlowRPC),
		QueueWait:           p.span("CHAT_QUEUE_WAIT", 25*time.Millisecond),
		MaxInflight:         p.count("CHAT_MAX_INFLIGHT", 2048),
		DrainDelay:          p.span("CHAT_DRAIN_DELAY", 2*time.Second),
		GRPCShutdown:        p.span("CHAT_GRPC_SHUTDOWN", 5*time.Second),
		PublisherDrain:      p.span("CHAT_PUBLISHER_DRAIN", 5*time.Second),
		ShutdownBudget:      p.span("CHAT_SHUTDOWN_BUDGET", 30*time.Second),
		LockedMessageKinds:  p.kinds("MESSAGE_LOCKED_KINDS"),
		Interactions: interaction.Limits{
			Emojis:   p.listOr("REACTION_EMOJIS", interaction.DefaultEmojis),
			PinLimit: p.count("PIN_LIMIT", interaction.DefaultPinLimit),
		},
		Members: member.Limits{MemberBatch: p.count("MEMBER_BATCH_MAX", member.DefaultMemberBatch)},
	}
	c.AdvertiseAddr = p.advertiseAddr(c.NodeID, c.GRPCAddr)
	p.components(&c)
	c.RoomList = p.roomList()
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
		DrainDelay:  c.DrainDelay,
		GRPC:        c.GRPCShutdown,
		Sweeper:     CloseTimeout,
		Projections: c.Projection.StopTimeout(),
		Mention:     c.mentionStop(),
		Media:       c.mediaStop(),
		Timer:       timer.ConsumerConfig{}.StopTimeout(),
		Router:      c.RequestDeadline,
		CIDBatch:    2 * c.Dedupe.Timeout,
		Flusher:     c.Flush.InsertTimeout,
		Publisher:   c.PublisherDrain,
		StateClear:  stateclear.DrainTimeout,
		Slots:       slot.ReleaseTimeout,
		Close:       CloseTimeout,
	}
}

func (s StopPlan) Total() time.Duration {
	var sum time.Duration
	for _, d := range []time.Duration{
		s.DrainDelay, s.GRPC, s.Sweeper, max(s.Projections, s.Mention, s.Media), s.Timer, s.Router, s.CIDBatch, s.Flusher, s.Publisher, s.StateClear, s.Slots, s.Close,
	} {
		if d > math.MaxInt64-sum {
			return math.MaxInt64
		}
		sum += d
	}
	return sum
}

func (c Config) mentionStop() time.Duration {
	if !c.Mention.Enabled {
		return 0
	}
	return c.Mention.StopTimeout()
}

func (c Config) mediaStop() time.Duration {
	if !c.Media.Enabled {
		return 0
	}
	return mediaapp.StopTimeout()
}

func (s StopPlan) fitsWithin(budget time.Duration) bool { return s.Total() < budget }
