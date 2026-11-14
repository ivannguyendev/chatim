package attach_test

import (
	"errors"
	"maps"
	"testing"
	"time"

	"github.com/ivannguyendev/chatim/modules/media/internal/attach"
	"github.com/ivannguyendev/chatim/modules/media/internal/domain"
)

func TestTheFirstAvatarMovesToCompletedAndIsRecorded(t *testing.T) {
	r := newRig(t, false)
	r.uploaded(t, avatarA)
	r.group(group, 2, avatarA)
	r.settle(t, group)
	if !r.objects.has(domain.BucketCompleted, avatarA) || r.doc(t, avatarA).Bucket != domain.BucketCompleted {
		t.Fatalf("avatar not moved: %+v", r.doc(t, avatarA))
	}
	if got := r.avatarOf(t, group); got.FileKey != avatarA || got.InfoVer != 2 || got.Tenant != tenant || !got.UpdatedAt.Equal(now) {
		t.Fatalf("avatar doc = %+v", got)
	}
}

func TestAnOldEventChangesNothing(t *testing.T) {
	r := newRig(t, false)
	r.uploaded(t, avatarA)
	r.group(group, 2, avatarA)
	r.settle(t, group)
	before := r.storeCalls()
	r.settle(t, group)
	after := r.storeCalls()
	after["avatar_get"]--
	if !maps.Equal(before, after) {
		t.Fatalf("calls %v, want only one more avatar read after %v", after, before)
	}
}

func TestARenameOfAGroupWithoutAvatarWritesNothing(t *testing.T) {
	r := newRig(t, false)
	r.group(group, 3)
	r.settle(t, group)
	if calls := r.storeCalls(); calls["get_many"] != 0 || calls["avatar_put"] != 0 {
		t.Fatalf("calls %v, want no upload read and no write", calls)
	}
}

func TestARenameOfAGroupWithAMovedAvatarCopiesNothing(t *testing.T) {
	r := newRig(t, false)
	r.uploaded(t, avatarA)
	r.group(group, 2, avatarA)
	r.settle(t, group)
	copies := len(r.objects.copies)
	r.group(group, 3, avatarA)
	r.settle(t, group)
	if len(r.objects.copies) != copies || r.avatarOf(t, group).InfoVer != 3 || r.retired(t, avatarA) {
		t.Fatalf("copies %v, doc %+v", r.objects.copies, r.avatarOf(t, group))
	}
}

func TestAnAvatarUploadedLateMovesOnTheNextRoomEvent(t *testing.T) {
	r := newRig(t, false)
	r.upload(t, avatarA, "image/png", domain.StateUploading, now.Add(-25*time.Hour))
	r.group(group, 2, avatarA)
	r.settle(t, group)
	if r.svc.Stats().Dropped(attach.ReasonExpired) != 1 || r.avatarOf(t, group).FileKey != avatarA || r.objects.has(domain.BucketCompleted, avatarA) {
		t.Fatalf("expired upload: dropped %d, doc %+v", r.svc.Stats().Dropped(attach.ReasonExpired), r.avatarOf(t, group))
	}
	if _, err := r.uploads.Transition(t.Context(), avatarA, domain.StateUploading, 2, domain.StateUploaded, ""); err != nil {
		t.Fatal(err)
	}
	r.objects.buckets[domain.BucketProcessing][avatarA] = domain.Object{Size: 9, ContentType: "image/png"}
	r.group(group, 3, avatarA)
	r.settle(t, group)
	if !r.objects.has(domain.BucketCompleted, avatarA) || r.doc(t, avatarA).Bucket != domain.BucketCompleted {
		t.Fatalf("late upload not moved: %+v", r.doc(t, avatarA))
	}
}

func TestAnAvatarStillUploadingWaits(t *testing.T) {
	r := newRig(t, false)
	r.upload(t, avatarA, "image/png", domain.StateUploading, now.Add(-time.Minute))
	r.group(group, 2, avatarA)
	if err := r.roomUpdated(t, group); !errors.Is(err, attach.ErrWaitUpload) {
		t.Fatalf("err = %v, want ErrWaitUpload", err)
	}
	if calls := r.storeCalls(); calls["avatar_put"] != 0 {
		t.Fatalf("calls %v, want no write", calls)
	}
}

func TestAnAbortedAvatarIsRecordedButNotMoved(t *testing.T) {
	r := newRig(t, false)
	r.upload(t, avatarA, "image/png", domain.StateAborted, now.Add(-time.Minute))
	r.group(group, 2, avatarA)
	r.settle(t, group)
	if r.svc.Stats().Dropped(attach.ReasonAborted) != 1 || r.avatarOf(t, group).FileKey != avatarA || len(r.objects.copies) != 0 {
		t.Fatalf("dropped %d, doc %+v, copies %v", r.svc.Stats().Dropped(attach.ReasonAborted), r.avatarOf(t, group), r.objects.copies)
	}
}

func TestRoomsThatAreGoneForeignOrDirectAreSkipped(t *testing.T) {
	r := newRig(t, false)
	if err := r.roomUpdated(t, group); !errors.Is(err, attach.ErrDropped) || r.svc.Stats().Dropped(attach.ReasonNoRoom) != 1 {
		t.Fatalf("missing room: %v, dropped %d", err, r.svc.Stats().Dropped(attach.ReasonNoRoom))
	}
	r.chat.setRoom(group, domain.RoomAvatarState{Found: true, Tenant: "other", Group: true, Keys: []string{avatarA}, InfoVer: 2})
	if err := r.roomUpdated(t, group); !errors.Is(err, attach.ErrDropped) || r.svc.Stats().Dropped(attach.ReasonTenant) != 1 {
		t.Fatalf("foreign room: %v, dropped %d", err, r.svc.Stats().Dropped(attach.ReasonTenant))
	}
	r.chat.setRoom(group, domain.RoomAvatarState{Found: true, Tenant: tenant, InfoVer: 2})
	r.settle(t, group)
	if calls := r.storeCalls(); calls["avatar_get"] != 0 || calls["avatar_put"] != 0 {
		t.Fatalf("calls %v, want no avatar doc read or write", calls)
	}
}

func TestKeysThatAreNotPublicFilesOfTheTenantCountAsNoAvatar(t *testing.T) {
	r := newRig(t, false)
	r.uploaded(t, fileX)
	for _, key := range []string{fileX, "other/an/pub/20261113/0123456789abcdefghijklmnop"} {
		r.group(group, 2, key)
		r.settle(t, group)
		if calls := r.storeCalls(); calls["get_many"] != 0 || calls["avatar_put"] != 0 {
			t.Fatalf("%s: calls %v, want no upload read and no write", key, calls)
		}
	}
	r.uploaded(t, avatarA)
	r.group(group, 3, avatarA)
	r.settle(t, group)
	r.group(group, 4, fileX)
	r.settle(t, group)
	if got := r.avatarOf(t, group); got.FileKey != "" || got.InfoVer != 4 || !r.retired(t, avatarA) {
		t.Fatalf("doc %+v, avatar retired %v", got, r.retired(t, avatarA))
	}
}
