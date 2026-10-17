package config_test

import (
	"os"
	"reflect"
	"testing"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/change/mutate"
	"github.com/ivannguyendev/chatim/apps/core/internal/config"
	"github.com/ivannguyendev/chatim/apps/core/internal/event/projection"
	"github.com/ivannguyendev/chatim/apps/core/internal/event/publish"
	"github.com/ivannguyendev/chatim/apps/core/internal/event/stateclear"
	"github.com/ivannguyendev/chatim/apps/core/internal/event/sweep"
	"github.com/ivannguyendev/chatim/apps/core/internal/event/timer"
	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/platform/slot"
	"github.com/ivannguyendev/chatim/apps/core/internal/send/actor"
	"github.com/ivannguyendev/chatim/apps/core/internal/send/dedupe"
	"github.com/ivannguyendev/chatim/apps/core/internal/send/flush"
	"github.com/ivannguyendev/chatim/apps/mention/mentionapp"
)

const day = 24 * time.Hour

func TestLoadDefaults(t *testing.T) {
	setEnv(t, nil)
	host, err := os.Hostname()
	if err != nil {
		t.Fatalf("os.Hostname: %v", err)
	}
	got, err := config.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	want := config.Config{
		CoreID: host, GRPCAddr: ":9000", AdvertiseAddr: host + ":9000", AdminAddr: ":9090",
		MongoURI: testMongoURI, MongoDB: "chatim", MongoAuthSource: "admin", RedisAddr: "chatim-redis:6379", RedisDB: 0,
		RedisDedupeAddr: "chatim-redis-dedupe:6379",
		NATSURL:         "nats://chatim-nats:4222", ConnectTimeout: 10 * time.Second, RequestDeadline: 3 * time.Second,
		SlowRPC: 500 * time.Millisecond, QueueWait: 25 * time.Millisecond, MaxInflight: 2048, DrainDelay: 2 * time.Second, GRPCShutdown: 5 * time.Second,
		PublisherDrain: 5 * time.Second, ShutdownBudget: 30 * time.Second,
		Flush: flush.Config{Shards: 4, Window: 2 * time.Millisecond, MaxBatch: 256, QueueSize: 1024, InsertTimeout: time.Second},
		Actor: actor.Config{
			Mailbox: 1024, Idle: 5 * time.Minute, MaxGroup: 64, MaxActors: 100000,
			GroupDeadline: 3 * time.Second, ReservationTTL: 10 * time.Second, MentionTargets: 50,
		},
		Dedupe:   dedupe.Config{CoreID: host, PendingTTL: 10 * time.Second, CommittedTTL: 15 * time.Minute, Timeout: 100 * time.Millisecond, Cooldown: time.Second},
		CIDBatch: dedupe.BatchConfig{Shards: 4, MaxKeys: 256, Queue: 4096},
		Publish: publish.Config{
			SubjectRoot: "evt", Shards: 4, QueueSize: 1024, MaxPending: 256, AckTimeout: 2 * time.Second,
		},
		Stream: publish.StreamConfig{Name: "CHATIM_EVT", SubjectRoot: "evt", LiveRoot: "live", Replicas: 1, MaxAge: 7 * day, Duplicates: 5 * time.Minute},
		Timer:  timer.StreamConfig{Name: "CHATIM_TIMER", SubjectRoot: "timer", Replicas: 1, MaxAge: 2 * time.Hour, Duplicates: 2 * time.Minute, AckWait: 30 * time.Second},
		Slot: slot.Config{
			CoreID: host, Addr: host + ":9000", Tick: time.Second, HeartbeatTTL: 5 * time.Second,
			LeaseTTL: 10 * time.Second, HookTimeout: 500 * time.Millisecond,
		},
		StateClear: stateclear.Config{Flush: 2 * time.Second, Batch: 16384, Queue: 65536, Timeout: 5 * time.Second},
		Sweep: sweep.Config{
			SubjectRoot: "evt", Interval: 2 * time.Second, MinAge: 15 * time.Second, Lookback: time.Minute,
			Batch: 500, Rate: 5000, Tick: time.Second, AckTimeout: 2 * time.Second,
		},
		Projection: projection.Config{
			Stream: "CHATIM_EVT", SubjectRoot: "evt", FetchBatch: 8192, FetchWait: 3 * time.Second,
		},
		Limits:                 mutate.Limits{Emojis: mutate.DefaultEmojis, PinLimit: 50, MemberBatch: 500, MentionTargets: 50},
		MemberCountCheckDelay:  5 * time.Second,
		MessageCountCheckDelay: 5 * time.Second,
		RoomList:               config.RoomListConfig{JoinMax: 1500, TailCacheBytes: 256 << 20, InfoCacheEntries: 100000},
		Mention:                defaultMention(),
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Load() =\n%#v\nwant\n%#v", got, want)
	}
}

