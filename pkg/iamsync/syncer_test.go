package iamsync_test

import (
	"context"
	"net/http"
	"slices"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/ivannguyendev/chatim/pkg/iamsync"
	"github.com/ivannguyendev/chatim/pkg/jwtverify"
	"github.com/ivannguyendev/chatim/pkg/revocation"
)

func phoneClaims(issued time.Time) jwtverify.Claims {
	return jwtverify.Claims{Tenant: "shop", User: "lan", Session: "iphone", IssuedAt: issued}
}

func TestNewRejectsBadConfig(t *testing.T) {
	list, _ := revocation.New(revocation.Config{MaxKeep: time.Minute})
	cases := map[string]func(*iamsync.Config){
		"jwks over http":       func(c *iamsync.Config) { c.JWKSURL = "http://iam/.well-known/jwks.json" },
		"relative internal":    func(c *iamsync.Config) { c.InternalURL = "/internal" },
		"internal no secret":   func(c *iamsync.Config) { c.Secret = "" },
		"no internal url":      func(c *iamsync.Config) { c.InternalURL = "" },
		"no jwks url":          func(c *iamsync.Config) { c.JWKSURL = "" },
		"keys over a minute":   func(c *iamsync.Config) { c.KeysEvery = iamsync.MaxKeysInterval + time.Second },
		"zero revoke interval": func(c *iamsync.Config) { c.RevocationsEvery = 0 },
		"zero body max":        func(c *iamsync.Config) { c.BodyMax = 0 },
	}
	for name, change := range cases {
		cfg := testConfig()
		change(&cfg)
		if _, err := iamsync.New(cfg, list, &fakeKeys{}, nil, &fakeDoer{}, nil); err == nil {
			t.Fatalf("%s: New accepted a bad config", name)
		}
	}
	cfg := testConfig()
	cfg.JWKSURL, cfg.AllowHTTP = "http://iam/.well-known/jwks.json", true
	if _, err := iamsync.New(cfg, list, &fakeKeys{}, nil, &fakeDoer{}, nil); err != nil {
		t.Fatalf("http jwks with AllowHTTP: %v", err)
	}
}

func TestRevocationsAreMergedAndAnnouncedOnce(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		start := time.Now()
		doer := &fakeDoer{keys: okKeys(), revoke: answer{status: http.StatusOK, body: snapshotBody(t, phoneRevoked(start.Add(16*time.Minute))), etag: `"v1"`}}
		r := newRig(t, testConfig(), doer, nil)
		r.start()
		defer r.stop()
		added := <-r.added
		if !slices.Equal(added.Sessions, []string{"iphone"}) {
			t.Fatalf("added = %+v", added)
		}
		if got := r.syncer.Check(phoneClaims(start)); got != revocation.SessionRevoked {
			t.Fatalf("Check = %v", got)
		}
		first := doer.lastRevokeRequest()
		if first.Header.Get("Authorization") != "Bearer "+secret || first.URL.String() != internalURL+"/internal/v1/revocations" {
			t.Fatalf("request = %s %v", first.URL, first.Header)
		}
		doer.setRevoke(answer{status: http.StatusNotModified})
		time.Sleep(2 * time.Second)
		synctest.Wait()
		if got := doer.lastRevokeRequest().Header.Get("If-None-Match"); got != `"v1"` {
			t.Fatalf("If-None-Match = %q", got)
		}
		select {
		case again := <-r.added:
			t.Fatalf("a 304 announced %+v", again)
		default:
		}
	})
}

func TestBootedAfterTheFirstListAndKeys(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		doer := &fakeDoer{keys: okKeys(), revoke: answer{status: http.StatusOK, body: snapshotBody(t, revocation.Snapshot{})}}
		r := newRig(t, testConfig(), doer, nil)
		start := time.Now()
		r.start()
		defer r.stop()
		<-r.syncer.Booted()
		if waited := time.Since(start); waited > 0 {
			t.Fatalf("booted after %v; want at once", waited)
		}
		if r.keys.count() != 1 {
			t.Fatalf("keys set %d times", r.keys.count())
		}
	})
}

func TestBootedAfterTheTimeoutWhenIamIsDown(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		doer := &fakeDoer{keys: answer{err: errDown}, revoke: answer{err: errDown}}
		r := newRig(t, testConfig(), doer, nil)
		start := time.Now()
		r.start()
		defer r.stop()
		<-r.syncer.Booted()
		if waited := time.Since(start); waited != 10*time.Second {
			t.Fatalf("booted after %v; want the 10s ready timeout", waited)
		}
		body := metricsText(t, r.syncer)
		if !strings.Contains(body, "iam_boot_timeouts_total 1") || !strings.Contains(body, `iam_sync_errors_total{kind="revocations"}`) {
			t.Fatalf("metrics = %s", body)
		}
	})
}

func TestFailureFallsBackToTheSharedCopy(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		now := time.Now()
		shared := &fakeShared{value: sharedValue(t, now.Add(-time.Hour), phoneRevoked(now.Add(16*time.Minute)))}
		doer := &fakeDoer{keys: okKeys(), revoke: answer{err: errDown}}
		r := newRig(t, testConfig(), doer, shared)
		r.start()
		defer r.stop()
		added := <-r.added
		if !slices.Equal(added.Sessions, []string{"iphone"}) {
			t.Fatalf("added = %+v", added)
		}
		<-r.syncer.Booted()
		if got := r.syncer.Check(phoneClaims(now)); got != revocation.SessionRevoked {
			t.Fatalf("Check = %v", got)
		}
		if !strings.Contains(metricsText(t, r.syncer), `iam_shared_reads_total{result="merged"} 1`) {
			t.Fatal("merged shared read not counted")
		}
	})
}

