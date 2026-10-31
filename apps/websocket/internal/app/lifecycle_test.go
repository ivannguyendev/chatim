package app

import (
	"context"
	"errors"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/ivannguyendev/chatim/apps/websocket/internal/config"
	"github.com/ivannguyendev/chatim/apps/websocket/internal/session"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
	"github.com/ivannguyendev/chatim/pkg/wstest"
)

func TestRunIsReadyOnlyAfterTheSlotTableAndStopAnswersRunningCommandsBefore1001(t *testing.T) {
	mr := slotRedis(t)
	mr.SetError("LOADING redis is loading the dataset")
	keys := newSigner(t)
	core, dial := startFakeCore(t)
	lis, logs := newListeners(), newLogWatch()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := runAsync(ctx, testConfig(mr.Addr(), keys.path), logs.logger(), Deps{PresenceRedis: newPresenceRedis(t).open, Dial: dial, Listen: lis.listen, NATS: newFakeNATS().dial})

	adminAddr := awaitListener(t, lis.admin)
	if code, _ := httpStatus(t, adminAddr, "/livez"); code != 200 {
		t.Fatalf("/livez = %d before the slot table, want 200", code)
	}
	if code, _ := httpStatus(t, adminAddr, "/readyz"); code != 503 {
		t.Fatalf("/readyz = %d before the slot table, want 503", code)
	}
	_, body := httpStatus(t, adminAddr, "/metrics")
	for _, want := range []string{
		"chatim_websocket_connections 0",
		"chatim_websocket_unauthenticated_connections 0",
		`chatim_websocket_commands_total{code="OK",command="EditMessage"} 0`,
		`chatim_websocket_core_call_seconds_count{command="EditMessage"} 0`,
		`chatim_websocket_commands_rejected_total{reason="shutdown"} 0`,
		"chatim_websocket_unknown_frames_total 0",
		"chatim_websocket_interest_users 0",
		"chatim_websocket_room_ids_load_seconds_count 0",
		`chatim_websocket_events_dropped_total{reason="unknown_kind"} 0`,
		`chatim_websocket_events_delivered_total{level="owner"} 0`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("/metrics lacks %q", want)
		}
	}
	select {
	case <-lis.ws:
		t.Fatal("websocket listener opened before the slot table loaded")
	default:
	}

	mr.SetError("")
	wsAddr := awaitListener(t, lis.ws)
	logs.await(t, "websocket ready")
	if code, _ := httpStatus(t, adminAddr, "/readyz"); code != 200 {
		t.Fatalf("/readyz = %d after start, want 200", code)
	}

	callCtx, callCancel := context.WithTimeout(context.Background(), waitLimit)
	defer callCancel()
	client, err := wstest.Dial(callCtx, "ws://"+wsAddr+"/ws")
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	if err := client.Auth(keys.token(t, time.Now().Add(-5*time.Second))); err != nil {
		t.Fatalf("auth with a token inside the leeway: %v", err)
	}
	defer func() { _ = client.Close() }()
	answered := make(chan *chatimv1.ServerResponse, 1)
	go func() {
		resp, _ := client.Call(callCtx, &chatimv1.ClientRequest{Request: &chatimv1.ClientRequest_EditMessage{
			EditMessage: &chatimv1.EditMessageRequest{RoomId: "42", Seq: 1},
		}})
		answered <- resp
	}()
	receive(t, core.started, "core call")

	cancel()
	logs.await(t, "websocket shutting down")
	if code, _ := httpStatus(t, adminAddr, "/readyz"); code != 503 {
		t.Fatalf("/readyz = %d while stopping, want 503", code)
	}
	close(core.release)
	if resp := receive(t, answered, "command answer"); resp == nil || resp.GetCode() != 0 {
		t.Fatalf("running command answer = %v, want OK", resp)
	}
	receive(t, client.Done(), "connection close")
	if closeErr, ok := errors.AsType[*wstest.CloseError](client.Err()); !ok || closeErr.Code != session.CodeGoingAway {
		t.Fatalf("connection end = %v, want close code 1001", client.Err())
	}
	if err := awaitRun(t, done); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if written := logs.written(); strings.Contains(written, testRedisPassword) || strings.Contains(written, testNATSPassword) {
		t.Fatal("a log line holds the redis or NATS password")
	}
}

