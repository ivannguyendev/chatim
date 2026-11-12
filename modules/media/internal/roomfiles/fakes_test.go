package roomfiles_test

import (
	"context"
	"fmt"
	"maps"
	"testing"
	"time"

	"github.com/ivannguyendev/chatim/modules/media/internal/domain"
	"github.com/ivannguyendev/chatim/modules/media/internal/roomfiles"
	"github.com/ivannguyendev/chatim/modules/media/internal/store/memstore"
)

const (
	room  = "77"
	photo = "acme/binh/20261111/0123456789abcdefghijklmnop"
	clip  = "acme/binh/20261111/0123456789abcdefghijklmnoq"
	other = "acme/binh/20261111/0123456789abcdefghijklmnor"
	ttl   = time.Hour
)

var (
	now = time.Date(2026, 11, 11, 10, 0, 0, 0, time.UTC)
	an  = domain.Caller{Tenant: "acme", User: "an"}
)

func sentAt(minute int) time.Time { return now.Add(time.Duration(minute-60) * time.Minute) }

type fakeChat struct {
	access      domain.RoomAccess
	accessErr   error
	accessCalls int
	messages    map[string]domain.MessageFiles
	asked       [][]domain.MessageRef
}

func (c *fakeChat) RoomReadAccess(_ context.Context, tenant, user, room string) (domain.RoomAccess, error) {
	c.accessCalls++
	if tenant != an.Tenant || user != an.User {
		return domain.RoomAccess{}, fmt.Errorf("asked as %s/%s for %s", tenant, user, room)
	}
	return c.access, c.accessErr
}

func (c *fakeChat) VisibleAttachments(_ context.Context, tenant, user string, refs []domain.MessageRef) ([]domain.MessageFiles, error) {
	if tenant != an.Tenant || user != an.User {
		return nil, fmt.Errorf("asked as %s/%s", tenant, user)
	}
	c.asked = append(c.asked, refs)
	var out []domain.MessageFiles
	for _, r := range refs {
		if m, ok := c.messages[r.Key()]; ok {
			out = append(out, m)
		}
	}
	return out, nil
}

type recordingFiles struct {
	*memstore.MessageFiles
	queries []domain.RoomFilesQuery
}

func (f *recordingFiles) ListRoom(ctx context.Context, q domain.RoomFilesQuery) ([]domain.MessageFile, error) {
	f.queries = append(f.queries, q)
	return f.MessageFiles.ListRoom(ctx, q)
}

type fakeUploads struct{ docs map[string]domain.Upload }

func (u fakeUploads) GetMany(_ context.Context, tenant string, keys []string) (map[string]domain.Upload, error) {
	out := map[string]domain.Upload{}
	for _, k := range keys {
		if d, ok := u.docs[k]; ok && d.Tenant == tenant {
			out[k] = d
		}
	}
	return maps.Clone(out), nil
}

type fakeObjects struct{}

func (fakeObjects) SignGet(_ context.Context, bucket domain.Bucket, key string, _ time.Duration) (string, error) {
	return "https://s3/" + string(bucket) + "/" + key, nil
}

type rig struct {
	chat  *fakeChat
	files *recordingFiles
	ready bool
	svc   *roomfiles.Service
}

func newRig(t *testing.T) *rig {
	t.Helper()
	r := &rig{
		chat:  &fakeChat{access: domain.RoomAccess{Room: room}, messages: map[string]domain.MessageFiles{}},
		files: &recordingFiles{MessageFiles: memstore.NewMessageFiles()},
		ready: true,
	}
	uploads := fakeUploads{docs: map[string]domain.Upload{photo: {Key: photo, Tenant: "acme", Bucket: domain.BucketCompleted}}}
	svc, err := roomfiles.New(roomfiles.Config{SignatureTTL: ttl, Thumbnails: domain.Thumbnails{Enabled: true, Suffix: ".thumb"}}, roomfiles.Deps{
		Chat: r.chat, Files: r.files, Uploads: uploads, Objects: fakeObjects{},
		Ready: func() bool { return r.ready }, Now: func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}
	r.svc = svc
	return r
}

func ref(seq uint64) domain.MessageRef { return domain.MessageRef{Room: room, Seq: seq} }

func (r *rig) sent(t *testing.T, seq uint64, minute int, visible bool, files ...domain.File) {
	t.Helper()
	m := domain.MessageFiles{Ref: ref(seq), Sender: "binh", SentAt: sentAt(minute), Files: files}
	pairs := make([]domain.MessageFile, len(files))
	for i, f := range files {
		pairs[i] = domain.MessageFile{
			MessageKey: m.Ref.Key(), FileKey: f.Key, Tenant: "acme", Room: room,
			Kind: domain.RoomFileKindOf(f.Mime), SentAt: m.SentAt, CreatedAt: now,
		}
	}
	if err := r.files.Attach(t.Context(), pairs); err != nil {
		t.Fatal(err)
	}
	if visible {
		r.chat.messages[m.Ref.Key()] = m
	}
}

func png(key string) domain.File {
	return domain.File{Key: key, Name: "kệ.png", Mime: "image/png", Size: 9, Dim: []int32{640, 480}, Hash: "sha256:ab"}
}

func pdf(key string) domain.File {
	return domain.File{Key: key, Name: "báo giá.pdf", Mime: "application/pdf", Size: 3}
}

func (r *rig) list(t *testing.T, in roomfiles.Input) roomfiles.Page {
	t.Helper()
	page, err := r.svc.List(t.Context(), an, in)
	if err != nil {
		t.Fatal(err)
	}
	return page
}

func media(pageSize int) roomfiles.Input {
	return roomfiles.Input{Room: room, Kind: domain.RoomFileMedia, PageSize: pageSize}
}
