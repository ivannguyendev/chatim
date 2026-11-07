package gallery_test

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/ivannguyendev/chatim/modules/media/internal/domain"
	"github.com/ivannguyendev/chatim/modules/media/internal/gallery"
	"github.com/ivannguyendev/chatim/modules/media/internal/store/memstore"
	"github.com/ivannguyendev/chatim/modules/media/internal/store/storetest"
	"github.com/ivannguyendev/chatim/pkg/core/apperr"
)

const retention = 30 * 24 * time.Hour

var (
	now = time.Date(2026, 11, 7, 10, 0, 0, 0, time.UTC)
	an  = domain.Caller{Tenant: "acme", User: "an"}
)

type signedGet struct {
	key string
	ttl time.Duration
	how domain.Disposition
}

type fakeObjects struct {
	mu     sync.Mutex
	signed []signedGet
	err    error
}

func (f *fakeObjects) SignGet(_ context.Context, key string, ttl time.Duration, how domain.Disposition) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.signed = append(f.signed, signedGet{key: key, ttl: ttl, how: how})
	return "https://s3/" + key, f.err
}

type rig struct {
	svc     *gallery.Service
	uploads *memstore.Uploads
	objects *fakeObjects
	ready   bool
}

func newRig(t *testing.T, thumbs domain.Thumbnails) *rig {
	t.Helper()
	r := &rig{uploads: memstore.NewUploads(), objects: &fakeObjects{}, ready: true}
	svc, err := gallery.New(gallery.Config{SignatureTTL: time.Hour, Retention: retention, Thumbnails: thumbs}, gallery.Deps{
		Uploads: r.uploads, Objects: r.objects, Ready: func() bool { return r.ready }, Now: func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}
	r.svc = svc
	return r
}

func (r *rig) add(t *testing.T, uploads ...domain.Upload) {
	t.Helper()
	for _, u := range uploads {
		if err := r.uploads.Insert(t.Context(), u); err != nil {
			t.Fatal(err)
		}
	}
}

func uploaded(user, random, mime string, age time.Duration) domain.Upload {
	u := storetest.Uploaded("acme", user, random, mime)
	u.FileName, u.CreatedAt = "f-"+random, now.Add(-age)
	return u
}

func keys(files []gallery.File) []string {
	out := make([]string, len(files))
	for i, f := range files {
		out[i] = f.Upload.Key
	}
	return out
}

func TestNewNeedsUploadsObjectsAndDurations(t *testing.T) {
	if _, err := gallery.New(gallery.Config{SignatureTTL: time.Hour, Retention: retention}, gallery.Deps{Uploads: memstore.NewUploads()}); err == nil {
		t.Fatal("missing objects accepted")
	}
	if _, err := gallery.New(gallery.Config{SignatureTTL: time.Hour}, gallery.Deps{Uploads: memstore.NewUploads(), Objects: &fakeObjects{}}); err == nil {
		t.Fatal("zero retention accepted")
	}
}

func TestListShowsOnlyTheCallersFiles(t *testing.T) {
	r := newRig(t, domain.Thumbnails{})
	mine := uploaded("an", "0001", "image/png", time.Hour)
	r.add(t, mine, uploaded("an2", "0002", "image/png", time.Hour), uploaded("an-b", "0003", "image/png", time.Hour),
		storetest.Uploaded("other", "an", "0004", "image/png"))
	page, err := r.svc.List(t.Context(), an, gallery.Input{PageSize: 10})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(keys(page.Files), []string{mine.Key}) || page.NextBefore != "" || !page.ExpiresAt.Equal(now.Add(time.Hour)) {
		t.Fatalf("page %+v", page)
	}
	f := page.Files[0]
	if f.Expired || f.URL != "https://s3/"+mine.Key || f.ThumbnailURL != "" {
		t.Fatalf("file %+v", f)
	}
	want := []signedGet{{mine.Key, time.Hour, domain.FileDisposition(mine.FileName, mine.ContentType)}}
	if !slices.Equal(r.objects.signed, want) {
		t.Fatalf("signed %+v", r.objects.signed)
	}
}

func TestListPagesNewestFirst(t *testing.T) {
	r := newRig(t, domain.Thumbnails{})
	for i := range 5 {
		r.add(t, uploaded("an", fmt.Sprintf("%04d", i), "image/png", time.Hour))
	}
	var got []string
	in := gallery.Input{PageSize: 2}
	for page := range 3 {
		p, err := r.svc.List(t.Context(), an, in)
		if err != nil {
			t.Fatal(err)
		}
		got = append(got, keys(p.Files)...)
		if wantNext := page < 2; (p.NextBefore != "") != wantNext {
			t.Fatalf("page %d next %q", page, p.NextBefore)
		}
		in.Before = p.NextBefore
	}
	want := []string{"acme/an/20261107/0004", "acme/an/20261107/0003", "acme/an/20261107/0002", "acme/an/20261107/0001", "acme/an/20261107/0000"}
	if !slices.Equal(got, want) {
		t.Fatalf("pages %v", got)
	}
}

func TestListFiltersByKinds(t *testing.T) {
	r := newRig(t, domain.Thumbnails{})
	r.add(t, uploaded("an", "0003", "image/png", time.Hour), uploaded("an", "0002", "video/mp4", time.Hour), uploaded("an", "0001", "application/pdf", time.Hour))
	page, err := r.svc.List(t.Context(), an, gallery.Input{Kinds: domain.Kinds{Video: true, Other: true}, PageSize: 10})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(keys(page.Files), []string{"acme/an/20261107/0002", "acme/an/20261107/0001"}) {
		t.Fatalf("listed %v", keys(page.Files))
	}
}

func TestExpiredFilesHaveNoURLsFromDayThirty(t *testing.T) {
	r := newRig(t, domain.Thumbnails{Enabled: true, Suffix: ".thumb"})
	fresh := uploaded("an", "0002", "image/png", retention-time.Hour)
	old := uploaded("an", "0001", "image/png", retention)
	r.add(t, fresh, old)
	page, err := r.svc.List(t.Context(), an, gallery.Input{PageSize: 10})
	if err != nil {
		t.Fatal(err)
	}
	got, gone := page.Files[0], page.Files[1]
	if got.Expired || got.URL == "" || got.ThumbnailURL != "https://s3/"+fresh.Key+".thumb" {
		t.Fatalf("29d23h file %+v", got)
	}
	if !gone.Expired || gone.URL != "" || gone.ThumbnailURL != "" || gone.Upload.Key != old.Key {
		t.Fatalf("30d file %+v", gone)
	}
	if len(r.objects.signed) != 2 {
		t.Fatalf("signed %+v", r.objects.signed)
	}
}

func TestThumbnailURLsOnlyForImagesAndVideosWhenOn(t *testing.T) {
	for _, on := range []bool{false, true} {
		r := newRig(t, domain.Thumbnails{Enabled: on, Suffix: ".thumb"})
		r.add(t, uploaded("an", "0003", "image/png", time.Hour), uploaded("an", "0002", "video/mp4", time.Hour), uploaded("an", "0001", "application/pdf", time.Hour))
		page, err := r.svc.List(t.Context(), an, gallery.Input{PageSize: 10})
		if err != nil {
			t.Fatal(err)
		}
		for i, f := range page.Files {
			if wantThumb := on && i < 2; (f.ThumbnailURL != "") != wantThumb || f.URL == "" {
				t.Fatalf("thumbnails %v: file %+v", on, f)
			}
		}
	}
}

func TestBeforeMustBeTheCallersUpload(t *testing.T) {
	r := newRig(t, domain.Thumbnails{})
	theirs := uploaded("an2", "0001", "image/png", time.Hour)
	otherTenant := storetest.Uploaded("other", "an", "0002", "image/png")
	r.add(t, theirs, otherTenant)
	for _, before := range []string{theirs.Key, otherTenant.Key, "acme/an/20261107/none"} {
		if _, err := r.svc.List(t.Context(), an, gallery.Input{Before: before, PageSize: 10}); !errors.Is(err, apperr.ErrInvalidArgument) {
			t.Fatalf("before %s: err = %v", before, err)
		}
	}
}

func TestPageSizeIsOneToAHundred(t *testing.T) {
	r := newRig(t, domain.Thumbnails{})
	for _, n := range []int{0, -1, gallery.MaxPageSize + 1} {
		if _, err := r.svc.List(t.Context(), an, gallery.Input{PageSize: n}); !errors.Is(err, apperr.ErrInvalidArgument) {
			t.Fatalf("page size %d: err = %v", n, err)
		}
	}
	if _, err := r.svc.List(t.Context(), an, gallery.Input{PageSize: gallery.MaxPageSize}); err != nil {
		t.Fatal(err)
	}
}

func TestListRefusesUntilReadyAndReportsSigningErrors(t *testing.T) {
	r := newRig(t, domain.Thumbnails{})
	r.add(t, uploaded("an", "0001", "image/png", time.Hour))
	r.ready = false
	if _, err := r.svc.List(t.Context(), an, gallery.Input{PageSize: 10}); !errors.Is(err, apperr.ErrUnavailable) {
		t.Fatalf("not ready err = %v", err)
	}
	r.ready = true
	r.objects.err = errors.New("no credentials")
	if _, err := r.svc.List(t.Context(), an, gallery.Input{PageSize: 10}); !errors.Is(err, apperr.ErrUnavailable) {
		t.Fatalf("sign err = %v", err)
	}
}