func TestSharedCopyNeverRemovesEntries(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		now := time.Now()
		doer := &fakeDoer{keys: okKeys(), revoke: answer{status: http.StatusOK, body: snapshotBody(t, phoneRevoked(now.Add(16*time.Minute)))}}
		shared := &fakeShared{}
		r := newRig(t, testConfig(), doer, shared)
		r.start()
		defer r.stop()
		<-r.added
		doer.setRevoke(answer{err: errDown})
		shared.mu.Lock()
		shared.value = sharedValue(t, now.Add(time.Second), revocation.Snapshot{})
		shared.mu.Unlock()
		time.Sleep(2 * time.Second)
		synctest.Wait()
		if got := r.syncer.Check(phoneClaims(now)); got != revocation.SessionRevoked {
			t.Fatalf("an empty shared copy removed the entry: %v", got)
		}
	})
}

func TestSuccessWritesTheSharedCopy(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		now := time.Now()
		doer := &fakeDoer{keys: okKeys(), revoke: answer{status: http.StatusOK, body: snapshotBody(t, phoneRevoked(now.Add(16*time.Minute)))}}
		shared := &fakeShared{}
		r := newRig(t, testConfig(), doer, shared)
		r.start()
		defer r.stop()
		<-r.syncer.Booted()
		value, ttl, puts := shared.snapshot()
		if puts != 1 || ttl != time.Minute || !strings.Contains(string(value), "iphone") || !strings.Contains(string(value), "fetched_at") {
			t.Fatalf("shared = %s ttl %v puts %d", value, ttl, puts)
		}
		doer.setRevoke(answer{status: http.StatusNotModified})
		time.Sleep(2 * time.Second)
		synctest.Wait()
		if value, _, puts := shared.snapshot(); puts != 2 || !strings.Contains(string(value), "iphone") {
			t.Fatalf("after 304 shared = %s puts %d; want the kept list written again", value, puts)
		}
	})
}

func TestOversizedListIsRefused(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		cfg := testConfig()
		cfg.BodyMax = 64
		now := time.Now()
		doer := &fakeDoer{keys: okKeys(), revoke: answer{status: http.StatusOK, body: snapshotBody(t, phoneRevoked(now.Add(16*time.Minute)))}}
		r := newRig(t, cfg, doer, nil)
		r.start()
		defer r.stop()
		time.Sleep(time.Second)
		synctest.Wait()
		if got := r.syncer.Check(phoneClaims(now)); got != revocation.Allowed {
			t.Fatalf("oversized list applied: %v", got)
		}
	})
}

func TestUnknownKeyRetriesUntilAFreshFetchCompletes(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		doer := &fakeDoer{keys: okKeys(), revoke: answer{status: http.StatusNotModified}, keyCalled: make(chan struct{}, 4)}
		r := newRig(t, testConfig(), doer, nil)
		r.start()
		defer r.stop()
		<-r.syncer.Booted()
		<-doer.keyCalled
		time.Sleep(time.Second)
		if !r.syncer.UnknownKey("k-new") {
			t.Fatal("first sight of an unknown kid did not ask for a retry")
		}
		synctest.Wait()
		if doer.keyCount() != 2 {
			t.Fatalf("key fetches = %d; want one more after the unknown kid", doer.keyCount())
		}
		if r.syncer.UnknownKey("k-new") {
			t.Fatal("still retrying after a fetch that started after the kid was seen")
		}
	})
}

func TestKeyRequestsAreSpacedOut(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		doer := &fakeDoer{keys: okKeys(), revoke: answer{status: http.StatusNotModified}}
		r := newRig(t, testConfig(), doer, nil)
		r.start()
		defer r.stop()
		<-r.syncer.Booted()
		time.Sleep(10 * time.Second)
		for _, kid := range []string{"a", "b", "c", "d"} {
			r.syncer.UnknownKey(kid)
			synctest.Wait()
		}
		if got := doer.keyCount(); got != 2 {
			t.Fatalf("key fetches = %d; want the boot fetch and one requested fetch", got)
		}
		time.Sleep(5 * time.Second)
		r.syncer.UnknownKey("e")
		synctest.Wait()
		if got := doer.keyCount(); got != 3 {
			t.Fatalf("key fetches after the gap = %d; want 3", got)
		}
	})
}

func TestRunStopsWithTheContext(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := newRig(t, testConfig(), &fakeDoer{keys: okKeys(), revoke: answer{status: http.StatusNotModified}}, nil)
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan struct{})
		go func() {
			defer close(done)
			r.syncer.Run(ctx)
		}()
		cancel()
		<-done
	})
}

func TestSharedCopyCountsUnchangedAndBrokenReads(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		now := time.Now()
		shared := &fakeShared{value: sharedValue(t, now.Add(time.Hour), phoneRevoked(now.Add(16*time.Minute)))}
		doer := &fakeDoer{keys: okKeys(), revoke: answer{err: errDown}}
		r := newRig(t, testConfig(), doer, shared)
		r.start()
		defer r.stop()
		<-r.added
		time.Sleep(2 * time.Second)
		synctest.Wait()
		shared.mu.Lock()
		shared.value = []byte("{broken")
		shared.mu.Unlock()
		time.Sleep(2 * time.Second)
		synctest.Wait()
		body := metricsText(t, r.syncer)
		if !strings.Contains(body, `iam_shared_reads_total{result="unchanged"} 1`) || !strings.Contains(body, `iam_shared_reads_total{result="error"} 1`) {
			t.Fatalf("metrics = %s", body)
		}
		if strings.Contains(body, "iam_revocations_age_seconds -") {
			t.Fatal("a future fetched_at made the age negative")
		}
	})
}
