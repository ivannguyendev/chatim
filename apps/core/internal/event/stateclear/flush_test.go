package stateclear_test

import (
	"slices"
	"testing"
	"testing/synctest"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/event/stateclear"
)

func TestFlushesTargetsBelowTheBatchSizeTwoSecondsAfterTheFirst(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		if stateclear.DefaultFlush != 2*time.Second {
			t.Fatalf("default flush %v, want 2s", stateclear.DefaultFlush)
		}
		rg := started(t, defaultCfg)
		rg.add(1, 2)
		time.Sleep(time.Second)
		rg.add(3)
		synctest.Wait()
		time.Sleep(time.Second - time.Millisecond)
		synctest.Wait()
		expectSizes(t, rg.st)
		time.Sleep(time.Millisecond)
		synctest.Wait()
		expectSizes(t, rg.st, 3)
		if got := rg.st.seqs(0); !slices.Equal(got, []uint64{1, 2, 3}) {
			t.Fatalf("cleared seqs = %v, want [1 2 3]", got)
		}
		rg.expectCounts(t, 3, 0, 0)
		rg.closeAndWait(t)
		expectSizes(t, rg.st, 3)
	})
}

func TestFlushesAFullBatchOf16384WithoutWaitingForTheTimer(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		if stateclear.DefaultBatch != 16384 {
			t.Fatalf("default batch %d, want 16384", stateclear.DefaultBatch)
		}
		rg := started(t, defaultCfg)
		rg.add(seqRange(1, stateclear.DefaultBatch+1)...)
		synctest.Wait()
		expectSizes(t, rg.st, stateclear.DefaultBatch)
		time.Sleep(stateclear.DefaultFlush - time.Millisecond)
		synctest.Wait()
		expectSizes(t, rg.st, stateclear.DefaultBatch)
		time.Sleep(time.Millisecond)
		synctest.Wait()
		expectSizes(t, rg.st, stateclear.DefaultBatch, 1)
		rg.expectCounts(t, stateclear.DefaultBatch+1, 0, 0)
		rg.closeAndWait(t)
	})
}

func TestStaysIdleWithoutTargets(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		rg := started(t, defaultCfg)
		time.Sleep(10 * time.Second)
		synctest.Wait()
		expectSizes(t, rg.st)
		rg.closeAndWait(t)
		expectSizes(t, rg.st)
		rg.expectCounts(t, 0, 0, 0)
	})
}

func TestFailingClearIsCountedLoggedOncePerSecondAndNeverRetried(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		rg := started(t, fastFlush)
		rg.st.set(errStore, false)
		rg.add(1, 2)
		time.Sleep(100 * time.Millisecond)
		synctest.Wait()
		rg.add(3)
		time.Sleep(100 * time.Millisecond)
		synctest.Wait()
		expectSizes(t, rg.st, 2, 1)
		if got := rg.st.seqs(1); !slices.Equal(got, []uint64{3}) {
			t.Fatalf("second Clear seqs = %v, want [3] only", got)
		}
		rg.expectCounts(t, 0, 0, 3)
		if n := rg.sink.Count(clearFailedMsg); n != 1 {
			t.Fatalf("logged %d clear failures within one second, want 1", n)
		}
		time.Sleep(time.Second)
		rg.add(4)
		time.Sleep(100 * time.Millisecond)
		synctest.Wait()
		if n := rg.sink.Count(clearFailedMsg); n != 2 {
			t.Fatalf("logged %d clear failures after a second, want 2", n)
		}
		rg.st.set(nil, false)
		rg.add(5)
		time.Sleep(100 * time.Millisecond)
		synctest.Wait()
		expectSizes(t, rg.st, 2, 1, 1, 1)
		rg.expectCounts(t, 1, 0, 4)
		rg.closeAndWait(t)
	})
}

func TestEachClearGetsItsOwnTimeout(t *testing.T) {
	for name, tc := range map[string]struct {
		cfg     stateclear.Config
		timeout time.Duration
	}{
		"default five seconds": {fastFlush, 5 * time.Second},
		"configured":           {stateclear.Config{Flush: 100 * time.Millisecond, Timeout: 2 * time.Second}, 2 * time.Second},
	} {
		t.Run(name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				rg := started(t, tc.cfg)
				rg.st.set(nil, true)
				rg.add(1)
				start := time.Now()
				time.Sleep(100 * time.Millisecond)
				synctest.Wait()
				expectSizes(t, rg.st, 1)
				time.Sleep(tc.timeout - time.Millisecond)
				synctest.Wait()
				rg.expectCounts(t, 0, 0, 0)
				time.Sleep(time.Millisecond)
				synctest.Wait()
				rg.expectCounts(t, 0, 0, 1)
				rg.st.set(nil, false)
				rg.add(2)
				time.Sleep(100 * time.Millisecond)
				synctest.Wait()
				expectSizes(t, rg.st, 1, 1)
				rg.expectCounts(t, 1, 0, 1)
				if elapsed := time.Since(start); elapsed != tc.timeout+200*time.Millisecond {
					t.Fatalf("elapsed %v, want %v", elapsed, tc.timeout+200*time.Millisecond)
				}
				rg.closeAndWait(t)
			})
		})
	}
}
