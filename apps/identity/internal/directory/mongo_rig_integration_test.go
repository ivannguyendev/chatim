package directory_test

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"slices"
	"sync"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	"github.com/ivannguyendev/chatim/apps/identity/internal/directory"
	"github.com/ivannguyendev/chatim/apps/identity/internal/store"
	"github.com/ivannguyendev/chatim/apps/identity/internal/store/mongostore"
	"github.com/ivannguyendev/chatim/pkg/auth"
)

const (
	seedBatch     = 5000
	keySeparator  = "│"
	mirrorRecheck = time.Minute
	cleanupBudget = 30 * time.Second
)

type itest struct {
	opts  mongostore.Options
	store *mongostore.Store
	db    *mongo.Database
}

func itestDatabase(tb testing.TB) itest {
	tb.Helper()
	uri := os.Getenv("CHATIM_IT_MONGO_URI")
	if uri == "" {
		tb.Skip("set CHATIM_IT_MONGO_URI to run")
	}
	suffix := make([]byte, 6)
	_, _ = rand.Read(suffix)
	opts := mongostore.Options{URI: uri, Database: "identity_it_" + hex.EncodeToString(suffix), AppName: "chatim-identity-it", RevocationKeep: 16 * time.Minute,
		JITWriters: 1, JITWindow: time.Millisecond, JITMaxBatch: 16, JITQueue: 64, JITInsertTimeout: time.Second}
	client, err := mongo.Connect(options.Client().ApplyURI(uri))
	if err != nil {
		tb.Fatalf("connect: %v", err)
	}
	db := client.Database(opts.Database)
	tb.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), cleanupBudget)
		defer cancel()
		if err := db.Drop(ctx); err != nil {
			tb.Errorf("drop %s: %v", opts.Database, err)
		}
		_ = client.Disconnect(ctx)
	})
	it := itest{opts: opts, store: openStore(tb, opts), db: db}
	if _, err := it.store.Migrate(context.Background(), true); err != nil {
		tb.Fatalf("Migrate: %v", err)
	}
	return it
}

func openStore(tb testing.TB, opts mongostore.Options) *mongostore.Store {
	tb.Helper()
	s, err := mongostore.Open(opts)
	if err != nil {
		tb.Fatalf("Open: %v", err)
	}
	tb.Cleanup(s.Close)
	return s
}

func userID(n int) string { return fmt.Sprintf("u%06d", n) }

func seedUsers(tb testing.TB, it itest, count int) {
	tb.Helper()
	now := time.Now()
	users := it.db.Collection("users")
	for first := 1; first <= count; first += seedBatch {
		batch := make([]any, 0, seedBatch)
		for n := first; n < first+seedBatch && n <= count; n++ {
			batch = append(batch, bson.D{
				{Key: "_id", Value: "shop" + keySeparator + userID(n)}, {Key: "tenant_id", Value: "shop"}, {Key: "id", Value: userID(n)},
				{Key: "display_name", Value: fmt.Sprintf("User %d", n)}, {Key: "avatar_url", Value: ""}, {Key: "state", Value: store.StateActive},
				{Key: "groups", Value: bson.A{}}, {Key: "ver", Value: int64(1)},
				{Key: "created_at", Value: now}, {Key: "updated_at", Value: now}, {Key: "updated_by", Value: "test"},
				{Key: "realm", Value: "staff"}, {Key: "sub", Value: userID(n) + "@shop"},
			})
		}
		if _, err := users.InsertMany(context.Background(), batch); err != nil {
			tb.Fatalf("seed users from %d: %v", first, err)
		}
	}
}

type runningMirror struct {
	cache    *directory.Cache
	stats    *directory.Stats
	loaded   chan struct{}
	trust    chan struct{}
	cancel   context.CancelFunc
	done     chan error
	stopOnce sync.Once
	err      error
}

func startMirror(tb testing.TB, src directory.Source, slots int) *runningMirror {
	tb.Helper()
	list, err := auth.NewNodeRevocationList(0)
	if err != nil {
		tb.Fatalf("NewList: %v", err)
	}
	cache := directory.NewCache()
	r := &runningMirror{cache: cache, stats: directory.NewStats(cache), loaded: make(chan struct{}, 1), trust: make(chan struct{}, 1), done: make(chan error, 1)}
	m := &directory.Mirror{
		Source: src, Cache: cache, List: list, Stats: r.stats, Slots: slots, Recheck: mirrorRecheck,
		OnLoaded: func() { notify(r.loaded) },
		OnTrust:  func() { notify(r.trust) },
	}
	ctx, cancel := context.WithCancel(context.Background())
	r.cancel = cancel
	go func() { r.done <- m.Run(ctx) }()
	tb.Cleanup(func() {
		if err := r.stop(); err != nil {
			tb.Errorf("Mirror.Run: %v", err)
		}
	})
	return r
}

func notify(ch chan struct{}) {
	select {
	case ch <- struct{}{}:
	default:
	}
}

func (r *runningMirror) stop() error {
	r.stopOnce.Do(func() {
		r.cancel()
		r.err = <-r.done
	})
	return r.err
}

func await(tb testing.TB, ch <-chan struct{}, limit time.Duration, what string) {
	tb.Helper()
	select {
	case <-ch:
	case <-time.After(limit):
		tb.Fatalf("%s did not happen within %v", what, limit)
	}
}

type watchedSource struct {
	*mongostore.Store
	applied func(id string)
}

func (w watchedSource) OpenStream(ctx context.Context, from store.StreamToken) (store.ChangeStream, error) {
	cs, err := w.Store.OpenStream(ctx, from)
	if err != nil {
		return nil, err
	}
	return &watchedStream{ChangeStream: cs, applied: w.applied}, nil
}

type watchedStream struct {
	store.ChangeStream
	applied func(id string)
	last    string
}

func (s *watchedStream) Next(ctx context.Context) (store.StreamEvent, bool, error) {
	if s.last != "" {
		s.applied(s.last)
		s.last = ""
	}
	ev, has, err := s.ChangeStream.Next(ctx)
	if has && slices.Contains(ev.Kinds, store.EventUser) {
		s.last = ev.User.ID
	}
	return ev, has, err
}

func onceFor(id string) (func(string), chan struct{}) {
	seen := make(chan struct{})
	var once sync.Once
	return func(got string) {
		if got == id {
			once.Do(func() { close(seen) })
		}
	}, seen
}
