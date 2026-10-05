package config

import (
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/actor"
	"github.com/ivannguyendev/chatim/apps/core/internal/dedupe"
	"github.com/ivannguyendev/chatim/apps/core/internal/eventmark"
	"github.com/ivannguyendev/chatim/apps/core/internal/flush"
	"github.com/ivannguyendev/chatim/apps/core/internal/publish"
	"github.com/ivannguyendev/chatim/apps/core/internal/reconcile"
	"github.com/ivannguyendev/chatim/apps/core/internal/slot"
	"github.com/ivannguyendev/chatim/apps/core/internal/work"
	"github.com/ivannguyendev/chatim/pkg/envconfig"
)

const (
	defaultEffectDelay     = 5 * time.Second
	defaultEffectRoomCache = 65536
	workAckSlack           = 30 * time.Second
)

func (p *parser) components(c *Config) {
	pendingTTL := p.span("CID_PENDING_TTL", dedupe.DefaultPendingTTL)
	redisTimeout := p.span("REDIS_OP_TIMEOUT", dedupe.DefaultTimeout)
	redisCooldown := p.span("REDIS_COOLDOWN", dedupe.DefaultCooldown)
	subjectRoot := envconfig.String("EVT_SUBJECT_ROOT", "evt")
	tick := p.span("SLOT_TICK", time.Second)
	delay := p.span("RECONCILE_DELAY", defaultEffectDelay)

	c.Flush = flush.Config{
		Shards:        p.count("FLUSH_SHARDS", 4),
		Window:        p.span("FLUSH_WINDOW", 2*time.Millisecond),
		MaxBatch:      p.count("FLUSH_MAX_BATCH", 256),
		QueueSize:     p.count("FLUSH_QUEUE", 1024),
		InsertTimeout: p.span("FLUSH_INSERT_TIMEOUT", time.Second),
	}
	c.Actor = actor.Config{
		Mailbox:        p.count("ACTOR_MAILBOX", 1024),
		Idle:           p.span("ACTOR_IDLE", 5*time.Minute),
		MaxGroup:       p.count("ACTOR_MAX_GROUP", 64),
		MaxActors:      p.count("ACTOR_MAX", 100000),
		GroupDeadline:  c.RequestDeadline,
		ReservationTTL: pendingTTL,
	}
	c.Dedupe = dedupe.Config{
		CoreID:       c.CoreID,
		PendingTTL:   pendingTTL,
		CommittedTTL: p.span("CID_COMMITTED_TTL", dedupe.DefaultCommittedTTL),
		Timeout:      redisTimeout,
		Cooldown:     redisCooldown,
	}
	c.CIDBatch = dedupe.BatchConfig{
		Shards:  p.count("CID_BATCH_SHARDS", dedupe.DefaultBatchShards),
		MaxKeys: p.count("CID_BATCH_MAX_KEYS", dedupe.DefaultBatchMaxKeys),
		Queue:   p.count("CID_BATCH_QUEUE", dedupe.DefaultBatchQueue),
	}
	c.Publish = publish.Config{
		SubjectRoot: subjectRoot,
		Shards:      p.count("PUB_SHARDS", publish.DefaultShards),
		QueueSize:   p.count("PUB_QUEUE", publish.DefaultQueueSize),
		MaxPending:  p.count("PUB_MAX_PENDING", publish.DefaultMaxPending),
		AckTimeout:  p.span("PUB_ACK_TIMEOUT", publish.DefaultAckTimeout),
	}
	c.Stream = publish.StreamConfig{
		Name:        envconfig.String("EVT_STREAM", "CHATIM_EVT"),
		SubjectRoot: subjectRoot,
		LiveRoot:    envconfig.String("EVT_LIVE_ROOT", "live"),
		Replicas:    p.count("EVT_STREAM_REPLICAS", 1),
		MaxAge:      p.span("EVT_STREAM_MAX_AGE", publish.DefaultStreamMaxAge),
		Duplicates:  p.span("EVT_STREAM_DUPLICATES", publish.DefaultStreamDuplicates),
	}
	c.Work = work.StreamConfig{
		Name:        envconfig.String("WORK_STREAM", "CHATIM_WORK"),
		SubjectRoot: envconfig.String("WORK_SUBJECT_ROOT", "work"),
		Partitions:  p.count("WORK_PARTITIONS", work.DefaultPartitions),
		Replicas:    c.Stream.Replicas,
		MaxAge:      p.span("WORK_MAX_AGE", work.DefaultMaxAge),
		Duplicates:  p.span("WORK_DUPLICATES", work.DefaultDuplicates),
		AckWait:     delay + workAckSlack,
	}
	c.Slot = slot.Config{
		CoreID:       c.CoreID,
		Addr:         c.AdvertiseAddr,
		Tick:         tick,
		HeartbeatTTL: p.span("SLOT_HEARTBEAT_TTL", 5*time.Second),
		LeaseTTL:     p.span("SLOT_LEASE_TTL", 10*time.Second),
		HookTimeout:  p.span("SLOT_HOOK_TIMEOUT", tick/2),
	}
	c.ReconcileEnabled = p.flag("RECONCILE_ENABLED", true)
	c.EffectDelay = delay
	c.EffectRoomCache = p.count("RECONCILE_ROOM_CACHE", defaultEffectRoomCache)
	c.Reconcile = reconcile.Config{
		SubjectRoot:  c.Work.SubjectRoot,
		Partitions:   c.Work.Partitions,
		Window:       p.count("RECONCILE_WINDOW", reconcile.DefaultWindow),
		Batch:        p.count("RECONCILE_BATCH", reconcile.DefaultBatch),
		ConfirmEvery: p.span("RECONCILE_CONFIRM_EVERY", reconcile.DefaultConfirmEvery),
		Drain:        p.span("RECONCILE_DRAIN", reconcile.DefaultDrain),
		Poll:         tick,
	}
	c.AckMarks = eventmark.Config{
		TTL:      p.span("EVT_ACK_MARK_TTL", eventmark.DefaultTTL),
		Timeout:  redisTimeout,
		Cooldown: redisCooldown,
	}
}
