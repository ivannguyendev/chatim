package storetest

import (
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/ivannguyendev/chatim/modules/media/internal/domain"
	"github.com/ivannguyendev/chatim/modules/media/internal/store"
)

var (
	attached = created.Add(time.Hour)
	later    = created.Add(2 * time.Hour)
)

func moveCases() []uploadCase {
	return []uploadCase{
		{"get many reads every batch of the tenant's keys", getManyAcrossBatches},
		{"get many of no keys is empty", getManyEmpty},
		{"get many reads the location fields", getManyLocation},
		{"mark moved sets the bucket and the first attach time", markMovedOnce},
		{"mark moved with a stale version conflicts", markMovedStale},
		{"retire keeps the first mark and unretire clears it", retireKeepsFirstMark},
		{"retire and unretire leave the version alone", retireKeepsVersion},
	}
}

func getManyAcrossBatches(t *testing.T, s store.Uploads) {
	var keys []string
	for i := range 450 {
		u := Uploaded("acme", "an", fmt.Sprintf("%04d", i), "image/png")
		insertAll(t, s, u)
		keys = append(keys, u.Key)
	}
	var foreign []string
	for i := range 3 {
		u := Uploaded("other", "an", fmt.Sprintf("%04d", i), "image/png")
		insertAll(t, s, u)
		foreign = append(foreign, u.Key)
	}
	asked := append(append(append([]string{}, keys...), foreign...), "acme/an/20261107/none", keys[0], keys[449])
	got, err := s.GetMany(t.Context(), "acme", asked)
	if err != nil || len(got) != len(keys) {
		t.Fatalf("got %d uploads, %v", len(got), err)
	}
	for _, k := range keys {
		if u, ok := got[k]; !ok || u.Key != k || u.Tenant != "acme" {
			t.Fatalf("key %s = %+v, %v", k, u, ok)
		}
	}
	other, err := s.GetMany(t.Context(), "other", keys[:10])
	if err != nil || len(other) != 0 {
		t.Fatalf("another tenant read %d of acme's uploads, %v", len(other), err)
	}
}

func getManyEmpty(t *testing.T, s store.Uploads) {
	got, err := s.GetMany(t.Context(), "acme", nil)
	if err != nil || len(got) != 0 {
		t.Fatalf("got %v, %v", got, err)
	}
}

func getManyLocation(t *testing.T, s store.Uploads) {
	u := Uploaded("acme", "an", "0001", "video/mp4")
	u.Bucket, u.AttachedAt, u.RetiredAt = domain.BucketCompleted, attached, later
	plain := Uploaded("acme", "an", "0002", "image/png")
	insertAll(t, s, u, plain)
	got, err := s.GetMany(t.Context(), "acme", []string{u.Key, plain.Key})
	if err != nil {
		t.Fatal(err)
	}
	g := got[u.Key]
	if g.Tenant != u.Tenant || g.User != u.User || g.ContentType != u.ContentType || g.Size != u.Size || g.State != u.State || g.Ver != u.Ver {
		t.Fatalf("facts = %+v", g)
	}
	if g.Bucket != domain.BucketCompleted || !g.AttachedAt.Equal(attached) || !g.RetiredAt.Equal(later) || !g.CreatedAt.Equal(created) {
		t.Fatalf("location = %q %v %v %v", g.Bucket, g.AttachedAt, g.RetiredAt, g.CreatedAt)
	}
	if p := got[plain.Key]; p.Bucket != "" || !p.AttachedAt.IsZero() || !p.RetiredAt.IsZero() {
		t.Fatalf("plain upload location = %q %v %v", p.Bucket, p.AttachedAt, p.RetiredAt)
	}
}

func markMovedOnce(t *testing.T, s store.Uploads) {
	fresh := Uploaded("acme", "an", "0001", "image/png")
	seen := Uploaded("acme", "an", "0002", "image/png")
	seen.AttachedAt = attached
	insertAll(t, s, fresh, seen)
	for _, u := range []domain.Upload{fresh, seen} {
		if err := s.MarkMoved(t.Context(), u.Key, u.Ver, later); err != nil {
			t.Fatal(err)
		}
	}
	got, err := s.Get(t.Context(), fresh.Key)
	if err != nil || got.Bucket != domain.BucketCompleted || got.Ver != fresh.Ver+1 || !got.AttachedAt.Equal(later) || !got.UpdatedAt.Equal(later) {
		t.Fatalf("fresh after move = %+v, %v", got, err)
	}
	got, err = s.Get(t.Context(), seen.Key)
	if err != nil || got.Bucket != domain.BucketCompleted || !got.AttachedAt.Equal(attached) {
		t.Fatalf("seen after move = %+v, %v", got, err)
	}
}

func markMovedStale(t *testing.T, s store.Uploads) {
	u := Uploaded("acme", "an", "0001", "image/png")
	insertAll(t, s, u)
	if err := s.MarkMoved(t.Context(), u.Key, u.Ver+1, later); !errors.Is(err, domain.ErrRetryLater) {
		t.Fatalf("stale version err = %v", err)
	}
	if err := s.MarkMoved(t.Context(), "acme/an/20261107/none", 1, later); !errors.Is(err, domain.ErrRetryLater) {
		t.Fatalf("missing key err = %v", err)
	}
	got, err := s.Get(t.Context(), u.Key)
	if err != nil || got.Bucket != "" || got.Ver != u.Ver || !got.AttachedAt.IsZero() {
		t.Fatalf("doc changed: %+v, %v", got, err)
	}
	if err := s.MarkMoved(t.Context(), u.Key, u.Ver, later); err != nil {
		t.Fatal(err)
	}
	if err := s.MarkMoved(t.Context(), u.Key, u.Ver, later); !errors.Is(err, domain.ErrRetryLater) {
		t.Fatalf("second move with the old version err = %v", err)
	}
}

func retireKeepsFirstMark(t *testing.T, s store.Uploads) {
	a := Uploaded("acme", "an", "0001", "image/png")
	b := Uploaded("acme", "an", "0002", "image/png")
	insertAll(t, s, a, b)
	keys := []string{a.Key, b.Key, "acme/an/20261107/none"}
	if err := s.Retire(t.Context(), keys, attached); err != nil {
		t.Fatal(err)
	}
	if err := s.Retire(t.Context(), keys, later); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{a.Key, b.Key} {
		if got, err := s.Get(t.Context(), k); err != nil || !got.RetiredAt.Equal(attached) {
			t.Fatalf("%s retired at %v, %v", k, got.RetiredAt, err)
		}
	}
	for range 2 {
		if err := s.Unretire(t.Context(), []string{a.Key}); err != nil {
			t.Fatal(err)
		}
	}
	if got, err := s.Get(t.Context(), a.Key); err != nil || !got.RetiredAt.IsZero() {
		t.Fatalf("unretired %v, %v", got.RetiredAt, err)
	}
	if got, err := s.Get(t.Context(), b.Key); err != nil || !got.RetiredAt.Equal(attached) {
		t.Fatalf("untouched key retired at %v, %v", got.RetiredAt, err)
	}
}

func retireKeepsVersion(t *testing.T, s store.Uploads) {
	u := Uploaded("acme", "an", "0001", "image/png")
	insertAll(t, s, u)
	if err := s.Retire(t.Context(), []string{u.Key}, attached); err != nil {
		t.Fatal(err)
	}
	if err := s.Unretire(t.Context(), []string{u.Key}); err != nil {
		t.Fatal(err)
	}
	if err := s.MarkMoved(t.Context(), u.Key, u.Ver, later); err != nil {
		t.Fatalf("move after retire and unretire: %v", err)
	}
}
