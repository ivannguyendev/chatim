package attach_test

import (
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/ivannguyendev/chatim/modules/media/internal/attach"
	"github.com/ivannguyendev/chatim/modules/media/internal/domain"
)

var errStorage = errors.New("storage down")

func TestASentFileMovesWithItsThumbnail(t *testing.T) {
	r := newRig(t, true)
	r.uploaded(t, fileX)
	thumb := fileX + ".thumb"
	r.objects.buckets[domain.BucketProcessing][thumb] = domain.Object{Size: 3, ContentType: "image/webp"}
	m := ref("7", 1)
	r.sent(m, fileX)
	if err := r.created(t, m); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(r.objects.copies, []string{fileX, thumb}) || !slices.Equal(r.objects.deletes, []string{fileX, thumb}) {
		t.Fatalf("copies %v deletes %v", r.objects.copies, r.objects.deletes)
	}
	if got := r.objects.buckets[domain.BucketCompleted][thumb]; got != (domain.Object{Size: 3, ContentType: "image/webp"}) {
		t.Fatalf("thumbnail in completed %+v", got)
	}
	if r.objects.has(domain.BucketProcessing, fileX) || r.objects.has(domain.BucketProcessing, thumb) {
		t.Fatal("processing copies left")
	}
	u := r.doc(t, fileX)
	if u.Bucket != domain.BucketCompleted || !u.AttachedAt.Equal(now) || u.Ver != 3 || !slices.Equal(r.live(t, m), []string{fileX}) {
		t.Fatalf("upload %+v pairs %v", u, r.live(t, m))
	}
}

func TestAFileWithoutAThumbnailMovesAlone(t *testing.T) {
	r := newRig(t, true)
	r.uploaded(t, fileX)
	m := ref("7", 1)
	r.sent(m, fileX)
	if err := r.created(t, m); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(r.objects.copies, []string{fileX}) || !slices.Equal(r.objects.deletes, []string{fileX}) || r.objects.stats != 1 {
		t.Fatalf("copies %v deletes %v stats %d", r.objects.copies, r.objects.deletes, r.objects.stats)
	}
}

func TestThumbnailsOffOrNotAnImageSkipTheThumbnail(t *testing.T) {
	off := newRig(t, false)
	off.uploaded(t, fileX)
	off.sent(ref("7", 1), fileX)
	if err := off.created(t, ref("7", 1)); err != nil || off.objects.stats != 0 {
		t.Fatalf("thumbnails off: %v stats %d", err, off.objects.stats)
	}
	pdf := newRig(t, true)
	pdf.upload(t, fileX, "application/pdf", domain.StateUploaded, now.Add(-time.Hour))
	pdf.sent(ref("7", 1), fileX)
	if err := pdf.created(t, ref("7", 1)); err != nil || pdf.objects.stats != 0 {
		t.Fatalf("pdf: %v stats %d", err, pdf.objects.stats)
	}
}

func TestACopyFailureChangesNothingAndComesBack(t *testing.T) {
	r := newRig(t, true)
	r.uploaded(t, fileX)
	m := ref("7", 1)
	r.sent(m, fileX)
	r.objects.copyErr = errStorage
	if err := r.created(t, m); !errors.Is(err, errStorage) {
		t.Fatalf("err = %v", err)
	}
	if r.uploads.calls["mark_moved"] != 0 || len(r.objects.deletes) != 0 || r.doc(t, fileX).Bucket == domain.BucketCompleted {
		t.Fatalf("mark moved %d deletes %v", r.uploads.calls["mark_moved"], r.objects.deletes)
	}
	if r.svc.Stats().Moves(attach.ResultError) != 1 {
		t.Fatalf("move errors %d", r.svc.Stats().Moves(attach.ResultError))
	}
	r.objects.copyErr = nil
	if err := r.created(t, m); err != nil || r.doc(t, fileX).Bucket != domain.BucketCompleted {
		t.Fatalf("retry %v bucket %q", err, r.doc(t, fileX).Bucket)
	}
}

func TestAThumbnailFailureKeepsTheFileInProcessing(t *testing.T) {
	r := newRig(t, true)
	r.uploaded(t, fileX)
	thumb := fileX + ".thumb"
	r.objects.buckets[domain.BucketProcessing][thumb] = domain.Object{Size: 3, ContentType: "image/webp"}
	m := ref("7", 1)
	r.sent(m, fileX)
	r.objects.copyErr, r.objects.copyTarget = errStorage, thumb
	if err := r.created(t, m); !errors.Is(err, errStorage) {
		t.Fatalf("err = %v", err)
	}
	r.objects.statErr, r.objects.copyErr = errStorage, nil
	if err := r.created(t, m); !errors.Is(err, errStorage) {
		t.Fatalf("stat err = %v", err)
	}
	if r.uploads.calls["mark_moved"] != 0 || len(r.objects.deletes) != 0 || !r.objects.has(domain.BucketProcessing, fileX) {
		t.Fatalf("mark moved %d deletes %v", r.uploads.calls["mark_moved"], r.objects.deletes)
	}
}

func TestALostVersionRaceComesBackThenCompletes(t *testing.T) {
	r := newRig(t, false)
	r.uploaded(t, fileX)
	m := ref("7", 1)
	r.sent(m, fileX)
	r.uploads.conflictOnce = true
	if err := r.created(t, m); !errors.Is(err, domain.ErrRetryLater) {
		t.Fatalf("err = %v", err)
	}
	if len(r.objects.deletes) != 0 || r.doc(t, fileX).Bucket == domain.BucketCompleted {
		t.Fatalf("deleted %v before the move was recorded", r.objects.deletes)
	}
	if err := r.created(t, m); err != nil {
		t.Fatal(err)
	}
	if u := r.doc(t, fileX); u.Bucket != domain.BucketCompleted || len(r.objects.copies) != 2 || !slices.Equal(r.objects.deletes, []string{fileX}) {
		t.Fatalf("upload %+v copies %v deletes %v", u, r.objects.copies, r.objects.deletes)
	}
}

func TestADeleteFailureIsOnlyCounted(t *testing.T) {
	r := newRig(t, false)
	r.uploaded(t, fileX)
	m := ref("7", 1)
	r.sent(m, fileX)
	r.objects.deleteErr = errStorage
	if err := r.created(t, m); err != nil {
		t.Fatal(err)
	}
	s := r.svc.Stats()
	if r.doc(t, fileX).Bucket != domain.BucketCompleted || s.Moves(attach.ResultOK) != 1 || s.Moves(attach.ResultCleanupError) != 1 {
		t.Fatalf("bucket %q ok %d cleanup %d", r.doc(t, fileX).Bucket, s.Moves(attach.ResultOK), s.Moves(attach.ResultCleanupError))
	}
}

func TestStatsListEveryLabel(t *testing.T) {
	s := &attach.Stats{}
	s.Drop(attach.ReasonInvalid)
	var labels []string
	for _, c := range s.DroppedCounts() {
		labels = append(labels, c.Label)
		if c.Label == "invalid_event" && c.Read() != 1 {
			t.Fatalf("invalid count %d", c.Read())
		}
	}
	if !slices.Equal(labels, []string{"no_message", "no_upload", "aborted", "tenant_mismatch", "upload_expired", "invalid_event", "no_room"}) {
		t.Fatalf("drop labels %v", labels)
	}
	labels = labels[:0]
	for _, c := range s.MoveCounts() {
		labels = append(labels, c.Label)
	}
	if !slices.Equal(labels, []string{"ok", "error", "cleanup_error"}) {
		t.Fatalf("move labels %v", labels)
	}
}
