package session

import (
	"testing"
	"testing/synctest"
	"time"

	"google.golang.org/grpc/codes"
)

func TestRequestsOverTheRateGetResourceExhaustedWithoutCallingCore(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		opts := testOptions()
		opts.CommandRate, opts.CommandBurst = 1, 3
		h := newHarness(t, opts)
		tc := h.dial(true)
		tc.auth(h.token("u1", time.Hour))
		for id := uint64(1); id <= 3; id++ {
			tc.history(id)
		}
		codesByID := map[uint64]int32{}
		for range 3 {
			resp := tc.next()
			codesByID[resp.GetId()] = resp.GetCode()
		}
		if codesByID[1] != 0 || codesByID[2] != 0 || codesByID[3] != int32(codes.ResourceExhausted) {
			t.Fatalf("codes = %v", codesByID)
		}
		if h.core.calls.Load() != 2 || h.server.stats.rejected[rejectRate].Load() != 1 {
			t.Fatalf("core calls = %d, rate rejections = %d", h.core.calls.Load(), h.server.stats.rejected[rejectRate].Load())
		}
		time.Sleep(time.Second)
		tc.history(4)
		if resp := tc.next(); resp.GetCode() != 0 {
			t.Fatalf("after refill code = %d", resp.GetCode())
		}
		tc.close()
	})
}

func TestAuthFloodOverTheRateClosesWith1008BeforeCheckingTheSignature(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		opts := testOptions()
		opts.CommandRate, opts.CommandBurst = 1, 3
		h := newHarness(t, opts)
		tc := h.dial(true)
		token := h.token("u1", time.Hour)
		for range 5 {
			tc.auth(token)
		}
		if got := tc.waitClosed(); got != CodePolicyViolation {
			t.Fatalf("close code = %d, want %d", got, CodePolicyViolation)
		}
		if got := h.verifier.calls.Load(); got != 3 {
			t.Fatalf("signature checks = %d, want 3", got)
		}
	})
}

func TestHeartbeatFloodOverTheRateClosesWith1008(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		opts := testOptions()
		opts.CommandRate, opts.CommandBurst = 1, 3
		h := newHarness(t, opts)
		tc := h.dial(true)
		tc.auth(h.token("u1", time.Hour))
		for range 4 {
			tc.heartbeat()
		}
		if got := tc.waitClosed(); got != CodePolicyViolation {
			t.Fatalf("close code = %d, want %d", got, CodePolicyViolation)
		}
	})
}

func TestRequestsOverTheInflightLimitGetResourceExhausted(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		opts := testOptions()
		opts.CommandInflight = 2
		h := newHarness(t, opts)
		first, second := h.core.hold(1), h.core.hold(2)
		tc := h.dial(true)
		tc.auth(h.token("u1", time.Hour))
		tc.history(1)
		tc.send(2, "88")
		tc.history(3)
		resp := tc.next()
		if resp.GetId() != 3 || resp.GetCode() != int32(codes.ResourceExhausted) {
			t.Fatalf("response = %v", resp)
		}
		if h.server.stats.rejected[rejectInflight].Load() != 1 {
			t.Fatal("inflight rejection not counted")
		}
		first <- codes.OK
		second <- codes.OK
		tc.next()
		tc.next()
		tc.history(4)
		if resp := tc.next(); resp.GetId() != 4 || resp.GetCode() != 0 {
			t.Fatalf("response after release = %v", resp)
		}
		tc.close()
	})
}

func TestClientThatNeverReadsIsCutAfterTheWriteTimeout(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h := newHarness(t, testOptions())
		tc := h.dial(false)
		start := time.Now()
		tc.auth(h.token("u1", time.Hour))
		tc.history(1)
		<-tc.serverDone
		if elapsed := time.Since(start); elapsed != testOptions().WriteTimeout {
			t.Fatalf("cut after %v, want %v", elapsed, testOptions().WriteTimeout)
		}
		if h.closes(CodeAbnormal) != 1 || h.server.stats.open.Load() != 0 {
			t.Fatalf("aborts = %d, open = %d", h.closes(CodeAbnormal), h.server.stats.open.Load())
		}
		_ = tc.raw.Close()
	})
}

func TestResponsesOverAFullFrameQueueCloseWith4008(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		opts := testOptions()
		opts.SendQueue = 2
		h := newHarness(t, opts)
		tc, c := h.dialConnection(false)
		start := time.Now()
		tc.auth(h.token("u1", time.Hour))
		for id := uint64(1); id <= 6; id++ {
			tc.history(id)
		}
		synctest.Wait()
		if elapsed := time.Since(start); elapsed != 0 {
			t.Fatalf("decided after %v, want at once", elapsed)
		}
		if got := c.closing.Load(); got != uint32(CodeSlowClient) {
			t.Fatalf("closing = %d, want %d", got, CodeSlowClient)
		}
		tc.startReading()
		if got := tc.waitClosed(); got != CodeSlowClient {
			t.Fatalf("close code = %d, want %d", got, CodeSlowClient)
		}
		if h.closes(CodeSlowClient) != 1 || h.closes(CodeAbnormal) != 0 {
			t.Fatalf("closes 4008 = %d, aborts = %d", h.closes(CodeSlowClient), h.closes(CodeAbnormal))
		}
	})
}
