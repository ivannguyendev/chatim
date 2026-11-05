package auth_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/ivannguyendev/chatim/pkg/auth"
)

func TestRealHTTPFetchesListAndKeys(t *testing.T) {
	expires := time.Now().Add(10 * time.Minute)
	mux := http.NewServeMux()
	mux.HandleFunc("GET /internal/v1/revocations", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+secret {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		_, _ = w.Write(snapshotBody(t, phoneRevoked(expires)))
	})
	mux.HandleFunc("GET /.well-known/jwks.json", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"keys":[]}`))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	cfg := testSyncConfig()
	cfg.InternalURL, cfg.JWKSURL, cfg.AllowHTTP = srv.URL, srv.URL+"/.well-known/jwks.json", true
	list, _ := auth.NewRevocationList(auth.RevocationListConfig{Leeway: time.Second, MaxKeep: 17 * time.Minute, MaxAhead: time.Minute})
	keys := &fakeKeys{}
	syncer, err := auth.NewSyncer(cfg, list, keys, nil, nil, nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		syncer.Run(ctx)
	}()
	select {
	case <-syncer.Booted():
	case <-time.After(5 * time.Second):
		t.Fatal("not booted")
	}
	cancel()
	<-done
	if syncer.Check(phoneClaims(time.Now())) != auth.SessionRevoked || keys.count() != 1 {
		t.Fatalf("list or keys not loaded over http (keys set %d)", keys.count())
	}
}

func TestRedirectsAreNotFollowed(t *testing.T) {
	plain := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"keys":[]}`))
	}))
	t.Cleanup(plain.Close)
	redirect := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, plain.URL+r.URL.Path, http.StatusFound)
	}))
	t.Cleanup(redirect.Close)
	cfg := testSyncConfig()
	cfg.InternalURL, cfg.JWKSURL, cfg.AllowHTTP, cfg.ReadyTimeout = redirect.URL, redirect.URL+"/.well-known/jwks.json", true, 200*time.Millisecond
	list, _ := auth.NewRevocationList(auth.RevocationListConfig{MaxKeep: time.Minute})
	keys := &fakeKeys{}
	syncer, err := auth.NewSyncer(cfg, list, keys, nil, nil, nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		syncer.Run(ctx)
	}()
	<-syncer.Booted()
	cancel()
	<-done
	if keys.count() != 0 {
		t.Fatal("keys were taken from a redirect target")
	}
}

func TestURLsWithUserinfoAreRefused(t *testing.T) {
	cfg := testSyncConfig()
	cfg.InternalURL = "http://user:pass@chatim-identity-1:8081"
	list, _ := auth.NewRevocationList(auth.RevocationListConfig{MaxKeep: time.Minute})
	if _, err := auth.NewSyncer(cfg, list, &fakeKeys{}, nil, nil, nil); err == nil {
		t.Fatal("accepted a url with userinfo")
	}
}
