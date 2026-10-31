package api_test

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/ivannguyendev/chatim/apps/iam/internal/api"
	"github.com/ivannguyendev/chatim/apps/iam/internal/directory"
	"github.com/ivannguyendev/chatim/apps/iam/internal/store"
	"github.com/ivannguyendev/chatim/apps/iam/internal/store/pgstore"
	"github.com/ivannguyendev/chatim/pkg/iamsync"
)

func feedDatabase(t *testing.T) (*pgstore.Store, string) {
	t.Helper()
	base := os.Getenv("CHATIM_IT_PG_URI")
	if base == "" {
		t.Skip("set CHATIM_IT_PG_URI to run")
	}
	ctx := context.Background()
	admin, err := pgx.Connect(ctx, base)
	if err != nil {
		t.Fatalf("connect admin: %v", err)
	}
	suffix := make([]byte, 6)
	_, _ = rand.Read(suffix)
	name := "iam_it_" + hex.EncodeToString(suffix)
	if _, err := admin.Exec(ctx, "CREATE DATABASE "+pgx.Identifier{name}.Sanitize()); err != nil {
		t.Fatalf("create database: %v", err)
	}
	t.Cleanup(func() {
		_, _ = admin.Exec(context.Background(), "DROP DATABASE "+pgx.Identifier{name}.Sanitize()+" WITH (FORCE)")
		_ = admin.Close(context.Background())
	})
	u, err := url.Parse(base)
	if err != nil {
		t.Fatalf("parse uri: %v", err)
	}
	u.Path = "/" + name
	s, err := pgstore.Open(ctx, u.String(), pgstore.Options{MaxConns: 8, RevocationKeep: 16 * time.Minute})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(s.Close)
	if _, err := s.Migrate(ctx, true); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	raw, err := pgx.Connect(ctx, u.String())
	if err != nil {
		t.Fatalf("connect raw: %v", err)
	}
	defer func() { _ = raw.Close(context.Background()) }()
	_, err = raw.Exec(ctx, `INSERT INTO tenants (id, name, state, ver, created_at, updated_at, updated_by)
		VALUES ('shop', 'shop', 1, 1, now(), now(), 'test');
		INSERT INTO accounts (tenant_id, id, username, display_name, state, password_hash,
		password_must_change, password_changed_at, ver, created_at, updated_at, updated_by)
		VALUES ('shop', 'lan', 'lan', 'Lan', 1, 'x', false, now(), 1, now(), now(), 'test')`)
	if err != nil {
		t.Fatalf("seed: %v", err)
	}
	return s, u.String()
}

type firstCallFeed struct {
	api.DirectoryFeed
	once   sync.Once
	called chan struct{}
}

func (f *firstCallFeed) Changes(ctx context.Context, after store.Position, limit int) (store.ChangePage, error) {
	page, err := f.DirectoryFeed.Changes(ctx, after, limit)
	f.once.Do(func() { close(f.called) })
	return page, err
}

func startFollower(t *testing.T, s *pgstore.Store, uri string) *directory.Follower {
	t.Helper()
	list, err := iamsync.NewList(0)
	if err != nil {
		t.Fatalf("NewList: %v", err)
	}
	cache := directory.NewCache()
	listening := make(chan struct{})
	var once sync.Once
	follower := &directory.Follower{
		Listen: func(ctx context.Context) (store.Listener, error) {
			l, err := pgstore.OpenListener(ctx, uri)
			if err == nil {
				once.Do(func() { close(listening) })
			}
			return l, err
		},
		Reader: s, Revocations: s, Cache: cache, List: list, Stats: directory.NewStats(cache),
	}
	ctx, cancel := context.WithCancel(context.Background())
	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		_ = follower.Run(ctx)
	}()
	t.Cleanup(func() {
		cancel()
		<-stopped
	})
	select {
	case <-listening:
	case <-time.After(10 * time.Second):
		t.Fatal("the follower never listened")
	}
	return follower
}

func pollFeed(ctx context.Context, base, query string) (feedBody, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+feedPath+query, nil)
	if err != nil {
		return feedBody{}, err
	}
	req.Header.Set("Authorization", "Bearer "+nodeSecret)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return feedBody{}, err
	}
	defer func() { _ = resp.Body.Close() }()
	raw, _ := io.ReadAll(resp.Body)
	var body feedBody
	if err := json.Unmarshal(raw, &body); err != nil || resp.StatusCode != http.StatusOK {
		return feedBody{}, fmt.Errorf("status %d body %s: %w", resp.StatusCode, raw, err)
	}
	return body, nil
}

type polled struct {
	body feedBody
	err  error
}

func TestFeedLongPollIsWokenByTheFollowerSignal(t *testing.T) {
	s, uri := feedDatabase(t)
	follower := startFollower(t, s, uri)
	feed := &firstCallFeed{DirectoryFeed: s, called: make(chan struct{})}
	h, err := api.NewInternal(api.InternalOptions{Revocations: s, Secret: nodeSecret, DirectoryFeed: feed, DirectorySignal: follower, FeedWaitMax: 30 * time.Second, FeedRecheck: 30 * time.Second})
	if err != nil {
		t.Fatalf("NewInternal: %v", err)
	}
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)

	first, err := pollFeed(t.Context(), srv.URL, "?wait=10")
	if err != nil || len(first.Changes) != 1 || first.Next == "" {
		t.Fatalf("first page = %+v (%v); want the seeded account", first, err)
	}
	answered := make(chan polled, 1)
	go func() {
		body, err := pollFeed(t.Context(), srv.URL, "?wait=25&after="+first.Next)
		answered <- polled{body: body, err: err}
	}()
	<-feed.called

	name := "Lan Nguyen"
	audit := store.AuditEvent{TenantID: "shop", At: time.Now(), Actor: "test", Action: "profile_update", Detail: map[string]string{"fields": "display_name"}}
	if err := s.SetProfile(context.Background(), "shop", "lan", store.ProfileUpdate{DisplayName: &name}, audit); err != nil {
		t.Fatalf("SetProfile: %v", err)
	}
	committed := time.Now()
	select {
	case got := <-answered:
		elapsed, body := time.Since(committed), got.body
		if got.err != nil || len(body.Changes) != 1 || body.Changes[0]["id"] != "lan" || body.Changes[0]["display_name"] != name || body.Next == first.Next {
			t.Fatalf("woken page = %+v (%v); want lan renamed with a new cursor", body, got.err)
		}
		t.Logf("long poll answered %v after the commit", elapsed)
	case <-time.After(20 * time.Second):
		t.Fatal("no answer within 20s of the commit, before the 25s wait and the 30s handler recheck")
	}
}
