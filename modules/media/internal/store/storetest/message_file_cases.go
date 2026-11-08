package storetest

import (
	"maps"
	"slices"
	"testing"

	"github.com/ivannguyendev/chatim/modules/media/internal/domain"
	"github.com/ivannguyendev/chatim/modules/media/internal/store"
)

const (
	firstMessage  = "8812-0-42"
	secondMessage = "9001-0-7"
	fileX         = "acme/an/20261108/xxxx"
	fileY         = "acme/an/20261108/yyyy"
	fileZ         = "acme/an/20261108/zzzz"
	foreignFile   = "other/an/20261108/wwww"
)

type Stores struct {
	Files   store.MessageFiles
	Uploads store.Uploads
}

func Pair(tenant, messageKey, fileKey string) domain.MessageFile {
	return domain.MessageFile{MessageKey: messageKey, FileKey: fileKey, Tenant: tenant, State: domain.PairLive, CreatedAt: created}
}

func RunMessageFiles(t *testing.T, open func(t *testing.T) Stores) {
	cases := []struct {
		name string
		run  func(t *testing.T, s Stores)
	}{
		{"attach then active lists the message's files", attachThenActive},
		{"attach of a pair again does not revive a released pair", attachKeepsReleased},
		{"release twice keeps the pairs released", releaseTwice},
		{"still used is true while another live message holds the file", stillUsedByAnotherMessage},
		{"another tenant with the same message key sees and releases nothing", tenantIsolation},
		{"a forward after the last message retired the file clears the mark", forwardAfterRetire},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) { c.run(t, open(t)) })
	}
}

func attach(t *testing.T, s store.MessageFiles, pairs ...domain.MessageFile) {
	t.Helper()
	if err := s.Attach(t.Context(), pairs); err != nil {
		t.Fatal(err)
	}
}

func release(t *testing.T, s store.MessageFiles, tenant, messageKey string) {
	t.Helper()
	if err := s.Release(t.Context(), tenant, messageKey, attached); err != nil {
		t.Fatal(err)
	}
}

func active(t *testing.T, s store.MessageFiles, tenant, messageKey string) []string {
	t.Helper()
	keys, err := s.Active(t.Context(), tenant, messageKey)
	if err != nil {
		t.Fatal(err)
	}
	slices.Sort(keys)
	return keys
}

func stillUsed(t *testing.T, s store.MessageFiles, keys ...string) map[string]bool {
	t.Helper()
	used, err := s.StillUsed(t.Context(), keys)
	if err != nil {
		t.Fatal(err)
	}
	return used
}

func attachThenActive(t *testing.T, s Stores) {
	attach(t, s.Files)
	attach(t, s.Files, Pair("acme", firstMessage, fileY), Pair("acme", firstMessage, fileX), Pair("acme", firstMessage, fileX))
	if got := active(t, s.Files, "acme", firstMessage); !slices.Equal(got, []string{fileX, fileY}) {
		t.Fatalf("active = %v", got)
	}
	if got := active(t, s.Files, "acme", secondMessage); len(got) != 0 {
		t.Fatalf("active of another message = %v", got)
	}
}

func attachKeepsReleased(t *testing.T, s Stores) {
	attach(t, s.Files, Pair("acme", firstMessage, fileX))
	release(t, s.Files, "acme", firstMessage)
	attach(t, s.Files, Pair("acme", firstMessage, fileX))
	if got := active(t, s.Files, "acme", firstMessage); len(got) != 0 {
		t.Fatalf("released pair came back: %v", got)
	}
	if used := stillUsed(t, s.Files, fileX); used[fileX] {
		t.Fatalf("still used = %v", used)
	}
}

func releaseTwice(t *testing.T, s Stores) {
	attach(t, s.Files, Pair("acme", firstMessage, fileX), Pair("acme", firstMessage, fileY))
	release(t, s.Files, "acme", firstMessage)
	if err := s.Files.Release(t.Context(), "acme", firstMessage, later); err != nil {
		t.Fatal(err)
	}
	if err := s.Files.Release(t.Context(), "acme", secondMessage, later); err != nil {
		t.Fatal(err)
	}
	if got := active(t, s.Files, "acme", firstMessage); len(got) != 0 {
		t.Fatalf("active after release = %v", got)
	}
}

func stillUsedByAnotherMessage(t *testing.T, s Stores) {
	attach(t, s.Files, Pair("acme", firstMessage, fileX), Pair("acme", firstMessage, fileY), Pair("acme", secondMessage, fileX))
	release(t, s.Files, "acme", firstMessage)
	want := map[string]bool{fileX: true, fileY: false, fileZ: false}
	if got := stillUsed(t, s.Files, fileX, fileY, fileZ); !maps.Equal(got, want) {
		t.Fatalf("still used = %v, want %v", got, want)
	}
	release(t, s.Files, "acme", secondMessage)
	if got := stillUsed(t, s.Files, fileX); got[fileX] {
		t.Fatalf("still used after the last release = %v", got)
	}
	if got := stillUsed(t, s.Files); len(got) != 0 {
		t.Fatalf("still used of no keys = %v", got)
	}
}

func tenantIsolation(t *testing.T, s Stores) {
	attach(t, s.Files, Pair("acme", firstMessage, fileX), Pair("other", firstMessage, foreignFile))
	if got := active(t, s.Files, "other", firstMessage); !slices.Equal(got, []string{foreignFile}) {
		t.Fatalf("other tenant active = %v", got)
	}
	if got := active(t, s.Files, "other", secondMessage); len(got) != 0 {
		t.Fatalf("other tenant sees %v", got)
	}
	release(t, s.Files, "other", firstMessage)
	if got := active(t, s.Files, "acme", firstMessage); !slices.Equal(got, []string{fileX}) {
		t.Fatalf("acme active after the other tenant's release = %v", got)
	}
	if got := stillUsed(t, s.Files, fileX, foreignFile); !got[fileX] || got[foreignFile] {
		t.Fatalf("still used = %v", got)
	}
}

func forwardAfterRetire(t *testing.T, s Stores) {
	u := Uploaded("acme", "an", "xxxx", "image/png")
	insertAll(t, s.Uploads, u)
	attach(t, s.Files, Pair("acme", firstMessage, u.Key))
	release(t, s.Files, "acme", firstMessage)
	if used := stillUsed(t, s.Files, u.Key); used[u.Key] {
		t.Fatalf("still used after the last release = %v", used)
	}
	if err := s.Uploads.Retire(t.Context(), []string{u.Key}, attached); err != nil {
		t.Fatal(err)
	}
	attach(t, s.Files, Pair("acme", secondMessage, u.Key))
	if err := s.Uploads.Unretire(t.Context(), []string{u.Key}); err != nil {
		t.Fatal(err)
	}
	got, err := s.Uploads.Get(t.Context(), u.Key)
	if err != nil || !got.RetiredAt.IsZero() {
		t.Fatalf("retired at %v, %v", got.RetiredAt, err)
	}
	if used := stillUsed(t, s.Files, u.Key); !used[u.Key] {
		t.Fatalf("still used after the forward = %v", used)
	}
}
