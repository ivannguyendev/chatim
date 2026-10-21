package session

import (
	"slices"
	"testing"
	"testing/synctest"
	"time"

	"google.golang.org/grpc/codes"
)

func TestSendsToOneRoomKeepOrderAndOtherRoomsRunInParallel(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h := newHarness(t, testOptions())
		first, second, other := h.core.hold(1), h.core.hold(2), h.core.hold(3)
		tc := h.dial(true)
		tc.auth(h.token("u1", time.Hour))
		tc.send(1, "77")
		tc.send(2, "77")
		tc.send(3, "88")
		synctest.Wait()
		if got := h.core.startedIDs(); !sameIDs(got, 1, 3) {
			t.Fatalf("started = %v, want 1 and 3", got)
		}
		first <- codes.OK
		if resp := tc.next(); resp.GetId() != 1 {
			t.Fatalf("first response id = %d", resp.GetId())
		}
		synctest.Wait()
		if got := h.core.startedIDs(); !sameIDs(got, 2) {
			t.Fatalf("started after the first send = %v, want 2", got)
		}
		second <- codes.OK
		other <- codes.OK
		tc.next()
		tc.next()
		tc.close()
	})
}

func TestRoomIDsWithLeadingZerosShareOneQueue(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h := newHarness(t, testOptions())
		first := h.core.hold(1)
		tc := h.dial(true)
		tc.auth(h.token("u1", time.Hour))
		tc.send(1, "77")
		tc.send(2, "077")
		synctest.Wait()
		if got := h.core.startedIDs(); !sameIDs(got, 1) {
			t.Fatalf("started = %v, want only 1", got)
		}
		first <- codes.OK
		tc.next()
		if resp := tc.next(); resp.GetId() != 2 {
			t.Fatalf("second response id = %d", resp.GetId())
		}
		tc.close()
	})
}

func TestInvalidRoomIDSkipsTheQueue(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h := newHarness(t, testOptions())
		first := h.core.hold(1)
		tc := h.dial(true)
		tc.auth(h.token("u1", time.Hour))
		tc.send(1, "77")
		tc.send(2, "not-a-room")
		if resp := tc.next(); resp.GetId() != 2 {
			t.Fatalf("response id = %d, want 2 before the held send", resp.GetId())
		}
		first <- codes.OK
		tc.next()
		tc.close()
	})
}

func TestInfrastructureFailureAnswersTheWaitingSendsWithoutCallingCore(t *testing.T) {
	for _, code := range []codes.Code{codes.Unavailable, codes.DeadlineExceeded} {
		t.Run(code.String(), func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				h := newHarness(t, testOptions())
				first := h.core.hold(1)
				tc := h.dial(true)
				tc.auth(h.token("u1", time.Hour))
				tc.send(1, "77")
				tc.send(2, "77")
				tc.send(3, "077")
				synctest.Wait()
				first <- code
				for want := uint64(1); want <= 3; want++ {
					resp := tc.next()
					if resp.GetId() != want || resp.GetCode() != int32(code) {
						t.Fatalf("response = %v, want id %d code %v", resp, want, code)
					}
				}
				if got := h.core.calls.Load(); got != 1 {
					t.Fatalf("core calls = %d, want 1", got)
				}
				tc.send(4, "77")
				if resp := tc.next(); resp.GetId() != 4 || resp.GetCode() != 0 {
					t.Fatalf("send after the sweep = %v", resp)
				}
				tc.close()
			})
		})
	}
}

func TestBusinessFailureDoesNotSweepTheQueue(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h := newHarness(t, testOptions())
		first := h.core.hold(1)
		tc := h.dial(true)
		tc.auth(h.token("u1", time.Hour))
		tc.send(1, "77")
		tc.send(2, "77")
		synctest.Wait()
		first <- codes.PermissionDenied
		if resp := tc.next(); resp.GetCode() != int32(codes.PermissionDenied) {
			t.Fatalf("first response = %v", resp)
		}
		if resp := tc.next(); resp.GetId() != 2 || resp.GetCode() != 0 {
			t.Fatalf("second response = %v", resp)
		}
		tc.close()
	})
}

func TestPanicInACommandClosesOnlyThatConnection(t *testing.T) {
	for name, send := range map[string]func(tc *testClient){
		"send":    func(tc *testClient) { tc.send(1, "77") },
		"history": func(tc *testClient) { tc.history(1) },
	} {
		t.Run(name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				h := newHarness(t, testOptions())
				h.core.panicOn(1)
				broken, healthy := h.dial(true), h.dial(true)
				broken.auth(h.token("u1", time.Hour))
				healthy.auth(h.token("u2", time.Hour))
				send(broken)
				if got := broken.waitClosed(); got != CodeInternalError {
					t.Fatalf("close code = %d, want %d", got, CodeInternalError)
				}
				healthy.history(2)
				if resp := healthy.next(); resp.GetId() != 2 || resp.GetCode() != 0 {
					t.Fatalf("healthy response = %v", resp)
				}
				healthy.close()
				if h.server.commands.count.Load() != 0 {
					t.Fatal("command slot leaked")
				}
			})
		})
	}
}

func TestClosingDropsQueuedSendsAndCancelsTheRunningOne(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h := newHarness(t, testOptions())
		h.core.hold(1)
		h.core.hold(3)
		tc := h.dial(true)
		tc.auth(h.token("u1", time.Hour))
		tc.send(1, "77")
		tc.send(2, "77")
		tc.history(3)
		synctest.Wait()
		tc.close()
		if got := h.core.startedIDs(); !sameIDs(got, 1, 3) {
			t.Fatalf("started = %v, want 1 and 3", got)
		}
		if h.server.commands.count.Load() != 0 {
			t.Fatal("command slots leaked")
		}
	})
}

func sameIDs(got []uint64, want ...uint64) bool {
	slices.Sort(got)
	return slices.Equal(got, want)
}
