package publish_test

import (
	"testing"

	"github.com/alicebob/miniredis/v2"

	"github.com/ivannguyendev/chatim/apps/core/internal/publish"
)

func TestBaseStartsFromThePresentRedisWatermark(t *testing.T) {
	mr := miniredis.RunT(t)
	mustSet(t, mr, publish.WatermarkKey(roomA), "10")
	mustSet(t, mr, publish.WatermarkKey(roomB), "10")
	rg := newRig(t, fastSetup, mr).start(t)

	rg.enqueue(t, roomA, 11, 12)
	waitWatermark(t, mr, roomA, 12)

	rg.enqueue(t, roomB, 12)
	eventually(t, "room B pts 12 stored", func() bool { return len(rg.js.Stored()) == 3 })
	holdsWatermark(t, mr, roomB, 10)
	rg.enqueue(t, roomB, 11)
	waitWatermark(t, mr, roomB, 12)
}

func TestBaseIsLowestHandedMinusOneWhenRedisHasNone(t *testing.T) {
	rg := started(t, fastSetup)
	rg.enqueue(t, roomA, 21, 22)
	waitWatermark(t, rg.mr, roomA, 22)

	rg.js.NackWhen(nackIDs("202-31"))
	rg.enqueue(t, roomB, 31, 32)
	waitWatermark(t, rg.mr, roomB, 30)
	eventually(t, "pts 31 abandoned", func() bool { return rg.sink.Count(abandonedMsg) == 1 })
	holdsWatermark(t, rg.mr, roomB, 30)
	rg.js.NackWhen(nil)
	rg.enqueue(t, roomB, 31)
	waitWatermark(t, rg.mr, roomB, 32)
}

func TestMalformedRedisWatermarkIsTreatedAsAbsent(t *testing.T) {
	mr := miniredis.RunT(t)
	mustSet(t, mr, publish.WatermarkKey(roomA), "007")
	rg := newRig(t, fastSetup, mr).start(t)
	rg.enqueue(t, roomA, 41)
	waitWatermark(t, mr, roomA, 41)
}

func TestWatermarkTTLIsRefreshedWhenItAdvances(t *testing.T) {
	cfg := fastSetup
	cfg.WatermarkTTL = publish.DefaultWatermarkTTL
	rg := started(t, cfg)
	rg.enqueue(t, roomA, 1)
	waitWatermark(t, rg.mr, roomA, 1)
	rg.mr.FastForward(publish.DefaultWatermarkTTL / 2)
	rg.enqueue(t, roomA, 2)
	waitWatermark(t, rg.mr, roomA, 2)
	if ttl := rg.mr.TTL(publish.WatermarkKey(roomA)); ttl != publish.DefaultWatermarkTTL {
		t.Fatalf("watermark ttl = %v after an advance, want %v", ttl, publish.DefaultWatermarkTTL)
	}
}

func TestRedisOutageStallsWatermarksButNotPublishing(t *testing.T) {
	rg := started(t, fastSetup)
	rg.mr.Close()
	rg.enqueue(t, roomA, 1, 2, 3)
	eventually(t, "events published during the outage", func() bool { return len(rg.js.Stored()) == 3 })
	eventually(t, "degraded logged", func() bool { return rg.sink.Count(degradedMsg) == 1 })
	if err := rg.mr.Restart(); err != nil {
		t.Fatalf("Restart: %v", err)
	}
	waitWatermark(t, rg.mr, roomA, 3)
	if n := rg.sink.Count(degradedMsg); n != 1 {
		t.Fatalf("logged degraded %d times during one outage, want 1", n)
	}
}

func mustSet(t *testing.T, mr *miniredis.Miniredis, key, value string) {
	t.Helper()
	if err := mr.Set(key, value); err != nil {
		t.Fatalf("Set(%s): %v", key, err)
	}
}
