package feed

import (
	"slices"
	"testing"
	"testing/synctest"
	"time"
)

func TestADeliveryWhileTheWatchdogReadsTheStreamEndIsNotASequenceFall(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		b := newFakeBroker()
		b.lastSeq["CHATIM_EVT"] = 10
		rec := newRecorder()
		f := startedFeed(t, rec, b)
		evt := b.consumer("CHATIM_EVT")
		next := uint64(11)
		b.set(func(b *fakeBroker) {
			b.onPosition = func(stream string) {
				if stream == "CHATIM_EVT" {
					seq := next
					b.set(func(b *fakeBroker) { b.lastSeq["CHATIM_EVT"] = seq })
					evt.handler(eventMsg(roomSubject, seq, time.Now()))
					next++
				}
			}
		})
		for range 3 {
			time.Sleep(watchInterval)
			synctest.Wait()
		}
		if jumps, _ := rec.history(); len(jumps) != 0 || len(b.consumersOf("CHATIM_EVT")) != 1 {
			t.Fatalf("deliveries during the stream end read caused jumps %v", jumps)
		}
		b.set(func(b *fakeBroker) { b.onPosition, b.lastSeq["CHATIM_EVT"] = nil, 2 })
		time.Sleep(watchInterval)
		synctest.Wait()
		stopFeed(t, f)
		if jumps, _ := rec.history(); !slices.Equal(jumps, []Jump{{Reason: JumpConsumerLost, Seq: 2}}) || !slices.Equal(rec.seqs("5"), []uint64{11, 12, 13}) {
			t.Fatalf("jumps %v delivered %v", jumps, rec.seqs("5"))
		}
	})
}

func TestConsumerBehindSecondsCountsFromTheFirstCheckThatSawTheGap(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		b := newFakeBroker()
		rec := newRecorder()
		f := startedFeed(t, rec, b)
		if got := readMetric(t, f, "consumer_behind_seconds", "CHATIM_EVT"); got != 0 {
			t.Fatalf("behind %v while caught up", got)
		}
		b.set(func(b *fakeBroker) { b.lastSeq["CHATIM_EVT"] = 1 })
		time.Sleep(watchInterval)
		synctest.Wait()
		time.Sleep(10 * time.Second)
		if got := readMetric(t, f, "consumer_behind_seconds", "CHATIM_EVT"); got != 10 {
			t.Fatalf("behind %v, want 10", got)
		}
		b.consumer("CHATIM_EVT").handler(eventMsg(roomSubject, 1, time.Now()))
		if got := readMetric(t, f, "consumer_behind_seconds", "CHATIM_EVT"); got != 0 {
			t.Fatalf("behind %v after the delivery", got)
		}
		stopFeed(t, f)
	})
}
