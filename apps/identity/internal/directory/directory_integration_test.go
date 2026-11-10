package directory_test

import (
	"context"
	"fmt"
	mrand "math/rand/v2"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"

	"github.com/ivannguyendev/chatim/apps/identity/internal/store"
	"github.com/ivannguyendev/chatim/apps/identity/internal/store/mongostore"
)

const (
	seededUsers = 20000
	loadLimit   = time.Minute
	trustLimit  = 2 * time.Second
	markerID    = "zz-marker"
)

func renameUntilStopped(t *testing.T, s *mongostore.Store, stop <-chan struct{}, writes *atomic.Int64, warmed chan<- struct{}) {
	ctx := context.Background()
	for n := 0; ; n++ {
		select {
		case <-stop:
			return
		default:
		}
		name := fmt.Sprintf("Renamed %d", n)
		err := s.SetProfile(ctx, "shop", userID(1+mrand.IntN(seededUsers)), store.ProfileUpdate{DisplayName: &name}, time.Now(), "test")
		if err != nil {
			t.Errorf("rename: %v", err)
			return
		}
		if writes.Add(1) == 20 {
			close(warmed)
		}
	}
}

type dbUser struct {
	ID          string `bson:"id"`
	DisplayName string `bson:"display_name"`
	Ver         int64  `bson:"ver"`
}

func TestWritesDuringLoadAreInTheCacheAfterReady(t *testing.T) {
	it := itestDatabase(t)
	seedUsers(t, it, seededUsers)
	ctx := context.Background()
	var writes atomic.Int64
	stop, writerDone, warmed := make(chan struct{}), make(chan struct{}), make(chan struct{})
	var stopOnce sync.Once
	stopWriter := func() {
		stopOnce.Do(func() { close(stop) })
		<-writerDone
	}
	go func() {
		defer close(writerDone)
		renameUntilStopped(t, it.store, stop, &writes, warmed)
	}()
	defer stopWriter()
	select {
	case <-warmed:
	case <-writerDone:
		t.Fatal("the writer stopped before it warmed up")
	}
	applied, markerSeen := onceFor(markerID)
	before := writes.Load()
	m := startMirror(t, watchedSource{Store: it.store, applied: applied}, 4)
	await(t, m.loaded, loadLimit, "directory load")
	during := writes.Load() - before
	stopWriter()
	if !m.cache.Ready() || during == 0 {
		t.Fatalf("ready=%v writes during load=%d; want ready with writes during the load", m.cache.Ready(), during)
	}
	marker := store.NewUser{TenantID: "shop", Realm: "test", Sub: markerID, CandidateID: markerID, DisplayName: "Marker", At: time.Now(), By: "test"}
	if _, err := it.store.LinkUser(ctx, marker); err != nil {
		t.Fatalf("write marker: %v", err)
	}
	await(t, markerSeen, loadLimit, "marker user applied")
	cursor, err := it.db.Collection("users").Find(ctx, bson.D{{Key: "tenant_id", Value: "shop"}})
	if err != nil {
		t.Fatalf("read users: %v", err)
	}
	var want []dbUser
	if err := cursor.All(ctx, &want); err != nil {
		t.Fatalf("decode users: %v", err)
	}
	if len(want) != seededUsers+1 || m.cache.Len() != seededUsers+1 {
		t.Fatalf("db users=%d cache=%d; want %d each", len(want), m.cache.Len(), seededUsers+1)
	}
	renamed, mismatched := 0, 0
	for _, d := range want {
		got := m.cache.Lookup("shop", []string{d.ID})
		if len(got) != 1 || got[0].DisplayName != d.DisplayName || got[0].Ver != d.Ver {
			mismatched++
			if mismatched <= 5 {
				t.Errorf("%s: cache %+v; db name %q ver %d", d.ID, got, d.DisplayName, d.Ver)
			}
		}
		if d.Ver > 1 {
			renamed++
		}
	}
	if mismatched > 0 || renamed == 0 {
		t.Fatalf("mismatched=%d renamed=%d; want 0 mismatched and some renamed", mismatched, renamed)
	}
	t.Logf("writes during load=%d renamed users=%d load=%.3fs", during, renamed, m.stats.LastLoadSeconds())
}

func TestTrustedKeyAddWakesTrustAtOnce(t *testing.T) {
	it := itestDatabase(t)
	ctx := context.Background()
	now := time.Now()
	if err := it.store.CreateTenant(ctx, store.Tenant{ID: "shop", Name: "Shop", State: store.StateActive, CreatedAt: now, UpdatedAt: now, UpdatedBy: "test"}); err != nil {
		t.Fatalf("CreateTenant: %v", err)
	}
	if err := it.store.CreateTrustedIssuer(ctx, "https://shop.example", "shop", "", now, "test"); err != nil {
		t.Fatalf("CreateTrustedIssuer: %v", err)
	}
	m := startMirror(t, it.store, 1)
	await(t, m.loaded, loadLimit, "directory load")
	select {
	case <-m.trust:
		t.Fatal("trust woken by writes made before the load")
	default:
	}
	added := time.Now()
	key := store.TrustedKey{Issuer: "https://shop.example", KeyID: "shop-1", Algorithm: "EdDSA", PublicKey: []byte(`{"kty":"OKP","crv":"Ed25519","x":"11qYAYKxCrfVS_7TyWQHOg7hcvPapiMlrwIaaPcHURo"}`)}
	if err := it.store.AddTrustedKey(ctx, key, added, "test"); err != nil {
		t.Fatalf("AddTrustedKey: %v", err)
	}
	await(t, m.trust, trustLimit, "OnTrust after the key add")
	t.Logf("OnTrust %v after the key add (recheck %v)", time.Since(added), mirrorRecheck)
}

func TestDropDatabaseReloads(t *testing.T) {
	it := itestDatabase(t)
	seedUsers(t, it, 3)
	m := startMirror(t, it.store, 1)
	await(t, m.loaded, loadLimit, "first directory load")
	if m.cache.Len() != 3 || m.stats.StreamReloads() != 0 {
		t.Fatalf("cache=%d reloads=%d after the first load; want 3 and 0", m.cache.Len(), m.stats.StreamReloads())
	}
	if err := it.db.Drop(context.Background()); err != nil {
		t.Fatalf("drop database: %v", err)
	}
	await(t, m.loaded, loadLimit, "reload after dropDatabase")
	if got := m.stats.StreamReloads(); got != 1 || !m.cache.Ready() || m.cache.Len() != 0 {
		t.Fatalf("reloads=%d ready=%v cache=%d; want 1 reload, ready, empty cache", got, m.cache.Ready(), m.cache.Len())
	}
}