func TestRunRefusesToStartOnBadConfigOrMissingKeys(t *testing.T) {
	keys := newSigner(t)
	overBudget := testConfig("127.0.0.1:1", keys.path)
	overBudget.DrainDelay = 10 * time.Second
	missingKeys := testConfig("127.0.0.1:1", keys.path+".missing")
	for name, tc := range map[string]struct {
		cfg  config.Config
		want string
	}{
		"stop plan over budget": {overBudget, "WS_SHUTDOWN_BUDGET"},
		"missing key set":       {missingKeys, "jwt key set"},
	} {
		t.Run(name, func(t *testing.T) {
			lis := newListeners()
			err := run(t.Context(), tc.cfg, newLogWatch().logger(), Deps{PresenceRedis: newPresenceRedis(t).open, Listen: lis.listen})
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("Run = %v, want an error naming %q", err, tc.want)
			}
			if len(lis.admin)+len(lis.ws) != 0 {
				t.Fatal("Run opened a listener before refusing to start")
			}
		})
	}
}

func TestRunFailsWhenTheSlotTableNeverLoads(t *testing.T) {
	mr := slotRedis(t)
	mr.SetError("LOADING redis is loading the dataset")
	keys := newSigner(t)
	lis := newListeners()
	nats := newFakeNATS()
	deps := Deps{PresenceRedis: newPresenceRedis(t).open, Listen: lis.listen, ReadyTimeout: 300 * time.Millisecond, NATS: nats.dial}
	err := run(t.Context(), testConfig(mr.Addr(), keys.path), newLogWatch().logger(), deps)
	if err == nil || !strings.Contains(err.Error(), "slot table") {
		t.Fatalf("Run = %v, want a slot table error", err)
	}
	if len(lis.admin) != 1 || len(lis.ws) != 0 {
		t.Fatalf("listeners opened: admin %d, websocket %d; want only admin", len(lis.admin), len(lis.ws))
	}
	if nats.running() != 0 || nats.closed.Load() != 1 {
		t.Fatalf("feed left open after a failed start: consumers %d, NATS closes %d", nats.running(), nats.closed.Load())
	}
}

func TestRunStopsCleanlyWhenSignalledWhileWaitingForTheSlotTable(t *testing.T) {
	mr := slotRedis(t)
	mr.SetError("LOADING redis is loading the dataset")
	keys := newSigner(t)
	lis := newListeners()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := runAsync(ctx, testConfig(mr.Addr(), keys.path), newLogWatch().logger(), Deps{PresenceRedis: newPresenceRedis(t).open, Listen: lis.listen, NATS: newFakeNATS().dial})
	awaitListener(t, lis.admin)
	cancel()
	if err := awaitRun(t, done); err != nil {
		t.Fatalf("Run = %v, want a clean stop", err)
	}
	if len(lis.ws) != 0 {
		t.Fatal("websocket listener opened after the stop signal")
	}
}

func TestRunFailsWhenTheAdminAddressCannotBeBound(t *testing.T) {
	keys := newSigner(t)
	refuse := func(context.Context, string) (net.Listener, error) { return nil, errors.New("address in use") }
	err := run(t.Context(), testConfig("127.0.0.1:1", keys.path), newLogWatch().logger(), Deps{PresenceRedis: newPresenceRedis(t).open, Listen: refuse})
	if err == nil || !strings.Contains(err.Error(), "address in use") {
		t.Fatalf("Run = %v, want the listen error", err)
	}
}

func TestRunClosesTheWebsocketListenerWhenStartupEndsRightAfterBindingIt(t *testing.T) {
	bindErr := errors.New("bound but broken")
	for name, tc := range map[string]struct {
		cancelOnBind bool
		listenErr    error
	}{
		"stop signal right after bind": {cancelOnBind: true},
		"listener returned with error": {listenErr: bindErr},
	} {
		t.Run(name, func(t *testing.T) {
			mr := slotRedis(t)
			keys := newSigner(t)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			bound := make(chan net.Listener, 1)
			listen := func(ctx context.Context, addr string) (net.Listener, error) {
				var lc net.ListenConfig
				lis, err := lc.Listen(ctx, "tcp", "127.0.0.1:0")
				if err != nil || addr != "ws" {
					return lis, err
				}
				bound <- lis
				if tc.cancelOnBind {
					cancel()
				}
				return lis, tc.listenErr
			}
			logs := newLogWatch()
			err := run(ctx, testConfig(mr.Addr(), keys.path), logs.logger(), Deps{PresenceRedis: newPresenceRedis(t).open, Listen: listen, NATS: newFakeNATS().dial})
			if !errors.Is(err, tc.listenErr) || (tc.listenErr == nil && err != nil) {
				t.Fatalf("Run = %v, want %v", err, tc.listenErr)
			}
			if err := receive(t, bound, "websocket bind").Close(); !errors.Is(err, net.ErrClosed) {
				t.Fatalf("websocket listener left open: second Close = %v", err)
			}
			if strings.Contains(logs.written(), "websocket ready") {
				t.Fatal("node turned ready after an aborted start")
			}
		})
	}
}