func defaultMention() mentionapp.Config {
	m := mentionapp.DefaultConfig()
	m.EventStream, m.EventSubjectRoot, m.LiveRoot, m.Replicas = "CHATIM_EVT", "evt", "live", 1
	return m
}

func TestLoadOverrides(t *testing.T) {
	setEnv(t, overrides)
	got, err := config.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	want := config.Config{
		CoreID: "core-a", GRPCAddr: ":7000", AdvertiseAddr: "10.0.0.5:7000", AdminAddr: "127.0.0.1:7090",
		MongoURI: "mongodb://m1,m2/?replicaSet=rs1", MongoDB: "chatim_it",
		MongoUser: "core-user", MongoPassword: "mongo-pw", MongoAuthSource: "chatim", RedisAddr: "redis:6380", RedisDB: 3,
		RedisPassword: "state-pw", RedisDedupeAddr: "dedupe:6381", RedisDedupeDB: 2, RedisDedupePassword: "dedupe-pw",
		NATSURL: "nats://n1:4222", ConnectTimeout: 4 * time.Second, RequestDeadline: 2 * time.Second,
		SlowRPC: 750 * time.Millisecond, QueueWait: 10 * time.Millisecond, MaxInflight: 100, DrainDelay: time.Second, GRPCShutdown: 4 * time.Second,
		PublisherDrain: 3 * time.Second, ShutdownBudget: 22 * time.Second,
		Flush: flush.Config{Shards: 8, Window: 5 * time.Millisecond, MaxBatch: 512, QueueSize: 256, InsertTimeout: 500 * time.Millisecond},
		Actor: actor.Config{
			Mailbox: 64, Idle: time.Minute, MaxGroup: 32, MaxActors: 5000,
			GroupDeadline: 2 * time.Second, ReservationTTL: 8 * time.Second, MentionTargets: 20,
		},
		Dedupe:   dedupe.Config{CoreID: "core-a", PendingTTL: 8 * time.Second, CommittedTTL: 30 * time.Minute, Timeout: 50 * time.Millisecond, Cooldown: 2 * time.Second},
		CIDBatch: dedupe.BatchConfig{Shards: 2, MaxKeys: 64, Queue: 512},
		Publish: publish.Config{
			SubjectRoot: "evt_it", Shards: 2, QueueSize: 512, MaxPending: 128, AckTimeout: time.Second,
		},
		Stream: publish.StreamConfig{Name: "CHATIM_EVT_IT", SubjectRoot: "evt_it", LiveRoot: "live_it", Replicas: 3, MaxAge: 2 * day, Duplicates: 5 * time.Minute},
		Timer:  timer.StreamConfig{Name: "CHATIM_TIMER_IT", SubjectRoot: "timer_it", Replicas: 3, MaxAge: 2 * time.Hour, Duplicates: 2 * time.Minute, AckWait: 30 * time.Second},
		Slot: slot.Config{
			CoreID: "core-a", Addr: "10.0.0.5:7000", Tick: 500 * time.Millisecond, HeartbeatTTL: 3 * time.Second,
			LeaseTTL: 6 * time.Second, HookTimeout: 200 * time.Millisecond,
		},
		StateClear: stateclear.Config{Flush: 50 * time.Millisecond, Batch: 200, Queue: 4096, Timeout: 2 * time.Second},
		Sweep: sweep.Config{
			SubjectRoot: "evt_it", Interval: 3 * time.Second, MinAge: 4 * time.Second, Lookback: 20 * time.Second,
			Batch: 250, Rate: 2000, Tick: 500 * time.Millisecond, AckTimeout: time.Second,
		},
		Projection: projection.Config{
			Stream: "CHATIM_EVT_IT", SubjectRoot: "evt_it", FetchBatch: 128, FetchWait: 250 * time.Millisecond,
		},
		LockedMessageKinds:     []domain.Kind{domain.KindText},
		Limits:                 mutate.Limits{Emojis: []string{"🎉", "👍"}, PinLimit: 10, MemberBatch: 20, MentionTargets: 20},
		MemberCountCheckDelay:  4 * time.Second,
		MessageCountCheckDelay: 6 * time.Second,
		RoomList:               config.RoomListConfig{JoinMax: 300, TailCacheBytes: 2 << 20, InfoCacheEntries: 5000},
		Mention: mentionapp.Config{
			Enabled: true, EventStream: "CHATIM_EVT_IT", EventSubjectRoot: "evt_it", LiveRoot: "live_it", Replicas: 3,
			FetchBatch: 64, FetchWait: 200 * time.Millisecond, Workers: 4, MaxAckPending: 1024, NotifyMaxAge: 2 * time.Minute,
			NotifyStream: "CHATIM_NOTIFY_IT", NotifySubjectRoot: "notify_it", NotifyMaxStreamAge: 12 * time.Hour, NotifyDuplicates: 3 * time.Minute,
			HideReadTenants: []string{"acme", "globex"}, RoomGroupsSyncMax: 200, RoomGroupsTTL: 30 * time.Second, RoomGroupsRebuild: 5 * time.Minute,
		},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Load() =\n%#v\nwant\n%#v", got, want)
	}
}

