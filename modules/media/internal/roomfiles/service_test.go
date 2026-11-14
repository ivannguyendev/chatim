package roomfiles_test

import (
	"errors"
	"fmt"
	"reflect"
	"testing"

	"github.com/ivannguyendev/chatim/modules/media/internal/domain"
	"github.com/ivannguyendev/chatim/modules/media/internal/roomfiles"
	"github.com/ivannguyendev/chatim/pkg/core/apperr"
)

func TestAnOutsiderIsRefusedBeforeAnyPairIsRead(t *testing.T) {
	r := newRig(t)
	r.sent(t, 1, 1, true, png(photo))
	r.chat.accessErr = fmt.Errorf("not a member: %w", apperr.ErrPermissionDenied)
	_, err := r.svc.List(t.Context(), an, media(10))
	if !errors.Is(err, apperr.ErrPermissionDenied) || len(r.files.queries) != 0 || len(r.chat.asked) != 0 {
		t.Fatalf("err = %v, queries %d, chat asked %d", err, len(r.files.queries), len(r.chat.asked))
	}
}

func TestBadInputIsInvalidBeforeAskingChat(t *testing.T) {
	r := newRig(t)
	for name, in := range map[string]roomfiles.Input{
		"page size 0":   media(0),
		"page size 101": media(101),
		"no kind":       {Room: room, PageSize: 10},
		"unknown kind":  {Room: room, Kind: "image", PageSize: 10},
		"broken cursor": {Room: room, Kind: domain.RoomFileMedia, PageSize: 10, Before: "not a cursor!"},
	} {
		if _, err := r.svc.List(t.Context(), an, in); !errors.Is(err, apperr.ErrInvalidArgument) {
			t.Fatalf("%s: err = %v", name, err)
		}
	}
	if r.chat.accessCalls != 0 || len(r.files.queries) != 0 {
		t.Fatalf("asked chat %d times and the store %d times", r.chat.accessCalls, len(r.files.queries))
	}
}

func TestMediaNotReadyIsUnavailable(t *testing.T) {
	r := newRig(t)
	r.ready = false
	if _, err := r.svc.List(t.Context(), an, media(10)); !errors.Is(err, apperr.ErrUnavailable) {
		t.Fatalf("err = %v", err)
	}
}

func TestVisibleFilesComeNewestFirstWithURLsFromTheirBucket(t *testing.T) {
	r := newRig(t)
	cleared := sentAt(0)
	r.chat.access = domain.RoomAccess{Room: room, ClearedAt: cleared}
	r.sent(t, 1, 1, true, png(clip))
	r.sent(t, 2, 2, false, png(other))
	r.sent(t, 3, 3, true, png(photo), pdf(other+"x"))
	page := r.list(t, roomfiles.Input{Room: "077", Kind: domain.RoomFileMedia, PageSize: 10})
	want := []roomfiles.File{
		{Ref: ref(3), Sender: "binh", SentAt: sentAt(3), File: png(photo), URL: "https://s3/completed/" + photo, ThumbnailURL: "https://s3/completed/" + photo + ".thumb"},
		{Ref: ref(1), Sender: "binh", SentAt: sentAt(1), File: png(clip), URL: "https://s3/processing/" + clip, ThumbnailURL: "https://s3/processing/" + clip + ".thumb"},
	}
	if !reflect.DeepEqual(page.Files, want) || page.NextBefore != "" || !page.ExpiresAt.Equal(now.Add(ttl)) {
		t.Fatalf("page = %+v\nwant %+v", page, want)
	}
	q := r.files.queries[0]
	if q.Tenant != an.Tenant || q.Room != room || q.Kind != domain.RoomFileMedia || !q.ClearedAt.Equal(cleared) || q.Limit != 10 {
		t.Fatalf("store asked %+v", q)
	}
	files := r.list(t, roomfiles.Input{Room: room, Kind: domain.RoomFileOther, PageSize: 10})
	if len(files.Files) != 1 || files.Files[0].File.Key != other+"x" || files.Files[0].ThumbnailURL != "" {
		t.Fatalf("file tab = %+v", files.Files)
	}
}

func TestAShortPageKeepsTheCursorWhenTheStorePageWasFull(t *testing.T) {
	r := newRig(t)
	r.sent(t, 1, 1, true, png(clip))
	r.sent(t, 2, 2, false, png(other))
	r.sent(t, 3, 3, true, png(photo))
	first := r.list(t, media(2))
	if len(first.Files) != 1 || first.Files[0].Ref != ref(3) || first.NextBefore == "" {
		t.Fatalf("first page = %+v", first)
	}
	in := media(2)
	in.Before = first.NextBefore
	second := r.list(t, in)
	if len(second.Files) != 1 || second.Files[0].Ref != ref(1) || second.NextBefore != "" {
		t.Fatalf("second page = %+v", second)
	}
}

func TestACursorOlderThanTheClearMarkEndsTheListWithoutAStoreRead(t *testing.T) {
	r := newRig(t)
	r.sent(t, 1, 1, true, png(clip))
	r.sent(t, 3, 3, true, png(photo))
	first := r.list(t, media(1))
	r.chat.access.ClearedAt = sentAt(5)
	in := media(1)
	in.Before = first.NextBefore
	page := r.list(t, in)
	if len(page.Files) != 0 || page.NextBefore != "" || len(r.files.queries) != 1 {
		t.Fatalf("page = %+v after %d store reads", page, len(r.files.queries))
	}
}

func TestAPairWhoseFileIsNotInTheMessageIsDropped(t *testing.T) {
	r := newRig(t)
	r.sent(t, 3, 3, true, png(photo))
	r.chat.messages[ref(3).Key()] = domain.MessageFiles{Ref: ref(3), Sender: "binh", SentAt: sentAt(3), Files: []domain.File{png(clip)}}
	if page := r.list(t, media(10)); len(page.Files) != 0 {
		t.Fatalf("page = %+v", page.Files)
	}
}

func TestAForwardListsTheFileAtTheForwardTime(t *testing.T) {
	r := newRig(t)
	r.sent(t, 9, 30, true, png(photo))
	page := r.list(t, media(10))
	if len(page.Files) != 1 || page.Files[0].Ref != ref(9) || !page.Files[0].SentAt.Equal(sentAt(30)) {
		t.Fatalf("page = %+v", page.Files)
	}
}
