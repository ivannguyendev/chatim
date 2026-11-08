package attach_test

import (
	"context"
	"log/slog"
	"maps"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/ivannguyendev/chatim/modules/media/internal/attach"
	"github.com/ivannguyendev/chatim/modules/media/internal/domain"
	"github.com/ivannguyendev/chatim/modules/media/internal/store/memstore"
)

const (
	tenant = "acme"
	fileX  = "acme/an/20261108/0123456789abcdefghijklmnop"
	fileY  = "acme/an/20261108/abcdefghijklmnop0123456789"
)

var now = time.Date(2026, 11, 8, 12, 0, 0, 0, time.UTC)

type fakeChat struct {
	mu     sync.Mutex
	states map[domain.MessageRef]domain.MessageState
	err    error
	calls  int
}

func (c *fakeChat) MessageFiles(_ context.Context, ref domain.MessageRef) (domain.MessageState, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.calls++
	return c.states[ref], c.err
}

func (c *fakeChat) set(ref domain.MessageRef, state domain.MessageState) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.states[ref] = state
}

type countingUploads struct {
	*memstore.Uploads
	calls        map[string]int
	conflictOnce bool
}

func (u *countingUploads) GetMany(ctx context.Context, tenant string, keys []string) (map[string]domain.Upload, error) {
	u.calls["get_many"]++
	return u.Uploads.GetMany(ctx, tenant, keys)
}

func (u *countingUploads) MarkMoved(ctx context.Context, key string, ver int64, at time.Time) error {
	u.calls["mark_moved"]++
	if u.conflictOnce {
		u.conflictOnce = false
		return domain.ErrRetryLater
	}
	return u.Uploads.MarkMoved(ctx, key, ver, at)
}

func (u *countingUploads) Retire(ctx context.Context, keys []string, at time.Time) error {
	u.calls["retire"]++
	return u.Uploads.Retire(ctx, keys, at)
}

func (u *countingUploads) Unretire(ctx context.Context, keys []string) error {
	u.calls["unretire"]++
	return u.Uploads.Unretire(ctx, keys)
}

type countingFiles struct {
	*memstore.MessageFiles
	calls map[string]int
}

func (f *countingFiles) Attach(ctx context.Context, pairs []domain.MessageFile) error {
	f.calls["attach"]++
	return f.MessageFiles.Attach(ctx, pairs)
}

func (f *countingFiles) Active(ctx context.Context, tenant, messageKey string) ([]string, error) {
	f.calls["active"]++
	return f.MessageFiles.Active(ctx, tenant, messageKey)
}

func (f *countingFiles) Release(ctx context.Context, tenant, messageKey string, at time.Time) error {
	f.calls["release"]++
	return f.MessageFiles.Release(ctx, tenant, messageKey, at)
}

func (f *countingFiles) StillUsed(ctx context.Context, fileKeys []string) (map[string]bool, error) {
	f.calls["still_used"]++
	return f.MessageFiles.StillUsed(ctx, fileKeys)
}

type fakeObjects struct {
	buckets    map[domain.Bucket]map[string]domain.Object
	copies     []string
	deletes    []string
	stats      int
	copyErr    error
	deleteErr  error
	statErr    error
	copyTarget string
}

func newObjects() *fakeObjects {
	return &fakeObjects{buckets: map[domain.Bucket]map[string]domain.Object{domain.BucketProcessing: {}, domain.BucketCompleted: {}}}
}

func (o *fakeObjects) Copy(_ context.Context, key string, size int64, contentType string) error {
	o.copies = append(o.copies, key)
	if o.copyErr != nil && (o.copyTarget == "" || o.copyTarget == key) {
		return o.copyErr
	}
	src, ok := o.buckets[domain.BucketProcessing][key]
	if !ok || src.Size != size {
		return domain.ErrRetryLater
	}
	o.buckets[domain.BucketCompleted][key] = domain.Object{Size: size, ContentType: contentType}
	return nil
}

