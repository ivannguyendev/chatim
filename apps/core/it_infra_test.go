package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"maps"
	"net"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
	"github.com/redis/go-redis/v9"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
	"go.mongodb.org/mongo-driver/v2/mongo/readpref"

	"github.com/ivannguyendev/chatim/apps/core/internal/config"
)

const (
	itMongoURIEnv  = "CHATIM_IT_MONGO_URI"
	itRedisAddrEnv = "CHATIM_IT_REDIS_ADDR"
	itNATSURLEnv   = "CHATIM_IT_NATS_URL"
	itRedisDB      = 12
	itCleanupLimit = 30 * time.Second
)

type itInfra struct {
	mongoURI, redisAddr, natsURL string
	suffix                       string
	mongo                        *mongo.Client
	rdb                          *redis.Client
	js                           jetstream.JetStream
}

func realInfra(t *testing.T) *itInfra {
	t.Helper()
	it := &itInfra{mongoURI: os.Getenv(itMongoURIEnv), redisAddr: os.Getenv(itRedisAddrEnv), natsURL: os.Getenv(itNATSURLEnv)}
	if it.mongoURI == "" || it.redisAddr == "" || it.natsURL == "" {
		t.Skip("set CHATIM_IT_MONGO_URI, CHATIM_IT_REDIS_ADDR and CHATIM_IT_NATS_URL to run")
	}
	var b [6]byte
	_, _ = rand.Read(b[:])
	it.suffix = hex.EncodeToString(b[:])

	client, err := mongo.Connect(options.Client().ApplyURI(it.mongoURI))
	if err != nil {
		t.Fatalf("mongo connect: %v", err)
	}
	t.Cleanup(func() { _ = client.Disconnect(context.Background()) })
	if err := client.Ping(t.Context(), readpref.Primary()); err != nil {
		t.Fatalf("mongo ping: %v", err)
	}
	it.mongo = client

	it.rdb = redis.NewClient(&redis.Options{Addr: it.redisAddr, DB: itRedisDB, ContextTimeoutEnabled: true})
	t.Cleanup(func() { _ = it.rdb.Close() })
	if err := it.rdb.Ping(t.Context()).Err(); err != nil {
		t.Fatalf("redis ping: %v", err)
	}

	nc, err := nats.Connect(it.natsURL)
	if err != nil {
		t.Fatalf("nats connect: %v", err)
	}
	t.Cleanup(nc.Close)
	if it.js, err = jetstream.New(nc); err != nil {
		t.Fatalf("jetstream.New: %v", err)
	}
	return it
}

func (it *itInfra) coreConfig(t *testing.T, env map[string]string) config.Config {
	t.Helper()
	base := map[string]string{
		"CORE_ID":          "it-core-" + it.suffix,
		"CORE_GRPC_ADDR":   freeAddr(t),
		"CORE_ADMIN_ADDR":  freeAddr(t),
		"MONGO_URI":        it.mongoURI,
		"MONGO_DB":         "chatim_it_core_" + it.suffix,
		"REDIS_ADDR":       it.redisAddr,
		"REDIS_DB":         strconv.Itoa(itRedisDB),
		"NATS_URL":         it.natsURL,
		"EVT_STREAM":       "IT_CORE_" + strings.ToUpper(it.suffix),
		"EVT_SUBJECT_ROOT": "itcevt" + it.suffix,
		"EVT_LIVE_ROOT":    "itclive" + it.suffix,
	}
	maps.Copy(base, env)
	for k, v := range base {
		t.Setenv(k, v)
	}
	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	t.Cleanup(func() { it.forget(t, cfg) })
	return cfg
}

func (it *itInfra) forget(t *testing.T, cfg config.Config) {
	ctx, cancel := context.WithTimeout(context.Background(), itCleanupLimit)
	defer cancel()
	if err := it.mongo.Database(cfg.MongoDB).Drop(ctx); err != nil {
		t.Errorf("drop %s: %v", cfg.MongoDB, err)
	}
	if err := it.js.DeleteStream(ctx, cfg.Stream.Name); err != nil && !errors.Is(err, jetstream.ErrStreamNotFound) {
		t.Errorf("delete stream %s: %v", cfg.Stream.Name, err)
	}
	iter := it.rdb.Scan(ctx, 0, "chatim:*", 1000).Iterator()
	var keys []string
	for iter.Next(ctx) {
		keys = append(keys, iter.Val())
	}
	if err := iter.Err(); err != nil {
		t.Errorf("scan redis db %d: %v", itRedisDB, err)
	}
	for len(keys) > 0 {
		n := min(len(keys), 1000)
		if err := it.rdb.Del(ctx, keys[:n]...).Err(); err != nil {
			t.Errorf("delete redis keys: %v", err)
		}
		keys = keys[n:]
	}
}

func (it *itInfra) streamIDs(t *testing.T, name string) map[string]int {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), itCleanupLimit)
	defer cancel()
	s, err := it.js.Stream(ctx, name)
	if err != nil {
		t.Fatalf("stream %s: %v", name, err)
	}
	info, err := s.Info(ctx)
	if err != nil {
		t.Fatalf("stream info: %v", err)
	}
	ids := make(map[string]int, info.State.Msgs)
	for seq := info.State.FirstSeq; info.State.Msgs > 0 && seq <= info.State.LastSeq; seq++ {
		m, err := s.GetMsg(ctx, seq)
		if errors.Is(err, jetstream.ErrMsgNotFound) {
			continue
		}
		if err != nil {
			t.Fatalf("get stream message %d: %v", seq, err)
		}
		ids[m.Header.Get(jetstream.MsgIDHeader)]++
	}
	return ids
}

func freeAddr(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer l.Close()
	return l.Addr().String()
}
