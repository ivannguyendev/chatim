package domain_test

import (
	"slices"
	"testing"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
)

var orderBase = time.Date(2026, 10, 20, 0, 0, 0, 0, time.UTC)

func clockAt(hour, minute int) time.Time {
	return orderBase.Add(time.Duration(hour)*time.Hour + time.Duration(minute)*time.Minute)
}

func TestNewRoomSortKeyUsesTheLaterOfLastMessageAndJoin(t *testing.T) {
	cases := []struct {
		name               string
		pinned, last, join time.Time
		want               domain.RoomSortKey
	}{
		{"message after join", time.Time{}, clockAt(10, 5), clockAt(8, 0), domain.RoomSortKey{Room: 1, SortAt: clockAt(10, 5)}},
		{"joined after the last message", time.Time{}, clockAt(8, 0), clockAt(10, 5), domain.RoomSortKey{Room: 1, SortAt: clockAt(10, 5)}},
		{"no message yet", time.Time{}, time.Time{}, clockAt(7, 0), domain.RoomSortKey{Room: 1, SortAt: clockAt(7, 0)}},
		{"pinned", clockAt(8, 0), clockAt(9, 40), clockAt(1, 0), domain.RoomSortKey{Pinned: true, PinnedAt: clockAt(8, 0), Room: 1, SortAt: clockAt(9, 40)}},
	}
	for _, c := range cases {
		if got := domain.NewRoomSortKey(1, c.pinned, c.last, c.join); got != c.want {
			t.Errorf("%s: NewRoomSortKey = %+v, want %+v", c.name, got, c.want)
		}
	}
	ns := domain.NewRoomSortKey(1, clockAt(8, 0).Add(999_999), clockAt(9, 0).Add(1_500_000), time.Time{})
	if want := (domain.RoomSortKey{Pinned: true, PinnedAt: clockAt(8, 0), SortAt: clockAt(9, 0).Add(time.Millisecond), Room: 1}); ns != want {
		t.Fatalf("NewRoomSortKey truncation = %+v, want %+v", ns, want)
	}
}

func TestCompareRoomSortKeysOrdersTheExampleRooms(t *testing.T) {
	r888 := domain.NewRoomSortKey(888, clockAt(8, 0), clockAt(9, 40), clockAt(1, 0))
	r777 := domain.NewRoomSortKey(777, time.Time{}, clockAt(10, 5), clockAt(1, 0))
	r999 := domain.NewRoomSortKey(999, time.Time{}, clockAt(-24, 0), clockAt(-48, 0))
	pinnedLater := domain.NewRoomSortKey(100, clockAt(9, 0), clockAt(-24, 0), clockAt(-48, 0))
	tieHigh := domain.NewRoomSortKey(1001, time.Time{}, clockAt(-24, 0), clockAt(-48, 0))
	pinTieLow := domain.NewRoomSortKey(50, clockAt(9, 0), clockAt(-24, 0), clockAt(-48, 0))
	pinTieSortAt := domain.NewRoomSortKey(60, clockAt(9, 0), clockAt(11, 0), clockAt(-48, 0))
	keys := []domain.RoomSortKey{r999, r777, pinTieLow, tieHigh, r888, pinTieSortAt, pinnedLater}
	slices.SortFunc(keys, domain.CompareRoomSortKeys)
	got := make([]uint64, len(keys))
	for i, k := range keys {
		got[i] = k.Room
	}
	want := []uint64{60, 100, 50, 888, 777, 1001, 999}
	if !slices.Equal(got, want) {
		t.Fatalf("order = %v, want %v", got, want)
	}
}

func TestCompareRoomSortKeysIsAStrictOrder(t *testing.T) {
	a := domain.NewRoomSortKey(5, time.Time{}, clockAt(1, 0), clockAt(0, 0))
	if domain.CompareRoomSortKeys(a, a) != 0 {
		t.Fatal("a key must compare equal to itself")
	}
	b := domain.NewRoomSortKey(4, time.Time{}, clockAt(1, 0), clockAt(0, 0))
	if domain.CompareRoomSortKeys(a, b) >= 0 || domain.CompareRoomSortKeys(b, a) <= 0 {
		t.Fatal("on a tie the higher room id comes first")
	}
}

func TestFolderOf(t *testing.T) {
	if got := domain.FolderOf(domain.RoomPreferences{PinnedAt: clockAt(8, 0)}); got != domain.FolderMain {
		t.Fatalf("not archived = %d, want FolderMain", got)
	}
	if got := domain.FolderOf(domain.RoomPreferences{ArchivedAt: clockAt(8, 0)}); got != domain.FolderArchived {
		t.Fatalf("archived = %d, want FolderArchived", got)
	}
}
