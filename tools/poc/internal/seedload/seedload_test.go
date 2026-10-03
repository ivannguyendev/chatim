package seedload

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/ivannguyendev/chatim/tools/poc/internal/roomset"
)

func TestRunInsertsEveryRowOnce(t *testing.T) {
	jobs, err := roomset.Jobs("interleaved", []uint64{1, 2, 3, 4}, 25, 20)
	if err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	seen := map[[2]uint64]int{}
	n, err := Run(context.Background(), jobs, 3, []string{"hello"}, func(_ context.Context, rows []Row) error {
		mu.Lock()
		defer mu.Unlock()
		for _, r := range rows {
			if r.Text != "hello" || r.From == "" {
				t.Errorf("row %+v has unexpected text or sender", r)
			}
			seen[[2]uint64{r.Room, r.Seq}]++
		}
		return nil
	})
	if err != nil || n != 100 || len(seen) != 100 {
		t.Fatalf("Run = %d, %v; distinct rows %d; want 100", n, err, len(seen))
	}
	for k, c := range seen {
		if c != 1 {
			t.Fatalf("row %v inserted %d times", k, c)
		}
	}
}

func TestRunStopsOnInsertError(t *testing.T) {
	jobs, err := roomset.Jobs("room", []uint64{1, 2, 3, 4, 5, 6}, 10, 5)
	if err != nil {
		t.Fatal(err)
	}
	var calls atomic.Int64
	boom := errors.New("boom")
	_, err = Run(context.Background(), jobs, 2, nil, func(context.Context, []Row) error {
		if calls.Add(1) == 3 {
			return boom
		}
		return nil
	})
	if !errors.Is(err, boom) {
		t.Fatalf("Run err = %v, want boom", err)
	}
}
