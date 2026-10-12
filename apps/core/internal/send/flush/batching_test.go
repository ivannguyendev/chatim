package flush_test

import (
	"strconv"
	"testing"
	"testing/synctest"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/send/flush"
)

func TestFlushesWhenBatchIsFullWithoutSplittingGroups(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		fs, rec := &fakeStore{}, newRecorder()
		h := start(t, fs, flush.Config{Shards: 1, Window: time.Hour, MaxBatch: 4, QueueSize: 16})
		h.submit(t, rec.group("a", 1, 1, 2))
		h.submit(t, rec.group("b", 2, 1, 2))
		synctest.Wait()
		assertSizes(t, fs, 4)

		h.submit(t, rec.group("c", 1, 3, 4, 5))
		h.submit(t, rec.group("d", 2, 3, 4))
		synctest.Wait()
		assertSizes(t, fs, 4, 3)

		h.submit(t, rec.group("e", 3, 1, 2, 3, 4, 5))
		synctest.Wait()
		assertSizes(t, fs, 4, 3, 2, 5)
		if elapsed := time.Since(fs.all()[0].at); elapsed != 0 {
			t.Errorf("full batches waited %v for the window", elapsed)
		}
		h.stop(t)
		rec.assertCalledOnce(t, "a", "b", "c", "d", "e")
	})
}

func TestFlushesWhenWindowElapsesSinceFirstGroup(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		fs, rec := &fakeStore{}, newRecorder()
		h := start(t, fs, flush.Config{Shards: 1, Window: 10 * time.Millisecond, MaxBatch: 256, QueueSize: 16})
		begin := time.Now()
		h.submit(t, rec.group("a", 1, 1))
		time.Sleep(6 * time.Millisecond)
		h.submit(t, rec.group("b", 2, 1, 2))
		time.Sleep(3 * time.Millisecond)
		synctest.Wait()
		assertSizes(t, fs)

		time.Sleep(2 * time.Millisecond)
		synctest.Wait()
		assertSizes(t, fs, 3)
		if at := fs.all()[0].at.Sub(begin); at != 10*time.Millisecond {
			t.Errorf("first batch sent at %v, want 10ms", at)
		}

		h.submit(t, rec.group("c", 1, 2))
		time.Sleep(time.Hour)
		h.stop(t)
		assertSizes(t, fs, 3, 1)
		if at := fs.all()[1].at.Sub(begin); at != 21*time.Millisecond {
			t.Errorf("second batch sent at %v, want 21ms", at)
		}
		rec.assertCalledOnce(t, "a", "b", "c")
	})
}

func TestResultsReachTheirGroupsInOrder(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		fs, rec := &fakeStore{respond: bySeq}, newRecorder()
		h := start(t, fs, flush.Config{Shards: 1, Window: time.Millisecond, MaxBatch: 256, QueueSize: 16})
		groups := []struct {
			id   string
			room uint64
			seqs []uint64
		}{
			{"a", 1, []uint64{1, 2, 3}},
			{"b", 2, []uint64{4}},
			{"c", 3, []uint64{5, 6, 7, 8}},
			{"d", 1, []uint64{9, 10}},
		}
		for _, g := range groups {
			h.submit(t, rec.group(g.id, g.room, g.seqs...))
		}
		h.stop(t)
		assertSizes(t, fs, 10)

		for _, g := range groups {
			got := rec.results(t, g.id)
			if len(got) != len(g.seqs) {
				t.Errorf("group %s: %d results, want %d", g.id, len(got), len(g.seqs))
				continue
			}
			for i, seq := range g.seqs {
				if want := expected(g.room, seq); !sameResult(got[i], want) {
					t.Errorf("group %s result %d = %+v, want %+v", g.id, i, got[i], want)
				}
			}
		}
	})
}

func TestRoomOrderHoldsAcrossBatchesAndShards(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		const shards = 3
		fs, rec := &fakeStore{shards: shards}, newRecorder()
		h := start(t, fs, flush.Config{Shards: shards, Window: time.Millisecond, MaxBatch: 3, QueueSize: 64})
		rooms := append(roomsIn(0, shards, 2), roomsIn(1, shards, 2)...)
		rooms = append(rooms, roomsIn(2, shards, 1)...)
		next := map[uint64]uint64{}
		var ids []string
		for i := range 40 {
			room := rooms[i%len(rooms)]
			seqs := make([]uint64, i%2+1)
			for j := range seqs {
				next[room]++
				seqs[j] = next[room]
			}
			ids = append(ids, strconv.Itoa(i))
			h.submit(t, rec.group(ids[i], room, seqs...))
			if i%7 == 6 {
				time.Sleep(time.Millisecond)
			}
		}
		h.stop(t)
		rec.assertCalledOnce(t, ids...)
		assertRoomOrder(t, fs.all(), shards, 3, next)
		for shard, peak := range fs.peaks() {
			if peak != 1 {
				t.Errorf("shard %d had %d inserts in flight, want 1", shard, peak)
			}
		}
	})
}

func assertRoomOrder(t *testing.T, batches []batch, shards, maxBatch int, last map[uint64]uint64) {
	t.Helper()
	seen, mixed := map[uint64]uint64{}, false
	for i, b := range batches {
		if len(b.msgs) > maxBatch {
			t.Errorf("batch %d has %d messages, max %d", i, len(b.msgs), maxBatch)
		}
		for _, m := range b.msgs {
			if shardOf(m.Room, shards) != shardOf(b.msgs[0].Room, shards) {
				t.Errorf("batch %d mixes shards", i)
			}
			mixed = mixed || m.Room != b.msgs[0].Room
			if m.Seq != seen[m.Room]+1 {
				t.Errorf("room %d: seq %d after %d", m.Room, m.Seq, seen[m.Room])
			}
			seen[m.Room] = m.Seq
		}
	}
	if !mixed {
		t.Error("no batch carried more than one room")
	}
	for room, n := range last {
		if seen[room] != n {
			t.Errorf("room %d: stored up to seq %d, want %d", room, seen[room], n)
		}
	}
}
