package domain_test

import (
	"errors"
	"testing"

	"github.com/ivannguyendev/chatim/modules/media/internal/domain"
)

const (
	mib = int64(1) << 20
	gib = int64(1) << 30
)

func limits() domain.Limits {
	return domain.Limits{MinPartBytes: 16 * mib, MaxPartBytes: 128 * mib, MaxParts: 2000, MaxParallel: 4, MaxFileBytes: 2000 * 128 * mib}
}

func TestPlanPartsKeepsFourStreamsBusy(t *testing.T) {
	cases := []struct {
		name          string
		size, speed   int64
		partSize      int64
		count         int
		lastPartBytes int64
	}{
		{"one byte", 1, 0, 16 * mib, 1, 1},
		{"40 MiB is too small for four parts", 40 * mib, 0, 16 * mib, 3, 8 * mib},
		{"100 MiB is one round", 100 * mib, 0, 25 * mib, 4, 25 * mib},
		{"2 GiB", 2 * gib, 0, 32 * mib, 64, 32 * mib},
		{"4 GiB without a speed", 4 * gib, 0, 32 * mib, 128, 32 * mib},
		{"4 GiB at 20 MB/s per stream", 4 * gib, 20_000_000, 128 * mib, 32, 128 * mib},
		{"100 GiB grows parts to stay under 2000", 100 * gib, 0, 52 * mib, 1970, 100*gib - 1969*52*mib},
		{"250 GiB is the ceiling", 2000 * 128 * mib, 0, 128 * mib, 2000, 128 * mib},
		{"slow phone stays at the minimum", 1 * gib, 1_000_000, 16 * mib, 64, 16 * mib},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			partSize, parts, err := domain.PlanParts(c.size, c.speed, limits())
			if err != nil {
				t.Fatal(err)
			}
			if partSize != c.partSize || len(parts) != c.count || parts[len(parts)-1].Size != c.lastPartBytes {
				t.Fatalf("part size %d, parts %d, last %d; want %d, %d, %d", partSize, len(parts), parts[len(parts)-1].Size, c.partSize, c.count, c.lastPartBytes)
			}
		})
	}
}

func TestPlanPartsRefusesAFileOverTheCeiling(t *testing.T) {
	_, _, err := domain.PlanParts(2000*128*mib+1, 0, limits())
	if !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("err = %v", err)
	}
	if _, _, err := domain.PlanParts(0, 0, limits()); !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("zero size err = %v", err)
	}
}

func FuzzPlanParts(f *testing.F) {
	f.Add(int64(1), int64(0))
	f.Add(40*mib, int64(0))
	f.Add(100*gib, int64(3_000_000))
	f.Add(2000*128*mib, int64(1<<40))
	f.Fuzz(func(t *testing.T, size, speed int64) {
		l := limits()
		if size <= 0 || size > l.MaxFileBytes || speed < 0 {
			return
		}
		partSize, parts, err := domain.PlanParts(size, speed, l)
		if err != nil {
			t.Fatal(err)
		}
		var total int64
		for i, p := range parts {
			if p.Number != int32(i+1) {
				t.Fatalf("part %d numbered %d", i, p.Number)
			}
			last := i == len(parts)-1
			if !last && (p.Size != partSize || p.Size < l.MinPartBytes || p.Size > l.MaxPartBytes) {
				t.Fatalf("part %d size %d with part size %d", i+1, p.Size, partSize)
			}
			if p.Size <= 0 || p.Size > partSize {
				t.Fatalf("part %d size %d", i+1, p.Size)
			}
			total += p.Size
		}
		if total != size || len(parts) > l.MaxParts {
			t.Fatalf("total %d of %d in %d parts", total, size, len(parts))
		}
		if size >= int64(l.MaxParallel)*l.MinPartBytes && len(parts) < l.MaxParallel {
			t.Fatalf("size %d speed %d: %d parts leave streams idle", size, speed, len(parts))
		}
	})
}
