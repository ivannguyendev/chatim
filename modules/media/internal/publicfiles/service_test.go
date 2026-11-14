package publicfiles_test

import (
	"context"
	"errors"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ivannguyendev/chatim/modules/media/internal/domain"
	"github.com/ivannguyendev/chatim/modules/media/internal/download"
	"github.com/ivannguyendev/chatim/modules/media/internal/publicfiles"
	"github.com/ivannguyendev/chatim/modules/media/internal/store/memstore"
	"github.com/ivannguyendev/chatim/pkg/core/apperr"
)

var (
	now  = time.Date(2026, 11, 13, 10, 0, 0, 0, time.UTC)
	binh = domain.Caller{Tenant: "acme", User: "binh"}
)

type fakeObjects struct {
	mu     sync.Mutex
	signed []string
	err    error
}

func (f *fakeObjects) SignGet(_ context.Context, bucket domain.Bucket, key string, _ time.Duration) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.signed = append(f.signed, string(bucket)+":"+key)
	return "https://s3/" + string(bucket) + "/" + key, f.err
}

type spyUploads struct {
	*memstore.Uploads
	reads int
	lie   bool
}

func (s *spyUploads) GetMany(ctx context.Context, tenant string, keys []string) (map[string]domain.Upload, error) {
	s.reads++
	docs, err := s.Uploads.GetMany(ctx, tenant, keys)
	if s.lie {
		for k, u := range docs {
			u.Tenant = "other"
			docs[k] = u
		}
	}
	return docs, err
}

type rig struct {
	svc     *publicfiles.Service
	uploads *spyUploads
	objects *fakeObjects
	ready   bool
}

func newRig(t *testing.T, thumbs bool) *rig {
	t.Helper()
	r := &rig{uploads: &spyUploads{Uploads: memstore.NewUploads()}, objects: &fakeObjects{}, ready: true}
	svc, err := publicfiles.New(publicfiles.Config{SignatureTTL: time.Hour, Thumbnails: domain.Thumbnails{Enabled: thumbs, Suffix: ".thumb"}}, publicfiles.Deps{
		Uploads: r.uploads, Objects: r.objects, Ready: func() bool { return r.ready }, Now: func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}
	r.svc = svc
	return r
}

func key(tenant string, public bool, seed byte) string {
	return domain.NewObjectKey(tenant, "an", public, now.Add(-time.Hour), [10]byte{seed})
}

func (r *rig) add(t *testing.T, k string, state domain.State, bucket domain.Bucket) {
	t.Helper()
	tenant, _, _ := strings.Cut(k, "/")
	u := domain.Upload{Key: k, Tenant: tenant, User: "an", ContentType: "image/png", Size: 9, State: state, Ver: 2, CreatedAt: now.Add(-time.Hour), Bucket: bucket}
	if err := r.uploads.Insert(t.Context(), u); err != nil {
		t.Fatal(err)
	}
}

func keysOf(files []download.FileURL) []string {
	out := make([]string, len(files))
	for i, f := range files {
		out[i] = f.Key
	}
	return out
}

func TestURLsSignOnlyUploadedPublicFilesOfTheCallersTenant(t *testing.T) {
	r := newRig(t, true)
	fresh, moved, private, foreign, open := key("acme", true, 1), key("acme", true, 2), key("acme", false, 3), key("other", true, 4), key("acme", true, 5)
	r.add(t, fresh, domain.StateUploaded, "")
	r.add(t, moved, domain.StateUploaded, domain.BucketCompleted)
	r.add(t, private, domain.StateUploaded, "")
	r.add(t, foreign, domain.StateUploaded, "")
	r.add(t, open, domain.StateUploading, "")
	got, err := r.svc.URLs(t.Context(), binh, []string{fresh, moved, private, foreign, open, key("acme", true, 6), fresh})
	if err != nil || !slices.Equal(keysOf(got.Files), []string{fresh, moved}) || !got.ExpiresAt.Equal(now.Add(time.Hour)) {
		t.Fatalf("URLs = %+v, %v", got, err)
	}
	if f := got.Files[1]; f.URL != "https://s3/completed/"+moved || f.ThumbnailURL != "https://s3/completed/"+moved+".thumb" {
		t.Fatalf("moved file %+v", f)
	}
	want := []string{"processing:" + fresh, "processing:" + fresh + ".thumb", "completed:" + moved, "completed:" + moved + ".thumb"}
	if !slices.Equal(r.objects.signed, want) {
		t.Fatalf("signed %v, want %v", r.objects.signed, want)
	}
}

func TestURLsWithoutPublicKeysOfTheTenantReadNothing(t *testing.T) {
	r := newRig(t, false)
	got, err := r.svc.URLs(t.Context(), binh, []string{key("acme", false, 1), key("other", true, 2)})
	if err != nil || len(got.Files) != 0 || r.uploads.reads != 0 {
		t.Fatalf("URLs = %+v, %v after %d reads", got, err, r.uploads.reads)
	}
}

func TestURLsSkipADocOfAnotherTenant(t *testing.T) {
	r := newRig(t, false)
	k := key("acme", true, 1)
	r.add(t, k, domain.StateUploaded, "")
	r.uploads.lie = true
	if got, err := r.svc.URLs(t.Context(), binh, []string{k}); err != nil || len(got.Files) != 0 {
		t.Fatalf("URLs = %+v, %v", got, err)
	}
}

func TestURLsRefuseBadInput(t *testing.T) {
	r := newRig(t, false)
	many := make([]string, publicfiles.MaxKeys+1)
	for i := range many {
		many[i] = key("acme", true, byte(i))
	}
	for name, keys := range map[string][]string{"none": nil, "too many": many, "not a key": {"acme/an/pub/x"}} {
		if _, err := r.svc.URLs(t.Context(), binh, keys); !errors.Is(err, apperr.ErrInvalidArgument) {
			t.Fatalf("%s: %v", name, err)
		}
	}
	r.ready = false
	if _, err := r.svc.URLs(t.Context(), binh, []string{key("acme", true, 1)}); !errors.Is(err, apperr.ErrUnavailable) {
		t.Fatalf("not ready: %v", err)
	}
}

func TestASigningFailureAsksToRetry(t *testing.T) {
	r := newRig(t, false)
	k := key("acme", true, 1)
	r.add(t, k, domain.StateUploaded, "")
	r.objects.err = errors.New("s3 down")
	if _, err := r.svc.URLs(t.Context(), binh, []string{k}); !errors.Is(err, domain.ErrRetryLater) {
		t.Fatalf("err = %v", err)
	}
}

func TestNewNeedsUploadsObjectsAndALifetime(t *testing.T) {
	for name, c := range map[string]struct {
		cfg  publicfiles.Config
		deps publicfiles.Deps
	}{
		"no uploads":  {publicfiles.Config{SignatureTTL: time.Hour}, publicfiles.Deps{Objects: &fakeObjects{}}},
		"no objects":  {publicfiles.Config{SignatureTTL: time.Hour}, publicfiles.Deps{Uploads: memstore.NewUploads()}},
		"no lifetime": {publicfiles.Config{}, publicfiles.Deps{Uploads: memstore.NewUploads(), Objects: &fakeObjects{}}},
	} {
		if _, err := publicfiles.New(c.cfg, c.deps); err == nil {
			t.Fatalf("%s accepted", name)
		}
	}
}