func (o *fakeObjects) Stat(_ context.Context, bucket domain.Bucket, key string) (domain.Object, bool, error) {
	o.stats++
	if o.statErr != nil {
		return domain.Object{}, false, o.statErr
	}
	obj, ok := o.buckets[bucket][key]
	return obj, ok, nil
}

func (o *fakeObjects) DeleteProcessing(_ context.Context, key string) error {
	o.deletes = append(o.deletes, key)
	if o.deleteErr != nil {
		return o.deleteErr
	}
	delete(o.buckets[domain.BucketProcessing], key)
	return nil
}

func (o *fakeObjects) has(bucket domain.Bucket, key string) bool {
	_, ok := o.buckets[bucket][key]
	return ok
}

type rig struct {
	chat    *fakeChat
	uploads *countingUploads
	files   *countingFiles
	objects *fakeObjects
	clock   time.Time
	svc     *attach.Service
}

func newRig(t *testing.T, thumbnails bool) *rig {
	t.Helper()
	r := &rig{
		chat:    &fakeChat{states: map[domain.MessageRef]domain.MessageState{}},
		uploads: &countingUploads{Uploads: memstore.NewUploads(), calls: map[string]int{}},
		files:   &countingFiles{MessageFiles: memstore.NewMessageFiles(), calls: map[string]int{}},
		objects: newObjects(),
		clock:   now,
	}
	svc, err := attach.New(attach.Config{Thumbnails: domain.Thumbnails{Enabled: thumbnails, Suffix: ".thumb"}}, attach.Deps{
		Chat: r.chat, Uploads: r.uploads, Files: r.files, Objects: r.objects, Log: slog.New(slog.DiscardHandler), Now: func() time.Time { return r.clock },
	})
	if err != nil {
		t.Fatal(err)
	}
	r.svc = svc
	return r
}

func (r *rig) upload(t *testing.T, key, contentType string, state domain.State, created time.Time) {
	t.Helper()
	u := domain.Upload{Key: key, Tenant: tenant, User: "an", ContentType: contentType, Size: 9, State: state, Ver: 2, CreatedAt: created}
	if err := r.uploads.Insert(t.Context(), u); err != nil {
		t.Fatal(err)
	}
	if state == domain.StateUploaded {
		r.objects.buckets[domain.BucketProcessing][key] = domain.Object{Size: 9, ContentType: contentType}
	}
}

func (r *rig) uploaded(t *testing.T, keys ...string) {
	t.Helper()
	for _, k := range keys {
		r.upload(t, k, "image/png", domain.StateUploaded, now.Add(-time.Hour))
	}
}

func (r *rig) sent(ref domain.MessageRef, keys ...string) {
	r.chat.set(ref, domain.MessageState{Found: true, Tenant: tenant, Keys: keys})
}

func (r *rig) deleted(ref domain.MessageRef) {
	r.chat.set(ref, domain.MessageState{Found: true, Deleted: true, Tenant: tenant})
}

func (r *rig) created(t *testing.T, ref domain.MessageRef) error {
	t.Helper()
	return r.svc.Sync(t.Context(), attach.Created, attach.Event{Tenant: tenant, Ref: ref, HasFiles: true})
}

func (r *rig) removed(t *testing.T, ref domain.MessageRef) error {
	t.Helper()
	return r.svc.Sync(t.Context(), attach.Deleted, attach.Event{Tenant: tenant, Ref: ref})
}

func (r *rig) doc(t *testing.T, key string) domain.Upload {
	t.Helper()
	u, err := r.uploads.Get(t.Context(), key)
	if err != nil {
		t.Fatal(err)
	}
	return u
}

func (r *rig) live(t *testing.T, ref domain.MessageRef) []string {
	t.Helper()
	keys, err := r.files.MessageFiles.Active(t.Context(), tenant, ref.Key())
	if err != nil {
		t.Fatal(err)
	}
	slices.Sort(keys)
	return keys
}

func (r *rig) storeCalls() map[string]int {
	out := maps.Clone(r.uploads.calls)
	maps.Copy(out, r.files.calls)
	return out
}

func ref(room string, seq uint64) domain.MessageRef { return domain.MessageRef{Room: room, Seq: seq} }
