package directory_test

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	mrand "math/rand/v2"
	"net/url"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/ivannguyendev/chatim/apps/iam/internal/directory"
	"github.com/ivannguyendev/chatim/apps/iam/internal/store/pgstore"
	"github.com/ivannguyendev/chatim/pkg/iamsync"
)

const seededAccounts = 20000

func itestDatabase(t *testing.T) (*pgstore.Store, *pgx.Conn, string) {
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
	uri := u.String()
	s, err := pgstore.Open(ctx, uri, pgstore.Options{MaxConns: 8, RevocationKeep: 16 * time.Minute})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(s.Close)
	if _, err := s.Migrate(ctx, true); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	raw, err := pgx.Connect(ctx, uri)
	if err != nil {
		t.Fatalf("connect raw: %v", err)
	}
	t.Cleanup(func() { _ = raw.Close(context.Background()) })
	return s, raw, uri
}

func seedDirectory(t *testing.T, raw *pgx.Conn) {
	t.Helper()
	ctx := context.Background()
	_, err := raw.Exec(ctx, `INSERT INTO tenants (id, name, state, ver, created_at, updated_at, updated_by)
		VALUES ('shop', 'shop', 1, 1, now(), now(), 'test')`)
	if err != nil {
		t.Fatalf("seed tenant: %v", err)
	}
	_, err = raw.Exec(ctx, `INSERT INTO accounts (tenant_id, id, username, display_name, state, password_hash,
		password_must_change, password_changed_at, ver, created_at, updated_at, updated_by)
		SELECT 'shop', 'u' || lpad(g::text, 6, '0'), 'user' || g, 'User ' || g, 1, 'x', false, now(), 1, now(), now(), 'test'
		FROM generate_series(1, $1::int) g`, seededAccounts)
	if err != nil {
		t.Fatalf("seed accounts: %v", err)
	}
}

func accountID(n int) string { return fmt.Sprintf("u%06d", n) }

func renameUntilStopped(t *testing.T, stop <-chan struct{}, uri string, writes *atomic.Int64, warmed chan<- struct{}) {
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, uri)
	if err != nil {
		t.Errorf("connect writer: %v", err)
		return
	}
	defer func() { _ = conn.Close(context.Background()) }()
	const rename = "UPDATE accounts SET display_name = $3 WHERE tenant_id = $1 AND id = $2"
	for n := 0; ; n++ {
		select {
		case <-stop:
			return
		default:
		}
		id := accountID(1 + mrand.IntN(seededAccounts))
		var err error
		if n%10 == 0 {
			err = pgx.BeginFunc(ctx, conn, func(tx pgx.Tx) error {
				if _, err := tx.Exec(ctx, rename, "shop", id, fmt.Sprintf("Slow %d", n)); err != nil {
					return err
				}
				time.Sleep(5 * time.Millisecond)
				_, err := tx.Exec(ctx, rename, "shop", accountID(1+mrand.IntN(seededAccounts)), fmt.Sprintf("Slow pair %d", n))
				return err
			})
		} else {
			_, err = conn.Exec(ctx, rename, "shop", id, fmt.Sprintf("Renamed %d", n))
		}
		if err != nil {
			t.Errorf("rename: %v", err)
			return
		}
		if writes.Add(1) == 20 {
			close(warmed)
		}
	}
}

func TestWritesDuringLoadAreInTheCacheAfterReady(t *testing.T) {
	s, raw, uri := itestDatabase(t)
	seedDirectory(t, raw)
	list, err := iamsync.NewList(0)
	if err != nil {
		t.Fatalf("NewList: %v", err)
	}
	cache := directory.NewCache()
	stats := directory.NewStats(cache)
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
		renameUntilStopped(t, stop, uri, &writes, warmed)
	}()
	defer stopWriter()
	select {
	case <-warmed:
	case <-writerDone:
		t.Fatal("the writer stopped before it warmed up")
	}

	before := writes.Load()
	loader := &directory.Loader{Reader: s, Revocations: s, Cache: cache, List: list, Slots: 4, Stats: stats}
	loadCtx, cancelLoad := context.WithTimeout(ctx, time.Minute)
	defer cancelLoad()
	if err := loader.Run(loadCtx); err != nil {
		t.Fatalf("Loader.Run: %v", err)
	}
	during := writes.Load() - before
	stopWriter()
	if !cache.Ready() || during == 0 {
		t.Fatalf("ready=%v writes during load=%d; want ready with writes during the load", cache.Ready(), during)
	}

	follower := &directory.Follower{Reader: s, Revocations: s, Cache: cache, List: list, Stats: stats}
	passes := 1
	follower.DirectoryPass(ctx)
	for deadline := time.Now().Add(30 * time.Second); stats.Stalled() && time.Now().Before(deadline); passes++ {
		time.Sleep(200 * time.Millisecond)
		follower.DirectoryPass(ctx)
	}
	if stats.ReadErrors() != 0 || stats.Stalled() {
		t.Fatalf("read errors=%d stalled=%v after %d passes; want a clean pass below the horizon", stats.ReadErrors(), stats.Stalled(), passes)
	}

	rows, err := raw.Query(ctx, `SELECT a.id, a.display_name, c.ver FROM accounts a
		JOIN directory_changes c ON c.kind = 1 AND c.tenant_id = a.tenant_id AND c.id = a.id
		WHERE a.tenant_id = 'shop' ORDER BY a.id`)
	if err != nil {
		t.Fatalf("read accounts: %v", err)
	}
	type dbRow struct {
		id, name string
		ver      int64
	}
	want, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (dbRow, error) {
		var d dbRow
		return d, r.Scan(&d.id, &d.name, &d.ver)
	})
	if err != nil {
		t.Fatalf("collect accounts: %v", err)
	}
	if len(want) != seededAccounts || cache.Len() != seededAccounts {
		t.Fatalf("db rows=%d cache=%d; want %d each", len(want), cache.Len(), seededAccounts)
	}
	renamed, mismatched := 0, 0
	for _, d := range want {
		got := cache.Lookup("shop", []string{d.id})
		if len(got) != 1 || got[0].DisplayName != d.name || got[0].Ver != d.ver {
			mismatched++
			if mismatched <= 5 {
				t.Errorf("%s: cache %+v; db name %q ver %d", d.id, got, d.name, d.ver)
			}
		}
		if d.ver > 1 {
			renamed++
		}
	}
	if mismatched > 0 || renamed == 0 {
		t.Fatalf("mismatched=%d renamed=%d; want 0 mismatched and some renamed", mismatched, renamed)
	}
	t.Logf("writes during load=%d renamed accounts=%d load=%.3fs passes=%d", during, renamed, stats.LastLoadSeconds(), passes)
}
