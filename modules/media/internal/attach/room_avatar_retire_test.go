package attach_test

import (
	"errors"
	"testing"

	"github.com/ivannguyendev/chatim/modules/media/internal/domain"
)

func TestAReplacedAvatarIsRetiredOnlyWhenNoGroupUsesIt(t *testing.T) {
	r := newRig(t, false)
	r.uploaded(t, avatarA, avatarB)
	r.group(group, 2, avatarA)
	r.settle(t, group)
	r.group(team, 5, avatarA)
	r.settle(t, team)
	r.group(group, 3, avatarB)
	r.settle(t, group)
	if r.retired(t, avatarA) {
		t.Fatal("avatar A retired while the team still shows it")
	}
	r.group(team, 6)
	r.settle(t, team)
	if !r.retired(t, avatarA) || r.retired(t, avatarB) {
		t.Fatalf("retired A %v B %v, want only A", r.retired(t, avatarA), r.retired(t, avatarB))
	}
}

func TestAnAvatarSetAgainIsUnretired(t *testing.T) {
	r := newRig(t, false)
	r.uploaded(t, avatarA, avatarB)
	steps := []struct {
		ver uint64
		key string
	}{{2, avatarA}, {3, avatarB}, {4, avatarA}, {5, avatarA}}
	for _, s := range steps {
		r.group(group, s.ver, s.key)
		r.settle(t, group)
	}
	if r.retired(t, avatarA) || !r.retired(t, avatarB) || r.avatarOf(t, group).InfoVer != 5 {
		t.Fatalf("retired A %v B %v, doc %+v", r.retired(t, avatarA), r.retired(t, avatarB), r.avatarOf(t, group))
	}
}

func TestTwoNodesInterleavedNeverRetireTheShownAvatar(t *testing.T) {
	r := newRig(t, false)
	r.uploaded(t, avatarA, avatarB)
	r.group(group, 1, avatarA)
	r.settle(t, group)
	r.group(group, 2, avatarB)
	r.avatars.beforeInUse = func() {
		r.group(group, 3, avatarA)
		r.settle(t, group)
	}
	r.settle(t, group)
	if r.retired(t, avatarA) || !r.retired(t, avatarB) {
		t.Fatalf("retired A %v B %v, want only B", r.retired(t, avatarA), r.retired(t, avatarB))
	}
	if got := r.avatarOf(t, group); got.FileKey != avatarA || got.InfoVer != 3 {
		t.Fatalf("doc %+v", got)
	}
}

func TestALostWriteRetiresNothingAndTheRetryCopiesNothing(t *testing.T) {
	r := newRig(t, false)
	r.uploaded(t, avatarA, avatarB)
	r.group(group, 2, avatarA)
	r.settle(t, group)
	r.group(group, 3, avatarB)
	r.avatars.putErr = domain.ErrRetryLater
	if err := r.roomUpdated(t, group); !errors.Is(err, domain.ErrRetryLater) {
		t.Fatalf("err = %v, want ErrRetryLater", err)
	}
	if r.retired(t, avatarA) || !r.objects.has(domain.BucketCompleted, avatarB) {
		t.Fatalf("after the lost write: A retired %v, B moved %v", r.retired(t, avatarA), r.objects.has(domain.BucketCompleted, avatarB))
	}
	copies := len(r.objects.copies)
	r.settle(t, group)
	if len(r.objects.copies) != copies || !r.retired(t, avatarA) || r.avatarOf(t, group).FileKey != avatarB {
		t.Fatalf("retry: copies %v, A retired %v, doc %+v", r.objects.copies, r.retired(t, avatarA), r.avatarOf(t, group))
	}
}

func TestARetireThatLandsAfterTheAvatarIsShownAgainIsUndone(t *testing.T) {
	r := newRig(t, false)
	r.uploaded(t, avatarA, avatarB)
	r.group(group, 1, avatarA)
	r.settle(t, group)
	r.group(group, 2, avatarB)
	r.uploads.beforeRetire = func() {
		r.group(group, 3, avatarA)
		r.settle(t, group)
	}
	r.settle(t, group)
	if r.retired(t, avatarA) || !r.retired(t, avatarB) || r.avatarOf(t, group).FileKey != avatarA {
		t.Fatalf("retired A %v B %v, doc %+v; want only B retired", r.retired(t, avatarA), r.retired(t, avatarB), r.avatarOf(t, group))
	}
}

func TestTheShownAvatarIsUnretiredAfterTheWrite(t *testing.T) {
	r := newRig(t, false)
	r.uploaded(t, avatarA)
	r.group(group, 2, avatarA)
	r.settle(t, group)
	if got := r.storeCalls()["unretire"]; got != 2 {
		t.Fatalf("unretire calls %d, want one before and one after the write", got)
	}
}
