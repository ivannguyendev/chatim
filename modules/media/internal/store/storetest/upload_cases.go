package storetest

import (
	"errors"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ivannguyendev/chatim/modules/media/internal/domain"
	"github.com/ivannguyendev/chatim/modules/media/internal/store"
)

var (
	created = time.UnixMilli(1_790_000_000_000).UTC()
	hash    = "sha256:" + strings.Repeat("9f86d081", 8)
)

func Upload(key string) domain.Upload {
	return domain.Upload{
		Key: key, Tenant: "acme", User: "an", FileName: "clip.mp4", ContentType: "video/mp4",
		Size: 40 << 20, PartSize: 16 << 20, PartCount: 3, UploadID: "upload-" + key,
		State: domain.StateUploading, Ver: 1, CreatedAt: created, UpdatedAt: created,
	}
}

func RunUploads(t *testing.T, open func(t *testing.T) store.Uploads) {
	cases := []struct {
		name string
		run  func(t *testing.T, s store.Uploads)
	}{
		{"insert then get returns the same upload", insertThenGet},
		{"insert without file facts reads back without them", insertWithoutFacts},
		{"get of a missing key is not found", getMissing},
		{"insert of a taken key fails and keeps the first", insertTwice},
		{"transition moves state and version", transitionMoves},
		{"transition from a stale state or version conflicts", transitionStale},
		{"concurrent transitions let exactly one win", transitionRace},
		{"transition to uploaded writes the hash", transitionWritesHash},
		{"transition with an empty hash or to aborted writes no hash", transitionSkipsHash},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) { c.run(t, open(t)) })
	}
}

func insertThenGet(t *testing.T, s store.Uploads) {
	want := Upload("acme/an/20261106/aaaa")
	want.Dim, want.Dur = []int32{1920, 1080}, 95400
	if err := s.Insert(t.Context(), want); err != nil {
		t.Fatal(err)
	}
	got, err := s.Get(t.Context(), want.Key)
	if err != nil {
		t.Fatal(err)
	}
	if !got.CreatedAt.Equal(want.CreatedAt) || !got.UpdatedAt.Equal(want.UpdatedAt) || !got.CompletedAt.IsZero() {
		t.Fatalf("times = %v %v %v", got.CreatedAt, got.UpdatedAt, got.CompletedAt)
	}
	got.CreatedAt, got.UpdatedAt = want.CreatedAt, want.UpdatedAt
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v\nwant %+v", got, want)
	}
}

func getMissing(t *testing.T, s store.Uploads) {
	if _, err := s.Get(t.Context(), "acme/an/20261106/none"); !errors.Is(err, domain.ErrUploadNotFound) {
		t.Fatalf("err = %v", err)
	}
}

func insertTwice(t *testing.T, s store.Uploads) {
	first := Upload("acme/an/20261106/bbbb")
	if err := s.Insert(t.Context(), first); err != nil {
		t.Fatal(err)
	}
	second := first
	second.FileName = "other.mp4"
	if err := s.Insert(t.Context(), second); !errors.Is(err, domain.ErrRetryLater) {
		t.Fatalf("second insert err = %v", err)
	}
	got, err := s.Get(t.Context(), first.Key)
	if err != nil || got.FileName != first.FileName {
		t.Fatalf("got %q, %v", got.FileName, err)
	}
}

func transitionMoves(t *testing.T, s store.Uploads) {
	u := Upload("acme/an/20261106/cccc")
	if err := s.Insert(t.Context(), u); err != nil {
		t.Fatal(err)
	}
	got, err := s.Transition(t.Context(), u.Key, domain.StateUploading, 1, domain.StateUploaded, "")
	if err != nil {
		t.Fatal(err)
	}
	if got.State != domain.StateUploaded || got.Ver != 2 || got.CompletedAt.IsZero() || !got.UpdatedAt.After(u.UpdatedAt) {
		t.Fatalf("after transition %+v", got)
	}
	read, err := s.Get(t.Context(), u.Key)
	if err != nil || read.State != domain.StateUploaded || read.Ver != 2 {
		t.Fatalf("read back %+v, %v", read, err)
	}
	aborted, err := s.Transition(t.Context(), "acme/an/20261106/dddd", domain.StateUploading, 1, domain.StateAborted, "")
	if !errors.Is(err, domain.ErrRetryLater) || aborted.Key != "" {
		t.Fatalf("missing key transition = %+v, %v", aborted, err)
	}
}

