package stateclear_test

import (
	"slices"
	"testing"
	"testing/synctest"
	"time"
)

func TestFlushesTargetsBelowTheBatchSizeAfterTheFlushInterval(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		rg := started(t, defaultCfg)
		rg.add(1, 2, 3)
		synctest.Wait()
		time.Sleep(99 * time.Millisecond)
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

func TestFlushesAFullBatchWithoutWaitingForTheTimer(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		rg := started(t, defaultCfg)
		rg.add(seqRange(1, 501)...)
		synctest.Wait()
		expectSizes(t, rg.st, 500)
		time.Sleep(100 * time.Millisecond)
		synctest.Wait()
		expectSizes(t, rg.st, 500, 1)
		rg.expectCounts(t, 501, 0, 0)
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
		rg := started(t, defaultCfg)
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

func TestEachClearGetsItsOwnOneSecondTimeout(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		rg := started(t, defaultCfg)
		rg.st.set(nil, true)
		rg.add(1)
		start := time.Now()
		time.Sleep(100 * time.Millisecond)
		synctest.Wait()
		expectSizes(t, rg.st, 1)
		time.Sleep(time.Second)
		synctest.Wait()
		rg.expectCounts(t, 0, 0, 1)
		rg.st.set(nil, false)
		rg.add(2)
		time.Sleep(100 * time.Millisecond)
		synctest.Wait()
		expectSizes(t, rg.st, 1, 1)
		rg.expectCounts(t, 1, 0, 1)
		if elapsed := time.Since(start); elapsed != 1200*time.Millisecond {
			t.Fatalf("elapsed %v, want 1.2s", elapsed)
		}
		rg.closeAndWait(t)
	})
}
