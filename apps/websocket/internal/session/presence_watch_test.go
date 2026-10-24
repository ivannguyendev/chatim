package session

import (
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"testing"
	"testing/synctest"
	"time"
)

func TestWatchPresenceAnswersTheWholeListAtOnceAndOnEveryHeartbeatAndPing(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h := newHarness(t, testOptions())
		h.presence.setOnline("t1", "lan")
		tc := h.readyClient("u1")
		tc.watch("lan", "nam")
		synctest.Wait()
		tc.expectPresence("lan+", "nam")
		tc.noFrame()
		if ask := h.presence.lastAsk(); ask.tenant != "t1" || !slices.Equal(ask.users, []string{"lan", "nam"}) {
			t.Fatalf("ask = %+v, want t1 [lan nam]", ask)
		}
		tc.heartbeat()
		synctest.Wait()
		tc.expectPresence("lan+", "nam")
		tc.noFrame()
		tc.watch("nam", "an")
		synctest.Wait()
		tc.expectPresence("nam", "an")
		_ = tc.conn.WritePing(nil)
		<-tc.pongs
		synctest.Wait()
		tc.expectPresence("nam", "an")
		if h.presenceMetric("presence_answers_total", "") != 4 || h.presenceMetric("presence_watching_connections", "") != 1 {
			t.Fatalf("answers %v watching %v, want 4 and 1", h.presenceMetric("presence_answers_total", ""), h.presenceMetric("presence_watching_connections", ""))
		}
		tc.close()
		if got := h.presenceMetric("presence_watching_connections", ""); got != 0 {
			t.Fatalf("watching after close = %v, want 0", got)
		}
	})
}

func TestAnEmptyListIsNeverAnswered(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h := newHarness(t, testOptions())
		tc := h.readyClient("u1")
		tc.heartbeat()
		tc.watch("lan")
		synctest.Wait()
		tc.expectPresence("lan")
		tc.watch()
		tc.heartbeat()
		synctest.Wait()
		tc.noFrame()
		if h.presence.askCount() != 1 || h.presenceMetric("presence_watching_connections", "") != 0 {
			t.Fatalf("asks %d watching %v, want 1 and 0", h.presence.askCount(), h.presenceMetric("presence_watching_connections", ""))
		}
		tc.close()
	})
}

func TestWatchPresenceDropsInvalidAndDuplicateIDs(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h := newHarness(t, testOptions())
		tc := h.readyClient("u1")
		tc.watch("lan", "", "bad id", "lan", strings.Repeat("x", 65), "nam", "nam")
		synctest.Wait()
		tc.expectPresence("lan", "nam")
		if got := h.presenceMetric("presence_watch_truncated_total", ""); got != 0 {
			t.Fatalf("truncated = %v, want 0", got)
		}
		tc.close()
	})
}

func TestWatchPresenceKeepsTheFirstLimitUsersAndCountsTheCut(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		opts := testOptions()
		opts.FrameMax = 8 << 10
		h := newHarness(t, opts)
		tc := h.readyClient("u1")
		users := make([]string, 250)
		for i := range users {
			users[i] = fmt.Sprintf("u%03d", i)
		}
		tc.watch(users...)
		synctest.Wait()
		tc.expectPresence(users[:200]...)
		if got := h.presenceMetric("presence_watch_truncated_total", ""); got != 1 {
			t.Fatalf("truncated = %v, want 1", got)
		}
		tc.close()
	})
}

func TestWatchingWorksBeforeReady(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h := newHarness(t, testOptions())
		h.loader.fail(errors.New("core down"))
		tc := h.dial(true)
		tc.auth(h.token("u1", time.Hour))
		if f := tc.nextFrame(); f.GetLoadFailed() == nil {
			t.Fatalf("first frame = %v, want load_failed", f)
		}
		tc.watch("lan")
		synctest.Wait()
		tc.expectPresence("lan")
		tc.close()
	})
}

func TestTheTenantComesFromTheToken(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h := newHarness(t, testOptions())
		h.presence.setOnline("t1", "lan")
		tc := h.dial(true)
		tc.auth(h.verifier.issue("other-tenant", "t2", "u1", time.Now().Add(time.Hour)))
		if f := tc.nextFrame(); f.GetReady() == nil {
			t.Fatalf("first frame = %v, want ready", f)
		}
		tc.watch("lan")
		synctest.Wait()
		tc.expectPresence("lan")
		if got := h.presence.lastAsk().tenant; got != "t2" {
			t.Fatalf("ask tenant = %q, want t2", got)
		}
		tc.close()
	})
}

