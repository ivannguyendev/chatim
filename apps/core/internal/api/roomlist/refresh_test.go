package roomlist_test

import (
	"errors"
	"slices"
	"testing"

	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/pkg/apperr"
)

func TestGetRoomsBuildsKnownRoomsAndListsTheRestAsMissing(t *testing.T) {
	st := seedMinh(t, 45)
	l := newLister(t, st, 2000)
	items, missing, err := l.Get(t.Context(), acme, minh, []uint64{shop, old, unknown, left, foreign, shop, direct})
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	expectItems(t, items,
		wantItem{room: shop, unread: 2, preview: 45},
		wantItem{room: old, preview: 90},
		wantItem{room: direct, preview: 12, peerRead: 10},
	)
	if !slices.Equal(missing, []uint64{unknown, left, foreign}) {
		t.Errorf("missing = %v, want [123 666 4444]", missing)
	}
}

func TestGetRoomsOnAWarmCacheReadsMembersActivityAndHidden(t *testing.T) {
	st := seedMinh(t, 45)
	l := newLister(t, st, 2000)
	list(t, l, acme, domain.FolderMain, nil, 20)
	st.reset()
	items, _, err := l.Get(t.Context(), acme, minh, []uint64{shop, direct})
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	st.expectCalls(t, map[string]int{"MembersByKeys": 1, "RoomActivities": 1, "HiddenInRooms": 1})
	expectItems(t, items, wantItem{room: shop, unread: 2, preview: 45}, wantItem{room: direct, preview: 12, peerRead: 10})
}

func TestGetRoomsCountsAgainAfterAnEditReloadsTheWindow(t *testing.T) {
	st := seedMinh(t, 45)
	l := newLister(t, st, 2000)
	list(t, l, acme, domain.FolderMain, nil, 20)
	if err := st.rooms.TouchActivity(t.Context(), []store.Activity{{Room: shop, Edited: true, At: clock(10, 30)}}); err != nil {
		t.Fatalf("TouchActivity: %v", err)
	}
	st.hide(t, shop, 45)
	st.reset()
	items, _, err := l.Get(t.Context(), acme, minh, []uint64{shop})
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if _, windows := st.snapshot(); !slices.Equal(windows, []windowCall{{room: shop, after: 0}}) {
		t.Errorf("tail scans = %v, want one full reload of room 777", windows)
	}
	expectItems(t, items, wantItem{room: shop, unread: 1, preview: 43})
}

func TestGetRoomsRejectsTooManyOrZeroIDs(t *testing.T) {
	l := newLister(t, newWorld(), 2000)
	for name, ids := range map[string][]uint64{"too many": make([]uint64, 101), "zero": {0}} {
		if name == "too many" {
			for i := range ids {
				ids[i] = uint64(i + 1)
			}
		}
		if _, _, err := l.Get(t.Context(), acme, minh, ids); !errors.Is(err, apperr.ErrInvalidArgument) {
			t.Errorf("%s: Get = %v, want ErrInvalidArgument", name, err)
		}
	}
	items, missing, err := l.Get(t.Context(), acme, minh, nil)
	if err != nil || len(items) != 0 || len(missing) != 0 {
		t.Errorf("Get(nil) = %v, %v, %v, want nothing", items, missing, err)
	}
}