func transitionStale(t *testing.T, s store.Uploads) {
	u := Upload("acme/an/20261106/eeee")
	if err := s.Insert(t.Context(), u); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Transition(t.Context(), u.Key, domain.StateUploading, 2, domain.StateUploaded, hash); !errors.Is(err, domain.ErrRetryLater) {
		t.Fatalf("stale version err = %v", err)
	}
	if _, err := s.Transition(t.Context(), u.Key, domain.StateUploaded, 1, domain.StateAborted, ""); !errors.Is(err, domain.ErrRetryLater) {
		t.Fatalf("stale state err = %v", err)
	}
	got, err := s.Get(t.Context(), u.Key)
	if err != nil || got.State != domain.StateUploading || got.Ver != 1 || !got.CompletedAt.IsZero() || got.Hash != "" {
		t.Fatalf("doc changed: %+v, %v", got, err)
	}
	aborted, err := s.Transition(t.Context(), u.Key, domain.StateUploading, 1, domain.StateAborted, "")
	if err != nil || aborted.State != domain.StateAborted || !aborted.CompletedAt.IsZero() {
		t.Fatalf("abort = %+v, %v", aborted, err)
	}
}

func transitionRace(t *testing.T, s store.Uploads) {
	u := Upload("acme/an/20261106/ffff")
	if err := s.Insert(t.Context(), u); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	results := make(chan error, 8)
	for i := range 8 {
		to := domain.StateUploaded
		if i%2 == 1 {
			to = domain.StateAborted
		}
		wg.Go(func() {
			_, err := s.Transition(t.Context(), u.Key, domain.StateUploading, 1, to, hash)
			results <- err
		})
	}
	wg.Wait()
	close(results)
	wins := 0
	for err := range results {
		switch {
		case err == nil:
			wins++
		case !errors.Is(err, domain.ErrRetryLater):
			t.Fatalf("loser err = %v", err)
		}
	}
	if wins != 1 {
		t.Fatalf("wins = %d", wins)
	}
}

func insertWithoutFacts(t *testing.T, s store.Uploads) {
	want := Upload("acme/an/20261106/gggg")
	if err := s.Insert(t.Context(), want); err != nil {
		t.Fatal(err)
	}
	got, err := s.Get(t.Context(), want.Key)
	if err != nil || got.Dim != nil || got.Dur != 0 || got.Hash != "" {
		t.Fatalf("got dim %v dur %d hash %q, %v", got.Dim, got.Dur, got.Hash, err)
	}
}

func transitionWritesHash(t *testing.T, s store.Uploads) {
	u := Upload("acme/an/20261106/hhhh")
	if err := s.Insert(t.Context(), u); err != nil {
		t.Fatal(err)
	}
	got, err := s.Transition(t.Context(), u.Key, domain.StateUploading, 1, domain.StateUploaded, hash)
	if err != nil || got.Hash != hash || got.State != domain.StateUploaded {
		t.Fatalf("transition = %+v, %v", got, err)
	}
	read, err := s.Get(t.Context(), u.Key)
	if err != nil || read.Hash != hash {
		t.Fatalf("read back hash %q, %v", read.Hash, err)
	}
}

func transitionSkipsHash(t *testing.T, s store.Uploads) {
	empty := Upload("acme/an/20261106/iiii")
	aborted := Upload("acme/an/20261106/jjjj")
	for _, u := range []domain.Upload{empty, aborted} {
		if err := s.Insert(t.Context(), u); err != nil {
			t.Fatal(err)
		}
	}
	if got, err := s.Transition(t.Context(), empty.Key, domain.StateUploading, 1, domain.StateUploaded, ""); err != nil || got.Hash != "" {
		t.Fatalf("empty hash transition = %+v, %v", got, err)
	}
	if got, err := s.Transition(t.Context(), aborted.Key, domain.StateUploading, 1, domain.StateAborted, hash); err != nil || got.Hash != "" {
		t.Fatalf("abort with a hash = %+v, %v", got, err)
	}
}
