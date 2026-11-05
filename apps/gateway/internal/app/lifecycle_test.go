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

func TestServesOverH2CAndIsReadyWithAChatNode(t *testing.T) {
	keys := newSigner(t)
	chat := startChatNode(t, &fakeChat{})
	lis, logs := newListeners(), newLogWatch()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := runAsync(ctx, testConfig(t, keys.path, chat), logs.logger(), lis)
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
		`chatim_gateway_chat_call_seconds_count{command="GetHistory"} 1`,
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

func TestRequestsRotateOverEveryChatNodeInTheTable(t *testing.T) {
	keys := newSigner(t)
	first, second := &fakeChat{}, &fakeChat{}
	lis, logs := newListeners(), newLogWatch()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := runAsync(ctx, testConfig(t, keys.path, startChatNode(t, first), startChatNode(t, second)), logs.logger(), lis)
	receive(t, lis.admin, "admin listener")
	apiAddr := receive(t, lis.api, "api listener")
	logs.await(t, "gateway ready")
	client := h2cClient(t, apiAddr, keys.token(t))
	for range 4 {
		if _, err := client.GetHistory(t.Context(), connect.NewRequest(&chatimv1.GetHistoryRequest{RoomId: "42"})); err != nil {
			t.Fatalf("GetHistory = %v", err)
		}
	}
	if a, b := first.calls.Load(), second.calls.Load(); a == 0 || b == 0 || a+b != 4 {
		t.Fatalf("calls = %d and %d, want both chat nodes used", a, b)
	}
	cancel()
	if err := receive(t, done, "Run return"); err != nil {
		t.Fatalf("Run = %v", err)
	}
}

func TestADeadChatNodeAnswersUnavailableAndTheGatewayStaysReady(t *testing.T) {
	keys := newSigner(t)
	lis, logs := newListeners(), newLogWatch()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := runAsync(ctx, testConfig(t, keys.path, deadAddr(t)), logs.logger(), lis)
	adminAddr, apiAddr := receive(t, lis.admin, "admin listener"), receive(t, lis.api, "api listener")
	logs.await(t, "gateway ready")
	_, err := h2cClient(t, apiAddr, keys.token(t)).GetHistory(t.Context(), connect.NewRequest(&chatimv1.GetHistoryRequest{RoomId: "42"}))
	if connect.CodeOf(err) != connect.CodeUnavailable {
		t.Fatalf("GetHistory = %v, want unavailable", err)
	}
	if code, _ := httpStatus(t, adminAddr, "/readyz"); code != http.StatusOK {
		t.Fatalf("/readyz = %d, want 200", code)
	}
	cancel()
	if err := receive(t, done, "Run return"); err != nil {
		t.Fatalf("Run = %v", err)
	}
}

func TestAnEmptyNodeTableKeepsTheGatewayReadyAndAnswersUnavailable(t *testing.T) {
	keys := newSigner(t)
	lis, logs := newListeners(), newLogWatch()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := runAsync(ctx, testConfig(t, keys.path), logs.logger(), lis)
	adminAddr, apiAddr := receive(t, lis.admin, "admin listener"), receive(t, lis.api, "api listener")
	logs.await(t, "gateway ready")
	_, err := h2cClient(t, apiAddr, keys.token(t)).GetHistory(t.Context(), connect.NewRequest(&chatimv1.GetHistoryRequest{RoomId: "42"}))
	if connect.CodeOf(err) != connect.CodeUnavailable {
		t.Fatalf("GetHistory = %v, want unavailable", err)
	}
	if code, _ := httpStatus(t, adminAddr, "/readyz"); code != http.StatusOK {
		t.Fatalf("/readyz = %d, want 200", code)
	}
	cancel()
	if err := receive(t, done, "Run return"); err != nil {
		t.Fatalf("Run = %v", err)
	}
}

func TestRunFailsWhenTheNodeTableCannotBeRead(t *testing.T) {
	keys := newSigner(t)
	cfg := testConfig(t, keys.path)
	cfg.Redis.Addr = deadAddr(t)
	lis := newListeners()
	err := run(t.Context(), cfg, newLogWatch().logger(), Deps{Listen: lis.listen, ReadyTimeout: 300 * time.Millisecond})
	if err == nil || !strings.Contains(err.Error(), "open chat router") {
		t.Fatalf("Run = %v, want the router error", err)
	}
	select {
	case addr := <-lis.admin:
		t.Fatalf("admin listened on %s before the node table was read", addr)
	default:
	}
}

func TestShutdownWaitsForTheCallInFlight(t *testing.T) {
	keys := newSigner(t)
	fake := &fakeChat{started: make(chan struct{}, 1), release: make(chan struct{})}
	chat := startChatNode(t, fake)
	lis, logs := newListeners(), newLogWatch()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := runAsync(ctx, testConfig(t, keys.path, chat), logs.logger(), lis)
	adminAddr, apiAddr := receive(t, lis.admin, "admin listener"), receive(t, lis.api, "api listener")
	logs.await(t, "gateway ready")
	called := make(chan error, 1)
	go func() {
		_, err := h2cClient(t, apiAddr, keys.token(t)).GetHistory(context.WithoutCancel(ctx), connect.NewRequest(&chatimv1.GetHistoryRequest{RoomId: "42"}))
		called <- err
	}()
	receive(t, fake.started, "chat call")
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
	bad := testConfig(t, keys.path, "chat:9000")
	bad.ShutdownBudget = time.Second
	if err := run(t.Context(), bad, newLogWatch().logger(), Deps{}); err == nil || !strings.Contains(err.Error(), "GW_SHUTDOWN_BUDGET") {
		t.Fatalf("Run = %v", err)
	}
	missing := testConfig(t, keys.path+".missing", "chat:9000")
	if err := run(t.Context(), missing, newLogWatch().logger(), Deps{}); err == nil || !strings.Contains(err.Error(), "jwt key set") {
		t.Fatalf("Run = %v", err)
	}
}

func TestHTTPServerCarriesTheConfiguredLimits(t *testing.T) {
	cfg := testConfig(t, "unused", "chat:9000")
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
