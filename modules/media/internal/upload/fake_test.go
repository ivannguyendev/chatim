package upload_test

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/ivannguyendev/chatim/modules/media/internal/domain"
	"github.com/ivannguyendev/chatim/modules/media/internal/objectstore"
	"github.com/ivannguyendev/chatim/modules/media/internal/store/memstore"
	"github.com/ivannguyendev/chatim/modules/media/internal/upload"
)

const mib = int64(1) << 20

var (
	now  = time.Date(2026, 11, 6, 10, 0, 0, 0, time.UTC)
	an   = upload.Caller{Tenant: "acme", User: "an"}
	binh = upload.Caller{Tenant: "acme", User: "binh"}
)

type signed struct {
	number int32
	size   int64
}

type fakeObjects struct {
	mu          sync.Mutex
	calls       int
	contentType string
	open        map[string]bool
	objects     map[string]int64
	signed      []signed
	completes   int
	deletes     int
	aborts      int
	startErr    error
	completeErr error
	headErr     error
	sizeDelta   int64
}

func newFakeObjects() *fakeObjects {
	return &fakeObjects{open: map[string]bool{}, objects: map[string]int64{}}
}

func (f *fakeObjects) StartMultipart(_ context.Context, key, contentType string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	f.contentType = contentType
	if f.startErr != nil {
		return "", f.startErr
	}
	f.open["id-"+key] = true
	return "id-" + key, nil
}

func (f *fakeObjects) SignPart(_ context.Context, key, id string, n int32, size int64, _ time.Duration) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	f.signed = append(f.signed, signed{number: n, size: size})
	return fmt.Sprintf("https://s3/%s?uploadId=%s&partNumber=%d", key, id, n), nil
}

func (f *fakeObjects) Complete(_ context.Context, key, id string, parts []domain.UploadedPart) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	f.completes++
	if f.completeErr != nil {
		return f.completeErr
	}
	if !f.open[id] {
		return objectstore.ErrNoSuchUpload
	}
	delete(f.open, id)
	f.objects[key] = int64(len(parts))
	return nil
}

func (f *fakeObjects) Abort(_ context.Context, _, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	f.aborts++
	if !f.open[id] {
		return objectstore.ErrNoSuchUpload
	}
	delete(f.open, id)
	return nil
}

func (f *fakeObjects) Head(_ context.Context, key string) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	if f.headErr != nil {
		return 0, f.headErr
	}
	if _, ok := f.objects[key]; !ok {
		return 0, objectstore.ErrNotFound
	}
	return 40*mib + f.sizeDelta, nil
}

func (f *fakeObjects) Delete(_ context.Context, key string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	f.deletes++
	delete(f.objects, key)
	return nil
}

func (f *fakeObjects) set(fn func(*fakeObjects)) {
	f.mu.Lock()
	defer f.mu.Unlock()
	fn(f)
}

type rig struct {
	svc     *upload.Service
	uploads *memstore.Uploads
	objects *fakeObjects
	ready   bool
}

func newRig(t *testing.T) *rig {
	t.Helper()
	r := &rig{uploads: memstore.NewUploads(), objects: newFakeObjects(), ready: true}
	serial := byte(0)
	svc, err := upload.New(upload.Config{
		Limits:     domain.Limits{MinPartBytes: 16 * mib, MaxPartBytes: 128 * mib, MaxParts: 2000, MaxParallel: 4, MaxFileBytes: 4 << 30},
		PartURLTTL: 15 * time.Minute,
	}, upload.Deps{
		Uploads: r.uploads, Objects: r.objects,
		Ready:  func() bool { return r.ready },
		Now:    func() time.Time { return now },
		Random: func() [10]byte { serial++; return [10]byte{serial} },
	})
	if err != nil {
		t.Fatal(err)
	}
	r.svc = svc
	return r
}

func (r *rig) start(t *testing.T) upload.Started {
	t.Helper()
	s, err := r.svc.Start(t.Context(), an, upload.StartInput{FileName: "clip.mp4", ContentType: "video/mp4", Size: 40 * mib})
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func etags(n int) []domain.UploadedPart {
	out := make([]domain.UploadedPart, n)
	for i := range out {
		out[i] = domain.UploadedPart{Number: int32(i + 1), ETag: fmt.Sprintf(`"%d"`, i+1)}
	}
	return out
}

func (f *fakeObjects) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}
