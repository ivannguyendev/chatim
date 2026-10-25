package app

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"

	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

func TestServesOverH2CAndIsReadyWithACore(t *testing.T) {
	keys := newSigner(t)
	core := startCore(t, &fakeCore{})
	lis, logs := newListeners(), newLogWatch()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := runAsync(ctx, testConfig(keys.path, core, deadAddr(t)), logs.logger(), lis)
	adminAddr, apiAddr := receive(t, lis.admin, "admin listener"), receive(t, lis.api, "api listener")
	logs.await(t, "gateway ready")
	if code, _ := httpStatus(t, adminAddr, "/readyz"); code != http.StatusOK {
		t.Fatalf("/readyz = %d", code)
	}
	if code, _ := httpStatus(t, adminAddr, "/livez"); code != http.StatusOK {
		t.Fatalf("/livez = %d", code)
	}
	token := keys.token(t)
	resp, err := h2cClient(t, apiAddr, token).GetHistory(t.Context(), connect.NewRequest(&chatimv1.GetHistoryRequest{RoomId: "42"}))
	if err != nil || resp.Msg == nil {
		t.Fatalf("GetHistory = %v", err)
	}
	_, body := httpStatus(t, adminAddr, "/metrics")
	for _, want := range []string{
		`chatim_gateway_commands_total{code="OK",command="GetHistory"} 1`,
		`chatim_gateway_commands_total{code="Unimplemented",command="unknown"} 0`,
		`chatim_gateway_auth_failures_total{reason="expired"} 0`,
		`chatim_gateway_rate_limited_total{limit="rate"} 0`,
		"chatim_gateway_inflight_rejected_total 0",
		"chatim_gateway_body_too_large_total 0",
		`chatim_gateway_core_call_seconds_count{command="GetHistory"} 1`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("/metrics lacks %q", want)
		}
	}
	if strings.Contains(body, `command="SendMessage"`) {
		t.Error("/metrics counts a realtime command")
	}
	cancel()
	if err := receive(t, done, "Run return"); err != nil {
		t.Fatalf("Run = %v", err)
	}
	if strings.Contains(logs.written(), token) {
		t.Fatal("the token reached the logs")
	}
}

func TestNoCoreMeansNotReadyAndUnavailableCalls(t *testing.T) {
	keys := newSigner(t)
	lis, logs := newListeners(), newLogWatch()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := runAsync(ctx, testConfig(keys.path, deadAddr(t)), logs.logger(), lis)
	adminAddr, apiAddr := receive(t, lis.admin, "admin listener"), receive(t, lis.api, "api listener")
	_, err := h2cClient(t, apiAddr, keys.token(t)).GetHistory(t.Context(), connect.NewRequest(&chatimv1.GetHistoryRequest{RoomId: "42"}))
	if connect.CodeOf(err) != connect.CodeUnavailable {
		t.Fatalf("GetHistory = %v, want unavailable", err)
	}
	if code, _ := httpStatus(t, adminAddr, "/readyz"); code != http.StatusServiceUnavailable {
		t.Fatalf("/readyz = %d, want 503", code)
	}
	cancel()
	if err := receive(t, done, "Run return"); err != nil {
		t.Fatalf("Run = %v", err)
	}
}

func TestShutdownWaitsForTheCallInFlight(t *testing.T) {
	keys := newSigner(t)
	fake := &fakeCore{started: make(chan struct{}, 1), release: make(chan struct{})}
	core := startCore(t, fake)
	lis, logs := newListeners(), newLogWatch()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := runAsync(ctx, testConfig(keys.path, core), logs.logger(), lis)
	adminAddr, apiAddr := receive(t, lis.admin, "admin listener"), receive(t, lis.api, "api listener")
	logs.await(t, "gateway ready")
	called := make(chan error, 1)
	go func() {
		_, err := h2cClient(t, apiAddr, keys.token(t)).GetHistory(context.WithoutCancel(ctx), connect.NewRequest(&chatimv1.GetHistoryRequest{RoomId: "42"}))
		called <- err
	}()
	receive(t, fake.started, "core call")
	cancel()
	logs.await(t, "gateway shutting down")
	if code, _ := httpStatus(t, adminAddr, "/readyz"); code != http.StatusServiceUnavailable {
		t.Fatalf("/readyz while stopping = %d", code)
	}
	time.AfterFunc(200*time.Millisecond, func() { close(fake.release) })
	if err := receive(t, called, "call return"); err != nil {
		t.Fatalf("call in flight = %v", err)
	}
	if err := receive(t, done, "Run return"); err != nil {
		t.Fatalf("Run = %v", err)
	}
}

func TestRunRefusesBadConfigAndKeys(t *testing.T) {
	keys := newSigner(t)
	bad := testConfig(keys.path, "core:9000")
	bad.ShutdownBudget = time.Second
	if err := Run(t.Context(), bad, newLogWatch().logger(), Deps{}); err == nil || !strings.Contains(err.Error(), "GW_SHUTDOWN_BUDGET") {
		t.Fatalf("Run = %v", err)
	}
	missing := testConfig(keys.path+".missing", "core:9000")
	if err := Run(t.Context(), missing, newLogWatch().logger(), Deps{}); err == nil || !strings.Contains(err.Error(), "jwt key set") {
		t.Fatalf("Run = %v", err)
	}
}

func TestHTTPServerCarriesTheConfiguredLimits(t *testing.T) {
	cfg := testConfig("unused", "core:9000")
	srv := newHTTPServer(cfg, http.NotFoundHandler(), newLogWatch().logger())
	if srv.ReadHeaderTimeout != 5*time.Second || srv.ReadTimeout != 10*time.Second || srv.WriteTimeout != 10*time.Second ||
		srv.IdleTimeout != time.Minute || srv.MaxHeaderBytes != 16<<10 || srv.HTTP2 == nil || srv.HTTP2.MaxConcurrentStreams != 250 {
		t.Fatalf("server = %+v", srv)
	}
	if srv.Protocols == nil || !srv.Protocols.HTTP1() || !srv.Protocols.UnencryptedHTTP2() || srv.Protocols.HTTP2() {
		t.Fatalf("protocols = %v", srv.Protocols)
	}
}

func TestMainRefusesBadArguments(t *testing.T) {
	if Main([]string{"nope"}) != 2 || Main([]string{"serve", "extra"}) != 2 {
		t.Fatal("bad arguments accepted")
	}
}
