package recovery_test

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
	"github.com/redis/go-redis/v9"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
	"go.mongodb.org/mongo-driver/v2/mongo/readpref"

	"github.com/ivannguyendev/chatim/apps/core/internal/pbconv"
	"github.com/ivannguyendev/chatim/apps/core/internal/publish"
	"github.com/ivannguyendev/chatim/apps/core/internal/store/mongostore"
	"github.com/ivannguyendev/chatim/pkg/slotmap"
)

const (
	itMongoURIEnv  = "CHATIM_IT_MONGO_URI"
	itRedisAddrEnv = "CHATIM_IT_REDIS_ADDR"
	itRedisPWEnv   = "CHATIM_IT_REDIS_PASSWORD"
	itNATSURLEnv   = "CHATIM_IT_NATS_URL"
	itRedisDB      = 13
)

type itInfra struct {
	store  *mongostore.Store
	rdb    *redis.Client
	js     jetstream.JetStream
	stream publish.StreamConfig
}

func realInfra(t *testing.T) *itInfra {
	t.Helper()
	mongoURI, redisAddr, natsURL := os.Getenv(itMongoURIEnv), os.Getenv(itRedisAddrEnv), os.Getenv(itNATSURLEnv)
	if mongoURI == "" || redisAddr == "" || natsURL == "" {
		t.Skip("set CHATIM_IT_MONGO_URI, CHATIM_IT_REDIS_ADDR and CHATIM_IT_NATS_URL to run")
	}
	var b [6]byte
	_, _ = rand.Read(b[:])
	suffix := hex.EncodeToString(b[:])
	it := &itInfra{store: itStore(t, mongoURI, suffix), rdb: itRedis(t, redisAddr, os.Getenv(itRedisPWEnv))}
	it.openStream(t, natsURL, suffix)
	return it
}

func itStore(t *testing.T, uri, suffix string) *mongostore.Store {
	t.Helper()
	client, err := mongo.Connect(options.Client().ApplyURI(uri))
	if err != nil {
		t.Fatalf("mongo connect: %v", err)
	}
	t.Cleanup(func() { _ = client.Disconnect(context.Background()) })
	if err := client.Ping(t.Context(), readpref.Primary()); err != nil {
		t.Fatalf("mongo ping: %v", err)
	}
	db := client.Database("chatim_it_recovery_" + suffix)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if err := db.Drop(ctx); err != nil {
			t.Errorf("drop %s: %v", db.Name(), err)
		}
	})
	if err := mongostore.Bootstrap(t.Context(), db); err != nil {
		t.Fatalf("Bootstrap: %v", err)
	}
	return mongostore.New(db, mongostore.Options{})
}

func itRedis(t *testing.T, addr, password string) *redis.Client {
	t.Helper()
	rdb := redis.NewClient(&redis.Options{Addr: addr, DB: itRedisDB, Password: password, ContextTimeoutEnabled: true})
	t.Cleanup(func() { _ = rdb.Close() })
	if err := rdb.Ping(t.Context()).Err(); err != nil {
		t.Fatalf("redis ping %s: %v", addr, err)
	}
	return rdb
}

func (it *itInfra) openStream(t *testing.T, url, suffix string) {
	t.Helper()
	nc, err := nats.Connect(url)
	if err != nil {
		t.Fatalf("nats connect %s: %v", url, err)
	}
	t.Cleanup(nc.Close)
	js, err := jetstream.New(nc, publishSetup.JetStreamOptions()...)
	if err != nil {
		t.Fatalf("jetstream.New: %v", err)
	}
	cfg := publish.StreamConfig{Name: "IT_RECOVERY_" + strings.ToUpper(suffix), SubjectRoot: "itrevt" + suffix, LiveRoot: "itrlive" + suffix, Replicas: 1}
	if err := publish.EnsureStream(t.Context(), js, cfg); err != nil {
		t.Fatalf("EnsureStream: %v", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := js.DeleteStream(ctx, cfg.Name); err != nil {
			t.Errorf("delete stream %s: %v", cfg.Name, err)
		}
	})
	it.js, it.stream = js, cfg
}

func (it *itInfra) forgetRoomKeys(t *testing.T, room uint64) {
	t.Helper()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		keys := []string{publish.WatermarkKey(room)}
		iter := it.rdb.Scan(ctx, 0, "chatim:cid:"+pbconv.RoomID(room)+":*", 500).Iterator()
		for iter.Next(ctx) {
			keys = append(keys, iter.Val())
		}
		if err := iter.Err(); err != nil {
			t.Errorf("scan cid keys of room %d: %v", room, err)
		}
		if err := it.rdb.Del(ctx, keys...).Err(); err != nil {
			t.Errorf("delete keys of room %d: %v", room, err)
		}
		if err := it.rdb.ZRem(ctx, publish.ActiveKey(slotmap.Of(room)), pbconv.RoomID(room)).Err(); err != nil {
			t.Errorf("unmark room %d: %v", room, err)
		}
	})
}

func (it *itInfra) storedIDs(t *testing.T) []string {
	t.Helper()
	s, err := it.js.Stream(t.Context(), it.stream.Name)
	if err != nil {
		t.Fatalf("stream: %v", err)
	}
	info, err := s.Info(t.Context())
	if err != nil {
		t.Fatalf("stream info: %v", err)
	}
	var out []string
	for seq := info.State.FirstSeq; seq <= info.State.LastSeq && info.State.Msgs > 0; seq++ {
		m, err := s.GetMsg(t.Context(), seq)
		if err != nil {
			t.Fatalf("get stream message %d: %v", seq, err)
		}
		out = append(out, m.Header.Get(jetstream.MsgIDHeader))
	}
	return out
}
