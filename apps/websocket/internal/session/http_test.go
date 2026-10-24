package session

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ivannguyendev/chatim/pkg/metrics"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
	"github.com/ivannguyendev/chatim/pkg/wsclient"
)

func upgradeRequest(protocols string) *http.Request {
	r := httptest.NewRequest(http.MethodGet, "/ws", nil)
	r.Header.Set("Connection", "Upgrade")
	r.Header.Set("Upgrade", "websocket")
	r.Header.Set("Sec-WebSocket-Version", "13")
	r.Header.Set("Sec-WebSocket-Key", "dGhlIHNhbXBsZSBub25jZQ==")
	if protocols != "" {
		r.Header.Set(subprotocolHeader, protocols)
	}
	return r
}

func TestUpgradeWithoutTheSubprotocolIsRefusedWith400(t *testing.T) {
	for name, protocols := range map[string]string{"missing": "", "other": "chat.v2", "prefix": "chatim.v10"} {
		t.Run(name, func(t *testing.T) {
			h := newHarness(t, testOptions())
			rec := httptest.NewRecorder()
			h.server.ServeHTTP(rec, upgradeRequest(protocols))
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400", rec.Code)
			}
			if h.server.stats.unauth.Load() != 0 {
				t.Fatal("refused upgrade kept an unauthenticated slot")
			}
		})
	}
}

func TestSubprotocolIsFoundInAList(t *testing.T) {
	if status := newHarness(t, testOptions()).server.admit(upgradeRequest("other, chatim.v1")); status != http.StatusOK {
		t.Fatalf("status = %d, want 200", status)
	}
}

func TestUpgradeOverTheUnauthenticatedLimitIsRefusedWith503(t *testing.T) {
	opts := testOptions()
	opts.UnauthMax = 1
	h := newHarness(t, opts)
	if status := h.server.admit(upgradeRequest(Subprotocol)); status != http.StatusOK {
		t.Fatalf("first admit = %d", status)
	}
	rec := httptest.NewRecorder()
	h.server.ServeHTTP(rec, upgradeRequest(Subprotocol))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", rec.Code)
	}
	if got := h.server.stats.unauth.Load(); got != 1 {
		t.Fatalf("unauthenticated = %d, want 1", got)
	}
}

func TestConcurrentUpgradeBurstNeverPassesTheUnauthenticatedLimit(t *testing.T) {
	opts := testOptions()
	opts.UnauthMax = 10
	h := newHarness(t, opts)
	var admitted atomic.Int64
	var wg sync.WaitGroup
	start := make(chan struct{})
	for range 200 {
		wg.Go(func() {
			<-start
			if h.server.admit(upgradeRequest(Subprotocol)) == http.StatusOK {
				admitted.Add(1)
			}
		})
	}
	close(start)
	wg.Wait()
	if admitted.Load() != 10 || h.server.stats.unauth.Load() != 10 {
		t.Fatalf("admitted = %d, unauthenticated = %d, want 10", admitted.Load(), h.server.stats.unauth.Load())
	}
}

func TestServeHTTPUpgradesAndRunsRequestsOverTCP(t *testing.T) {
	h := newHarness(t, testOptions())
	httpServer := httptest.NewServer(h.server)
	defer httpServer.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	client, err := wsclient.Dial(ctx, "ws"+strings.TrimPrefix(httpServer.URL, "http")+"/ws", h.token("u1", time.Hour))
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	resp, err := client.Call(ctx, &chatimv1.ClientRequest{Request: &chatimv1.ClientRequest_GetHistory{
		GetHistory: &chatimv1.GetHistoryRequest{RoomId: "77"},
	}})
	if err != nil || resp.GetCode() != 0 {
		t.Fatalf("call = %v, %v", resp, err)
	}
	if err := h.server.Shutdown(ctx); err != nil {
		t.Fatalf("shutdown: %v", err)
	}
	<-client.Done()
	if err := client.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
}

func TestNewRejectsInvalidOptions(t *testing.T) {
	mutations := map[string]func(o *Options){
		"auth timeout":               func(o *Options) { o.AuthTimeout = 0 },
		"heartbeat":                  func(o *Options) { o.HeartbeatTimeout = 0 },
		"write timeout":              func(o *Options) { o.WriteTimeout = 0 },
		"unauth max":                 func(o *Options) { o.UnauthMax = 0 },
		"frame max":                  func(o *Options) { o.FrameMax = 0 },
		"rate":                       func(o *Options) { o.CommandRate = 0 },
		"burst":                      func(o *Options) { o.CommandBurst = 0 },
		"inflight":                   func(o *Options) { o.CommandInflight = 0 },
		"send queue":                 func(o *Options) { o.SendQueue = 0 },
		"send bytes":                 func(o *Options) { o.SendQueueBytes = 0 },
		"send bytes under one frame": func(o *Options) { o.SendQueueBytes = o.FrameMax - 1 },
		"negative leeway":            func(o *Options) { o.JWTLeeway = -time.Second },
		"typing rate":                func(o *Options) { o.TypingRate = 0 },
		"typing burst":               func(o *Options) { o.TypingBurst = 0 },
	}
	for name, mutate := range mutations {
		t.Run(name, func(t *testing.T) {
			opts := testOptions()
			mutate(&opts)
			if _, err := New(opts, newFakeVerifier(t), newFakeCore(), newTestInterest(t), newTestTyping(t, newTestInterest(t)), slog.New(slog.DiscardHandler)); err == nil {
				t.Fatal("New accepted invalid options")
			}
		})
	}
	if _, err := New(testOptions(), nil, newFakeCore(), newTestInterest(t), newTestTyping(t, newTestInterest(t)), slog.New(slog.DiscardHandler)); err == nil {
		t.Fatal("New accepted a nil verifier")
	}
	if _, err := New(testOptions(), newFakeVerifier(t), newFakeCore(), nil, newTestTyping(t, newTestInterest(t)), slog.New(slog.DiscardHandler)); err == nil {
		t.Fatal("New accepted a nil interest")
	}
	if _, err := New(testOptions(), newFakeVerifier(t), newFakeCore(), newTestInterest(t), nil, slog.New(slog.DiscardHandler)); err == nil {
		t.Fatal("New accepted a nil typing service")
	}
}

func TestSourcesExposeEveryCounterWithFixedLabels(t *testing.T) {
	h := newHarness(t, testOptions())
	h.server.stats.closed(CodeAuthTimeout)
	h.server.stats.closed(CodeSlowClient)
	h.server.stats.closed(4999)
	h.server.stats.authFailed("bad_signature")
	h.server.stats.reject(rejectInflight)
	read := func(name, key, value string) float64 {
		for _, s := range h.server.Sources() {
			if s.Name == name && s.Labels[key] == value {
				return s.Read()
			}
		}
		t.Fatalf("metric %s{%s=%q} missing", name, key, value)
		return 0
	}
	if read("closes_total", "code", "4002") != 1 || read("closes_total", "code", "4008") != 1 || read("closes_total", "code", "other") != 1 {
		t.Fatal("close counters wrong")
	}
	if read("auth_failures_total", "reason", "bad_signature") != 1 || read("auth_failures_total", "reason", "identity_changed") != 0 {
		t.Fatal("auth failure counters wrong")
	}
	if read("commands_rejected_total", "reason", "inflight") != 1 {
		t.Fatal("rejection counter wrong")
	}
	if _, err := metrics.Handler("websocket", h.server.Sources()); err != nil {
		t.Fatalf("sources do not register: %v", err)
	}
}
