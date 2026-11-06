package upload_test

import (
	"errors"
	"sync"
	"testing"

	"github.com/ivannguyendev/chatim/modules/media/internal/domain"
	"github.com/ivannguyendev/chatim/modules/media/internal/objectstore"
	"github.com/ivannguyendev/chatim/pkg/core/apperr"
)

func TestCompleteMarksTheUploadDone(t *testing.T) {
	r := newRig(t)
	s := r.start(t)
	shuffled := []domain.UploadedPart{{Number: 3, ETag: "c"}, {Number: 1, ETag: "a"}, {Number: 2, ETag: "b"}}
	u, err := r.svc.Complete(t.Context(), an, s.Upload.Key, shuffled)
	if err != nil || u.State != domain.StateUploaded || u.Ver != 2 || u.CompletedAt.IsZero() {
		t.Fatalf("complete = %+v, %v", u, err)
	}
	again, err := r.svc.Complete(t.Context(), an, s.Upload.Key, nil)
	if err != nil || again.State != domain.StateUploaded || r.objects.completes != 1 {
		t.Fatalf("second complete = %+v, %v, storage completes %d", again, err, r.objects.completes)
	}
}

func TestCompleteRecoversWhenStorageAlreadyClosedTheSession(t *testing.T) {
	r := newRig(t)
	s := r.start(t)
	r.objects.set(func(f *fakeObjects) {
		delete(f.open, s.Upload.UploadID)
		f.objects[s.Upload.Key] = 3
	})
	u, err := r.svc.Complete(t.Context(), an, s.Upload.Key, etags(3))
	if err != nil || u.State != domain.StateUploaded {
		t.Fatalf("complete = %+v, %v", u, err)
	}
}

func TestCompleteRefusals(t *testing.T) {
	cases := map[string]struct {
		prepare func(r *rig, key, id string)
		parts   []domain.UploadedPart
		want    error
		state   domain.State
	}{
		"missing part": {
			parts: etags(2), want: apperr.ErrInvalidArgument, state: domain.StateUploading,
		},
		"storage rejects an ETag": {
			prepare: func(r *rig, _, _ string) {
				r.objects.set(func(f *fakeObjects) { f.completeErr = objectstore.ErrInvalidPart })
			},
			parts: etags(3), want: domain.ErrPartRejected, state: domain.StateUploading,
		},
		"storage down": {
			prepare: func(r *rig, _, _ string) {
				r.objects.set(func(f *fakeObjects) { f.completeErr = objectstore.ErrUnavailable })
			},
			parts: etags(3), want: apperr.ErrUnavailable, state: domain.StateUploading,
		},
		"session gone and no object": {
			prepare: func(r *rig, _, id string) { r.objects.set(func(f *fakeObjects) { delete(f.open, id) }) },
			parts:   etags(3), want: domain.ErrUploadGone, state: domain.StateUploading,
		},
		"object size differs from the declared size": {
			prepare: func(r *rig, _, _ string) { r.objects.set(func(f *fakeObjects) { f.sizeDelta = -1 }) },
			parts:   etags(3), want: domain.ErrSizeMismatch, state: domain.StateAborted,
		},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			r := newRig(t)
			s := r.start(t)
			if c.prepare != nil {
				c.prepare(r, s.Upload.Key, s.Upload.UploadID)
			}
			_, err := r.svc.Complete(t.Context(), an, s.Upload.Key, c.parts)
			if !errors.Is(err, c.want) {
				t.Fatalf("err = %v, want %v", err, c.want)
			}
			got, _ := r.uploads.Get(t.Context(), s.Upload.Key)
			if got.State != c.state {
				t.Fatalf("state = %d, want %d", got.State, c.state)
			}
		})
	}
}

func TestCompleteDeletesAnObjectOfTheWrongSize(t *testing.T) {
	r := newRig(t)
	s := r.start(t)
	r.objects.set(func(f *fakeObjects) { f.sizeDelta = 1 })
	if _, err := r.svc.Complete(t.Context(), an, s.Upload.Key, etags(3)); !errors.Is(err, apperr.ErrFailedPrecondition) {
		t.Fatal(err)
	}
	if r.objects.deletes != 1 {
		t.Fatalf("deletes = %d", r.objects.deletes)
	}
}

