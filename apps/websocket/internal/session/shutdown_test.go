package session

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"testing/synctest"
	"time"

	"google.golang.org/grpc/codes"
)

func TestDrainAnswersRunningCommandsThenCloseAllSends1001(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h := newHarness(t, testOptions())
		running := h.core.hold(1)
		tc := h.dial(true)
		tc.auth(h.token("u1", time.Hour))
		tc.history(1)
		synctest.Wait()
		drained := make(chan error, 1)
		go func() { drained <- h.server.Drain(context.Background()) }()
		synctest.Wait()
		tc.history(2)
		resp := tc.next()
		if resp.GetId() != 2 || resp.GetCode() != int32(codes.Unavailable) {
			t.Fatalf("request during drain = %v", resp)
		}
		if status := h.server.admit(upgradeRequest(Subprotocol)); status != http.StatusServiceUnavailable {
			t.Fatalf("admit during drain = %d", status)
		}
		select {
		case err := <-drained:
			t.Fatalf("drain returned %v with a command running", err)
		default:
		}
		running <- codes.OK
		if resp := tc.next(); resp.GetId() != 1 || resp.GetCode() != 0 {
			t.Fatalf("running command response = %v", resp)
		}
		if err := <-drained; err != nil {
			t.Fatalf("drain: %v", err)
		}
		h.server.CloseAll(CodeGoingAway)
		if got := tc.waitClosed(); got != CodeGoingAway {
			t.Fatalf("close code = %d, want %d", got, CodeGoingAway)
		}
		if err := h.server.Wait(context.Background()); err != nil {
			t.Fatalf("wait: %v", err)
		}
		if h.server.stats.rejected[rejectShutdown].Load() != 1 {
			t.Fatal("shutdown rejection not counted")
		}
	})
}

func TestShutdownPastItsDeadlineCancelsRunningCommandsAndCloses(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h := newHarness(t, testOptions())
		h.core.hold(1)
		tc := h.dial(true)
		tc.auth(h.token("u1", time.Hour))
		tc.history(1)
		synctest.Wait()
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if err := h.server.Shutdown(ctx); !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("shutdown = %v, want deadline exceeded", err)
		}
		if got := tc.waitClosed(); got != CodeGoingAway {
			t.Fatalf("close code = %d, want %d", got, CodeGoingAway)
		}
		if err := h.server.Wait(context.Background()); err != nil {
			t.Fatalf("wait: %v", err)
		}
	})
}

func TestShutdownWithIdleConnectionsClosesThemWith1001(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h := newHarness(t, testOptions())
		authed, fresh := h.dial(true), h.dial(true)
		authed.auth(h.token("u1", time.Hour))
		synctest.Wait()
		if err := h.server.Shutdown(context.Background()); err != nil {
			t.Fatalf("shutdown: %v", err)
		}
		for _, tc := range []*testClient{authed, fresh} {
			if got := tc.waitClosed(); got != CodeGoingAway {
				t.Fatalf("close code = %d, want %d", got, CodeGoingAway)
			}
		}
		if h.closes(CodeGoingAway) != 2 || h.server.stats.unauth.Load() != 0 {
			t.Fatalf("1001 closes = %d, unauth = %d", h.closes(CodeGoingAway), h.server.stats.unauth.Load())
		}
	})
}
