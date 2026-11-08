package attach_test

import (
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/ivannguyendev/chatim/modules/media/internal/attach"
	"github.com/ivannguyendev/chatim/modules/media/internal/domain"
	"github.com/ivannguyendev/chatim/pkg/core/apperr"
)

func TestNewNeedsEveryPort(t *testing.T) {
	if _, err := attach.New(attach.Config{}, attach.Deps{}); !errors.Is(err, apperr.ErrInvalidArgument) {
		t.Fatalf("err = %v", err)
	}
}

func TestAMessageWithoutFilesCostsNoRead(t *testing.T) {
	r := newRig(t, true)
	if err := r.svc.Sync(t.Context(), attach.Created, attach.Event{Tenant: tenant, Ref: ref("7", 1)}); err != nil {
		t.Fatal(err)
	}
	if r.chat.calls != 0 || len(r.storeCalls()) != 0 || r.objects.stats+len(r.objects.copies) != 0 {
		t.Fatalf("chat %d store %v objects %+v", r.chat.calls, r.storeCalls(), r.objects)
	}
}

func TestADeleteGoesStraightToThePairsWithoutAskingChat(t *testing.T) {
	r := newRig(t, false)
	if err := r.removed(t, ref("7", 1)); err != nil {
		t.Fatal(err)
	}
	if r.chat.calls != 0 || r.storeCalls()["active"] != 1 || len(r.storeCalls()) != 1 {
		t.Fatalf("chat %d store %v", r.chat.calls, r.storeCalls())
	}
}

func TestSendForwardAndDeletesFollowTheLivePairs(t *testing.T) {
	r := newRig(t, false)
	r.uploaded(t, fileX)
	first, forward := ref("7", 1), ref("8", 4)
	r.sent(first, fileX)
	r.sent(forward, fileX)
	if err := r.created(t, first); err != nil {
		t.Fatal(err)
	}
	if err := r.created(t, forward); err != nil {
		t.Fatal(err)
	}
	if len(r.objects.copies) != 1 || !slices.Equal(r.live(t, forward), []string{fileX}) {
		t.Fatalf("copies %v forward pairs %v", r.objects.copies, r.live(t, forward))
	}
	r.deleted(first)
	if err := r.removed(t, first); err != nil {
		t.Fatal(err)
	}
	if len(r.live(t, first)) != 0 || !r.doc(t, fileX).RetiredAt.IsZero() {
		t.Fatalf("original pairs %v retired %v", r.live(t, first), r.doc(t, fileX).RetiredAt)
	}
	r.clock = now.Add(time.Minute)
	if err := r.removed(t, forward); err != nil {
		t.Fatal(err)
	}
	if got := r.doc(t, fileX).RetiredAt; !got.Equal(now.Add(time.Minute)) {
		t.Fatalf("retired at %v", got)
	}
	if !r.objects.has(domain.BucketCompleted, fileX) || r.chat.calls != 2 {
		t.Fatalf("file gone or chat asked on delete: %d calls", r.chat.calls)
	}
}

func TestAForwardAfterTheLastDeleteClearsTheRetiredMark(t *testing.T) {
	r := newRig(t, false)
	r.uploaded(t, fileX)
	first, forward := ref("7", 1), ref("8", 4)
	r.sent(first, fileX)
	if err := r.created(t, first); err != nil {
		t.Fatal(err)
	}
	r.deleted(first)
	if err := r.removed(t, first); err != nil {
		t.Fatal(err)
	}
	if r.doc(t, fileX).RetiredAt.IsZero() {
		t.Fatal("last delete left no retired mark")
	}
	r.sent(forward, fileX)
	if err := r.created(t, forward); err != nil {
		t.Fatal(err)
	}
	if got := r.doc(t, fileX); !got.RetiredAt.IsZero() || got.Bucket != domain.BucketCompleted {
		t.Fatalf("after forward %+v", got)
	}
}

func TestADeleteHandledBeforeTheCreateLeavesNoPairAndNoMove(t *testing.T) {
	r := newRig(t, false)
	r.uploaded(t, fileX)
	m := ref("7", 1)
	r.deleted(m)
	if err := r.removed(t, m); err != nil {
		t.Fatal(err)
	}
	if err := r.created(t, m); err != nil {
		t.Fatal(err)
	}
	if len(r.live(t, m)) != 0 || len(r.objects.copies) != 0 || r.storeCalls()["get_many"] != 0 {
		t.Fatalf("pairs %v copies %v calls %v", r.live(t, m), r.objects.copies, r.storeCalls())
	}
}

func TestAnOldCreateAfterTheReleaseKeepsThePairReleased(t *testing.T) {
	r := newRig(t, false)
	r.uploaded(t, fileX)
	m := ref("7", 1)
	r.sent(m, fileX)
	if err := r.created(t, m); err != nil {
		t.Fatal(err)
	}
	r.deleted(m)
	if err := r.removed(t, m); err != nil {
		t.Fatal(err)
	}
	if err := r.created(t, m); err != nil {
		t.Fatal(err)
	}
	used, err := r.files.MessageFiles.StillUsed(t.Context(), []string{fileX}, "")
	if err != nil || used[fileX] || len(r.live(t, m)) != 0 {
		t.Fatalf("used %v pairs %v err %v", used, r.live(t, m), err)
	}
}

