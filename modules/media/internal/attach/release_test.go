package attach_test

import (
	"errors"
	"testing"

	"github.com/ivannguyendev/chatim/modules/media/internal/attach"
	"github.com/ivannguyendev/chatim/modules/media/internal/domain"
)

func sentAndDeleted(t *testing.T) (*rig, domain.MessageRef) {
	t.Helper()
	r := newRig(t, false)
	r.uploaded(t, fileX)
	m := ref("7", 1)
	r.sent(m, fileX)
	if err := r.created(t, m); err != nil {
		t.Fatal(err)
	}
	r.deleted(m)
	return r, m
}

func TestAFailedRetireComesBackAndStillRetires(t *testing.T) {
	r, m := sentAndDeleted(t)
	r.uploads.retireErr = errStorage
	if err := r.removed(t, m); !errors.Is(err, errStorage) {
		t.Fatalf("err = %v", err)
	}
	if r.files.calls["release"] != 0 || len(r.live(t, m)) != 1 {
		t.Fatalf("released before the retire: %v", r.storeCalls())
	}
	if err := r.removed(t, m); err != nil {
		t.Fatal(err)
	}
	if r.doc(t, fileX).RetiredAt.IsZero() || len(r.live(t, m)) != 0 {
		t.Fatalf("redelivery left retired %v pairs %v", r.doc(t, fileX).RetiredAt, r.live(t, m))
	}
}

func TestAFailedReleaseComesBackAndCompletes(t *testing.T) {
	r, m := sentAndDeleted(t)
	r.files.releaseErr = errStorage
	if err := r.removed(t, m); !errors.Is(err, errStorage) {
		t.Fatalf("err = %v", err)
	}
	if r.doc(t, fileX).RetiredAt.IsZero() || len(r.live(t, m)) != 1 {
		t.Fatalf("retired %v pairs %v", r.doc(t, fileX).RetiredAt, r.live(t, m))
	}
	if err := r.removed(t, m); err != nil {
		t.Fatal(err)
	}
	if r.doc(t, fileX).RetiredAt.IsZero() || len(r.live(t, m)) != 0 {
		t.Fatalf("redelivery left retired %v pairs %v", r.doc(t, fileX).RetiredAt, r.live(t, m))
	}
}

func TestAChatTenantOtherThanTheEventsIsDropped(t *testing.T) {
	r := newRig(t, false)
	r.uploaded(t, fileX)
	m := ref("7", 1)
	r.chat.set(m, domain.MessageState{Found: true, Tenant: "other", Keys: []string{fileX}})
	if err := r.created(t, m); !errors.Is(err, attach.ErrDropped) {
		t.Fatalf("err = %v", err)
	}
	if r.svc.Stats().Dropped(attach.ReasonTenant) != 1 || len(r.storeCalls()) != 0 || len(r.objects.copies) != 0 {
		t.Fatalf("drops %d calls %v copies %v", r.svc.Stats().Dropped(attach.ReasonTenant), r.storeCalls(), r.objects.copies)
	}
}

func TestDropsCountOnceTheWritesSucceed(t *testing.T) {
	r := newRig(t, false)
	r.uploaded(t, fileX)
	r.upload(t, fileY, "image/png", domain.StateAborted, now)
	m := ref("7", 1)
	r.sent(m, fileX, fileY)
	r.uploads.unretireErr = errStorage
	if err := r.created(t, m); !errors.Is(err, errStorage) {
		t.Fatalf("err = %v", err)
	}
	if got := r.svc.Stats().Dropped(attach.ReasonAborted); got != 0 {
		t.Fatalf("drops counted before the writes: %d", got)
	}
	if err := r.created(t, m); err != nil {
		t.Fatal(err)
	}
	if got := r.svc.Stats().Dropped(attach.ReasonAborted); got != 1 {
		t.Fatalf("drops %d, want 1", got)
	}
}