func TestOverridesCoverEveryKey(t *testing.T) {
	for _, k := range envKeys {
		if _, ok := overrides[k]; !ok {
			t.Errorf("overrides miss %s", k)
		}
	}
	if len(overrides) != len(envKeys) {
		t.Errorf("overrides has %d keys, envKeys %d", len(overrides), len(envKeys))
	}
}

func TestLoadAdvertiseAddrFollowsCoreIDAndGRPCPort(t *testing.T) {
	tests := []struct {
		name string
		env  map[string]string
		want string
	}{
		{"default port", map[string]string{"CORE_ID": "core-b"}, "core-b:9000"},
		{"port of a bare grpc addr", map[string]string{"CORE_ID": "core-b", "CORE_GRPC_ADDR": ":7100"}, "core-b:7100"},
		{"port of a bound grpc addr", map[string]string{"CORE_ID": "core-b", "CORE_GRPC_ADDR": "0.0.0.0:7200"}, "core-b:7200"},
		{"explicit advertise addr wins", map[string]string{"CORE_ID": "core-b", "CORE_ADVERTISE_ADDR": "10.1.1.1:7300"}, "10.1.1.1:7300"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			setEnv(t, tt.env)
			got, err := config.Load()
			if err != nil {
				t.Fatalf("Load: %v", err)
			}
			if got.AdvertiseAddr != tt.want || got.Slot.Addr != tt.want {
				t.Errorf("AdvertiseAddr = %q, Slot.Addr = %q, want %q", got.AdvertiseAddr, got.Slot.Addr, tt.want)
			}
		})
	}
}

func TestLoadDerivesHookTimeoutFromTick(t *testing.T) {
	setEnv(t, map[string]string{"SLOT_TICK": "600ms"})
	got, err := config.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got.Slot.HookTimeout != 300*time.Millisecond {
		t.Errorf("Slot.HookTimeout = %v, want half of SLOT_TICK", got.Slot.HookTimeout)
	}
}

func TestAdminAddrReadsOnlyTheAdminKey(t *testing.T) {
	for _, k := range envKeys {
		t.Setenv(k, "")
	}
	if got := config.AdminAddr(); got != ":9090" {
		t.Errorf("AdminAddr() = %q, want :9090", got)
	}
	t.Setenv("CORE_ADMIN_ADDR", "127.0.0.1:7091")
	if got := config.AdminAddr(); got != "127.0.0.1:7091" {
		t.Errorf("AdminAddr() = %q, want 127.0.0.1:7091", got)
	}
}