func TestAHeartbeatWhileAnAnswerIsInFlightIsDroppedAsBusy(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h := newHarness(t, testOptions())
		hold := h.presence.holdAnswers()
		tc := h.readyClient("u1")
		tc.watch("lan")
		synctest.Wait()
		tc.heartbeat()
		tc.history(1)
		if resp := tc.next(); resp.GetId() != 1 {
			t.Fatalf("response = %v, want id 1 while an answer hangs", resp)
		}
		synctest.Wait()
		tc.noFrame()
		if h.presenceMetric("presence_answer_dropped_total", "busy") != 1 || h.presence.askCount() != 1 {
			t.Fatalf("busy %v asks %d, want 1 and 1", h.presenceMetric("presence_answer_dropped_total", "busy"), h.presence.askCount())
		}
		h.presence.release(hold)
		synctest.Wait()
		tc.expectPresence("lan")
		tc.heartbeat()
		synctest.Wait()
		tc.expectPresence("lan")
		tc.close()
	})
}

func TestAFailedReadSendsNothingAndTheNextHeartbeatAnswers(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h := newHarness(t, testOptions())
		h.presence.fail(errors.New("redis down"))
		tc := h.readyClient("u1")
		tc.watch("lan")
		tc.heartbeat()
		tc.history(1)
		if resp := tc.next(); resp.GetId() != 1 {
			t.Fatalf("response = %v, want id 1 during a redis outage", resp)
		}
		synctest.Wait()
		tc.noFrame()
		h.presence.fail(nil)
		tc.heartbeat()
		synctest.Wait()
		tc.expectPresence("lan")
		if !tc.isOpen() {
			t.Fatal("connection closed")
		}
		tc.close()
	})
}

func TestAFullSendQueueDropsTheAnswerAndTheNextHeartbeatAnswers(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h := newHarness(t, offerOptions())
		tc, c := h.dialConnection(false)
		tc.auth(h.token("u1", time.Hour))
		synctest.Wait()
		for _, id := range []string{"a-", "b-", "c-"} {
			roomSink{c: c}.Send(sizedFrame(t, id, 20))
		}
		tc.watch("lan")
		synctest.Wait()
		if got := h.presenceMetric("presence_answer_dropped_total", "send_full"); got != 1 {
			t.Fatalf("send_full drops = %v, want 1", got)
		}
		tc.startReading()
		if f := tc.nextFrame(); f.GetReady() == nil {
			t.Fatalf("first frame = %v, want ready", f)
		}
		for range 3 {
			if f := tc.nextFrame(); f.GetEvent() == nil {
				t.Fatalf("frame = %v, want a queued event", f)
			}
		}
		tc.heartbeat()
		synctest.Wait()
		tc.expectPresence("lan")
		if c.closing.Load() != 0 {
			t.Fatalf("connection closing with %d", c.closing.Load())
		}
		tc.close()
	})
}

func TestAConnectionClosingDuringAnAnswerLeavesNoGoroutine(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h := newHarness(t, testOptions())
		h.presence.holdAnswers()
		tc := h.readyClient("u1")
		tc.watch("lan")
		synctest.Wait()
		tc.close()
		synctest.Wait()
		if h.presence.askCount() != 1 || h.presenceMetric("presence_answers_total", "") != 0 {
			t.Fatalf("asks %d answers %v, want 1 and 0", h.presence.askCount(), h.presenceMetric("presence_answers_total", ""))
		}
	})
}

func TestWatchFramesGoThroughTheSharedLimiter(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		opts := testOptions()
		opts.CommandRate, opts.CommandBurst = 1, 3
		h := newHarness(t, opts)
		tc := h.readyClient("u1")
		tc.watch("lan")
		tc.watch("nam")
		tc.watch("an")
		if got := tc.waitClosed(); got != CodePolicyViolation {
			t.Fatalf("close code = %d, want %d", got, CodePolicyViolation)
		}
	})
}

func TestWatchWhileWaitingGetsAuthRequiredAndNoAnswer(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h := newHarness(t, testOptions())
		tc := h.dial(true)
		tc.watch("lan")
		tc.expectAuthRequired(promptMissing)
		tc.heartbeat()
		synctest.Wait()
		tc.noFrame()
		if h.presence.askCount() != 0 {
			t.Fatal("presence asked before auth")
		}
		tc.close()
	})
}

func TestNewRequiresAPositivePresenceWatchLimit(t *testing.T) {
	opts := testOptions()
	opts.PresenceWatchMax = 0
	rooms := newTestInterest(t)
	if _, err := New(opts, newFakeVerifier(t), newFakeCore(), rooms, newTestTyping(t, rooms), &fakePresence{}, slog.New(slog.DiscardHandler)); err == nil {
		t.Fatal("New accepted a zero presence watch limit")
	}
}
