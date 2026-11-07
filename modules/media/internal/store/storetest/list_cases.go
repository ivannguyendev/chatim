package storetest

import (
	"fmt"
	"slices"
	"testing"

	"github.com/ivannguyendev/chatim/modules/media/internal/domain"
	"github.com/ivannguyendev/chatim/modules/media/internal/store"
)

func Uploaded(tenant, user, random, mime string) domain.Upload {
	u := Upload(tenant + "/" + user + "/20261107/" + random)
	u.Tenant, u.User, u.ContentType, u.State, u.Ver = tenant, user, mime, domain.StateUploaded, 2
	return u
}

func insertAll(t *testing.T, s store.Uploads, uploads ...domain.Upload) {
	t.Helper()
	for _, u := range uploads {
		if err := s.Insert(t.Context(), u); err != nil {
			t.Fatal(err)
		}
	}
}

func keysOf(t *testing.T, s store.Uploads, before string, kinds domain.Kinds, limit int) []string {
	t.Helper()
	got, err := s.ListOwned(t.Context(), "acme", "an", before, kinds, limit)
	if err != nil {
		t.Fatal(err)
	}
	keys := make([]string, len(got))
	for i, u := range got {
		if u.Tenant != "acme" || u.User != "an" || u.State != domain.StateUploaded {
			t.Fatalf("listed %+v", u)
		}
		keys[i] = u.Key
	}
	return keys
}

func listOnlyOwner(t *testing.T, s store.Uploads) {
	mine := Uploaded("acme", "an", "0001", "image/png")
	insertAll(t, s, mine,
		Uploaded("acme", "an2", "0002", "image/png"),
		Uploaded("acme", "an-b", "0003", "image/png"),
		Uploaded("other", "an", "0004", "image/png"),
	)
	if got := keysOf(t, s, "", domain.Kinds{}, 10); !slices.Equal(got, []string{mine.Key}) {
		t.Fatalf("listed %v", got)
	}
}

func listOnlyUploaded(t *testing.T, s store.Uploads) {
	done := Uploaded("acme", "an", "0001", "image/png")
	open := Uploaded("acme", "an", "0002", "image/png")
	open.State, open.Ver = domain.StateUploading, 1
	aborted := Uploaded("acme", "an", "0003", "image/png")
	aborted.State = domain.StateAborted
	insertAll(t, s, done, open, aborted)
	if got := keysOf(t, s, "", domain.Kinds{}, 10); !slices.Equal(got, []string{done.Key}) {
		t.Fatalf("listed %v", got)
	}
}

func listNewestFirstByPages(t *testing.T, s store.Uploads) {
	var want []string
	for i := 5; i >= 1; i-- {
		u := Uploaded("acme", "an", fmt.Sprintf("%04d", i), "image/png")
		insertAll(t, s, u)
		want = append(want, u.Key)
	}
	slices.Sort(want)
	slices.Reverse(want)
	var got []string
	before := ""
	for page := range 3 {
		keys := keysOf(t, s, before, domain.Kinds{}, 2)
		if wantLen := []int{2, 2, 1}[page]; len(keys) != wantLen {
			t.Fatalf("page %d = %v", page, keys)
		}
		got = append(got, keys...)
		before = keys[len(keys)-1]
	}
	if !slices.Equal(got, want) {
		t.Fatalf("pages %v, want %v", got, want)
	}
}

func listByKinds(t *testing.T, s store.Uploads) {
	image := Uploaded("acme", "an", "0004", "image/png")
	video := Uploaded("acme", "an", "0003", "video/mp4")
	pdf := Uploaded("acme", "an", "0002", "application/pdf")
	text := Uploaded("acme", "an", "0001", "text/plain")
	insertAll(t, s, image, video, pdf, text)
	cases := []struct {
		kinds domain.Kinds
		want  []string
	}{
		{domain.Kinds{}, []string{image.Key, video.Key, pdf.Key, text.Key}},
		{domain.Kinds{Image: true, Video: true, Other: true}, []string{image.Key, video.Key, pdf.Key, text.Key}},
		{domain.Kinds{Image: true}, []string{image.Key}},
		{domain.Kinds{Video: true}, []string{video.Key}},
		{domain.Kinds{Other: true}, []string{pdf.Key, text.Key}},
		{domain.Kinds{Image: true, Video: true}, []string{image.Key, video.Key}},
		{domain.Kinds{Image: true, Other: true}, []string{image.Key, pdf.Key, text.Key}},
		{domain.Kinds{Video: true, Other: true}, []string{video.Key, pdf.Key, text.Key}},
	}
	for _, c := range cases {
		if got := keysOf(t, s, "", c.kinds, 10); !slices.Equal(got, c.want) {
			t.Fatalf("%+v listed %v, want %v", c.kinds, got, c.want)
		}
	}
}