func TestConcurrentCompletesBothSucceed(t *testing.T) {
	r := newRig(t)
	s := r.start(t)
	var wg sync.WaitGroup
	errs := make(chan error, 6)
	for range 6 {
		wg.Go(func() {
			_, err := r.svc.Complete(t.Context(), an, s.Upload.Key, etags(3))
			errs <- err
		})
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("complete err = %v", err)
		}
	}
	got, _ := r.uploads.Get(t.Context(), s.Upload.Key)
	if got.State != domain.StateUploaded || got.Ver != 2 {
		t.Fatalf("final %+v", got)
	}
}

func TestAbort(t *testing.T) {
	r := newRig(t)
	s := r.start(t)
	if err := r.svc.Abort(t.Context(), binh, s.Upload.Key); !errors.Is(err, apperr.ErrNotFound) {
		t.Fatalf("someone else: %v", err)
	}
	if err := r.svc.Abort(t.Context(), an, s.Upload.Key); err != nil {
		t.Fatal(err)
	}
	if err := r.svc.Abort(t.Context(), an, s.Upload.Key); err != nil {
		t.Fatalf("second abort: %v", err)
	}
	if _, _, err := r.svc.PartURLs(t.Context(), an, s.Upload.Key, []int32{1}); !errors.Is(err, apperr.ErrFailedPrecondition) {
		t.Fatalf("part URLs after abort: %v", err)
	}
	if _, err := r.svc.Complete(t.Context(), an, s.Upload.Key, etags(3)); !errors.Is(err, apperr.ErrFailedPrecondition) {
		t.Fatalf("complete after abort: %v", err)
	}
}

func TestAbortLosesToACompleteThatAlreadyFinished(t *testing.T) {
	r := newRig(t)
	s := r.start(t)
	r.objects.set(func(f *fakeObjects) {
		delete(f.open, s.Upload.UploadID)
		f.objects[s.Upload.Key] = 3
	})
	if err := r.svc.Abort(t.Context(), an, s.Upload.Key); !errors.Is(err, apperr.ErrFailedPrecondition) {
		t.Fatalf("abort = %v", err)
	}
	if got, _ := r.uploads.Get(t.Context(), s.Upload.Key); got.State != domain.StateUploading {
		t.Fatalf("state = %d", got.State)
	}
	if _, err := r.svc.Complete(t.Context(), an, s.Upload.Key, etags(3)); err != nil {
		t.Fatalf("complete after the lost abort: %v", err)
	}
	if err := r.svc.Abort(t.Context(), an, s.Upload.Key); !errors.Is(err, apperr.ErrFailedPrecondition) {
		t.Fatalf("abort after complete = %v", err)
	}
}

func TestAbortWhenStorageAlreadyDroppedTheSession(t *testing.T) {
	r := newRig(t)
	s := r.start(t)
	r.objects.set(func(f *fakeObjects) { delete(f.open, s.Upload.UploadID) })
	if err := r.svc.Abort(t.Context(), an, s.Upload.Key); err != nil {
		t.Fatal(err)
	}
	if got, _ := r.uploads.Get(t.Context(), s.Upload.Key); got.State != domain.StateAborted {
		t.Fatalf("state = %d", got.State)
	}
}

func TestEveryCallWaitsForReadyStorage(t *testing.T) {
	r := newRig(t)
	s := r.start(t)
	r.ready = false
	_, _, errURLs := r.svc.PartURLs(t.Context(), an, s.Upload.Key, []int32{1})
	_, errComplete := r.svc.Complete(t.Context(), an, s.Upload.Key, etags(3))
	errAbort := r.svc.Abort(t.Context(), an, s.Upload.Key)
	for _, err := range []error{errURLs, errComplete, errAbort} {
		if !errors.Is(err, domain.ErrNotReady) {
			t.Fatalf("err = %v", err)
		}
	}
}