func TestReplayIsIdempotent(t *testing.T) {
	r := newRig(t, false)
	r.uploaded(t, fileX, fileY)
	m := ref("7", 1)
	r.sent(m, fileX, fileY, fileX)
	for range 2 {
		if err := r.created(t, m); err != nil {
			t.Fatal(err)
		}
	}
	if len(r.objects.copies) != 2 || r.svc.Stats().Moves(attach.ResultOK) != 2 || !slices.Equal(r.live(t, m), []string{fileX, fileY}) {
		t.Fatalf("copies %v pairs %v", r.objects.copies, r.live(t, m))
	}
	if c := r.storeCalls(); c["get_many"] != 2 || c["attach"] != 2 || c["unretire"] != 2 || c["mark_moved"] != 2 {
		t.Fatalf("calls %v, want one batch read and one batch write per delivery", c)
	}
}

func TestUploadsInProgressWaitUntilTheyExpire(t *testing.T) {
	r := newRig(t, false)
	m := ref("7", 1)
	r.upload(t, fileX, "image/png", domain.StateUploading, now.Add(-23*time.Hour))
	r.uploaded(t, fileY)
	r.sent(m, fileY, fileX)
	if err := r.created(t, m); !errors.Is(err, attach.ErrWaitUpload) {
		t.Fatalf("23h err = %v", err)
	}
	if c := r.storeCalls(); c["attach"] != 0 || len(r.objects.copies) != 0 || r.svc.Stats().Dropped(attach.ReasonExpired) != 0 {
		t.Fatalf("wrote while waiting: %v copies %v", c, r.objects.copies)
	}
	r.clock = now.Add(2 * time.Hour)
	if err := r.created(t, m); err != nil {
		t.Fatalf("25h err = %v", err)
	}
	if r.svc.Stats().Dropped(attach.ReasonExpired) != 1 || !slices.Equal(r.live(t, m), []string{fileY}) || !slices.Equal(r.objects.copies, []string{fileY}) {
		t.Fatalf("pairs %v copies %v", r.live(t, m), r.objects.copies)
	}
}

func TestMissingAbortedAndForeignFilesAreDropped(t *testing.T) {
	r := newRig(t, false)
	m := ref("7", 1)
	r.upload(t, fileY, "image/png", domain.StateAborted, now.Add(-time.Hour))
	r.uploaded(t, fileX)
	foreign := "other/an/20261108/0123456789abcdefghijklmnop"
	r.sent(m, foreign, fileX, fileY, "acme/an/20261108/vvvvvvvvvvvvvvvvvvvvvvvvvv")
	if err := r.created(t, m); err != nil {
		t.Fatal(err)
	}
	s := r.svc.Stats()
	if s.Dropped(attach.ReasonTenant) != 1 || s.Dropped(attach.ReasonAborted) != 1 || s.Dropped(attach.ReasonNoUpload) != 1 {
		t.Fatalf("drops tenant %d aborted %d missing %d", s.Dropped(attach.ReasonTenant), s.Dropped(attach.ReasonAborted), s.Dropped(attach.ReasonNoUpload))
	}
	if !slices.Equal(r.live(t, m), []string{fileX}) || !slices.Equal(r.objects.copies, []string{fileX}) {
		t.Fatalf("pairs %v copies %v", r.live(t, m), r.objects.copies)
	}
}

func TestOnlyForeignKeysNeverReadUploads(t *testing.T) {
	r := newRig(t, false)
	m := ref("7", 1)
	r.sent(m, "other/an/20261108/0123456789abcdefghijklmnop")
	if err := r.created(t, m); err != nil {
		t.Fatal(err)
	}
	if c := r.storeCalls(); len(c) != 0 || r.svc.Stats().Dropped(attach.ReasonTenant) != 1 {
		t.Fatalf("calls %v", c)
	}
}

func TestAMessageChatDoesNotHaveIsDropped(t *testing.T) {
	r := newRig(t, false)
	if err := r.created(t, ref("7", 1)); !errors.Is(err, attach.ErrDropped) {
		t.Fatalf("err = %v", err)
	}
	if r.svc.Stats().Dropped(attach.ReasonNoMessage) != 1 || len(r.storeCalls()) != 0 {
		t.Fatalf("drops %d calls %v", r.svc.Stats().Dropped(attach.ReasonNoMessage), r.storeCalls())
	}
}

func TestChatErrorsAndUnknownKindsComeBack(t *testing.T) {
	r := newRig(t, false)
	r.chat.err = errors.New("chat down")
	if err := r.created(t, ref("7", 1)); !errors.Is(err, r.chat.err) || len(r.storeCalls()) != 0 {
		t.Fatalf("err = %v calls %v", err, r.storeCalls())
	}
	if err := r.svc.Sync(t.Context(), attach.Kind(9), attach.Event{}); !errors.Is(err, apperr.ErrInvalidArgument) {
		t.Fatalf("unknown kind = %v", err)
	}
}
