package roomset

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestJobsCoverEverySeqOnce(t *testing.T) {
	ids := sequence(37)
	for _, order := range []string{"room", "interleaved"} {
		jobs, err := Jobs(order, ids, 23, 50)
		if err != nil {
			t.Fatalf("%s: %v", order, err)
		}
		seen := map[[2]uint64]int{}
		for _, j := range jobs {
			if size := len(j.Rooms) * int(j.To-j.From+1); size > 50 {
				t.Fatalf("%s: job %+v holds %d messages, want <= 50", order, j, size)
			}
			for _, r := range j.Rooms {
				for s := j.From; s <= j.To; s++ {
					seen[[2]uint64{r, s}]++
				}
			}
		}
		if len(seen) != 37*23 {
			t.Fatalf("%s: covered %d (room, seq) pairs, want %d", order, len(seen), 37*23)
		}
		for k, n := range seen {
			if n != 1 || k[1] < 1 || k[1] > 23 {
				t.Fatalf("%s: pair %v seen %d times", order, k, n)
			}
		}
	}
}

func TestInterleavedJobsMixRoomsInSeqWindows(t *testing.T) {
	jobs, err := Jobs("interleaved", sequence(1000), 100, 1000)
	if err != nil {
		t.Fatal(err)
	}
	first := jobs[0]
	if len(first.Rooms) != 100 || first.From != 1 || first.To != 10 {
		t.Fatalf("first job = %d rooms, seq %d..%d; want 100 rooms, seq 1..10", len(first.Rooms), first.From, first.To)
	}
	for i := 1; i < len(jobs); i++ {
		if jobs[i].From < jobs[i-1].From {
			t.Fatalf("job %d starts at seq %d before previous job at %d", i, jobs[i].From, jobs[i-1].From)
		}
	}
}

func TestRoomOrderKeepsOneRoomPerJob(t *testing.T) {
	jobs, err := Jobs("room", []uint64{7, 8}, 30, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(jobs) != 6 || len(jobs[0].Rooms) != 1 || jobs[0].Rooms[0] != 7 || jobs[0].From != 1 || jobs[0].To != 10 {
		t.Fatalf("jobs = %+v", jobs)
	}
}

func TestJobsRejectBadInput(t *testing.T) {
	if _, err := Jobs("random", sequence(3), 10, 10); !errors.Is(err, ErrOrder) {
		t.Errorf("unknown order err = %v", err)
	}
	for _, c := range []struct{ ids, perRoom, batch int }{{0, 10, 10}, {3, 0, 10}, {3, 10, 0}} {
		if _, err := Jobs("room", sequence(c.ids), c.perRoom, c.batch); err == nil {
			t.Errorf("Jobs(%+v) succeeded, want error", c)
		}
	}
}

func TestWriteReadRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rooms.txt")
	if err := Write(path, []uint64{5, 9}, 7); err != nil {
		t.Fatal(err)
	}
	rooms, err := Read(path)
	if err != nil || len(rooms) != 2 || rooms[0] != (Room{ID: 5, PerRoom: 7}) || rooms[1] != (Room{ID: 9, PerRoom: 7}) {
		t.Fatalf("Read = %+v, %v", rooms, err)
	}
	if _, err := Read(filepath.Join(t.TempDir(), "missing.txt")); err == nil {
		t.Fatal("Read of a missing file succeeded")
	}
	empty := filepath.Join(t.TempDir(), "empty.txt")
	if err := os.WriteFile(empty, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Read(empty); err == nil {
		t.Fatal("Read of an empty file succeeded")
	}
}

func sequence(n int) []uint64 {
	ids := make([]uint64, n)
	for i := range ids {
		ids[i] = uint64(i + 1)
	}
	return ids
}
