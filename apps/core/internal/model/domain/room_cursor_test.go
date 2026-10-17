package domain_test

import (
	"encoding/base64"
	"encoding/binary"
	"math"
	"testing"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
)

func TestRoomCursorRoundTrips(t *testing.T) {
	keys := []struct {
		folder domain.RoomFolder
		key    domain.RoomSortKey
	}{
		{domain.FolderMain, domain.NewRoomSortKey(888, clockAt(8, 0), clockAt(9, 40), clockAt(1, 0))},
		{domain.FolderMain, domain.NewRoomSortKey(777, time.Time{}, clockAt(10, 5), clockAt(1, 0))},
		{domain.FolderArchived, domain.NewRoomSortKey(555, time.Time{}, time.Time{}, clockAt(-200, 0))},
		{domain.FolderArchived, domain.NewRoomSortKey(math.MaxInt64, clockAt(3, 0), time.Time{}, time.Time{})},
	}
	for _, c := range keys {
		s := domain.EncodeRoomCursor(c.folder, c.key)
		got, err := domain.DecodeRoomCursor(s, c.folder)
		if err != nil || got != c.key {
			t.Fatalf("DecodeRoomCursor(%q) = %+v, %v; want %+v", s, got, err, c.key)
		}
		if _, err := base64.RawURLEncoding.DecodeString(s); err != nil {
			t.Fatalf("cursor %q is not base64url: %v", s, err)
		}
	}
}

func TestRoomCursorRejectsAnotherFolder(t *testing.T) {
	s := domain.EncodeRoomCursor(domain.FolderMain, domain.NewRoomSortKey(777, time.Time{}, clockAt(10, 5), clockAt(1, 0)))
	_, err := domain.DecodeRoomCursor(s, domain.FolderArchived)
	assertInvalid(t, err, "next")
}

func TestRoomCursorRejectsBrokenInput(t *testing.T) {
	good := domain.EncodeRoomCursor(domain.FolderMain, domain.NewRoomSortKey(777, clockAt(8, 0), clockAt(10, 5), clockAt(1, 0)))
	raw, _ := base64.RawURLEncoding.DecodeString(good)
	mutate := func(edit func([]byte) []byte) string {
		return base64.RawURLEncoding.EncodeToString(edit(append([]byte(nil), raw...)))
	}
	cases := map[string]string{
		"empty":                "",
		"not base64":           "!!!",
		"padded":               good + "==",
		"short":                mutate(func(b []byte) []byte { return b[:25] }),
		"long":                 mutate(func(b []byte) []byte { return append(b, 0) }),
		"unknown folder":       mutate(func(b []byte) []byte { b[0] = 9; return b }),
		"pinned flag 2":        mutate(func(b []byte) []byte { b[1] = 2; return b }),
		"pinned without time":  mutate(func(b []byte) []byte { binary.BigEndian.PutUint64(b[2:10], 0); return b }),
		"time without pinned":  mutate(func(b []byte) []byte { b[1] = 0; return b }),
		"negative pinned time": mutate(func(b []byte) []byte { binary.BigEndian.PutUint64(b[2:10], math.MaxUint64); return b }),
		"negative sort time":   mutate(func(b []byte) []byte { binary.BigEndian.PutUint64(b[10:18], math.MaxUint64); return b }),
		"room zero":            mutate(func(b []byte) []byte { binary.BigEndian.PutUint64(b[18:], 0); return b }),
	}
	for name, s := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := domain.DecodeRoomCursor(s, domain.FolderMain)
			assertInvalid(t, err, "next")
		})
	}
}
