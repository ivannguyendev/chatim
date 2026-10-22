package wspublish_test

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/nats-io/nats.go/jetstream"

	"github.com/ivannguyendev/chatim/pkg/apperr"
	"github.com/ivannguyendev/chatim/pkg/wspublish"
)

func TestSubjectJoinsRootAndTenant(t *testing.T) {
	got, err := wspublish.Subject(wspublish.DefaultRoot, "acme")
	if err != nil || got != "websocket.publish.acme" {
		t.Fatalf("Subject = %q, %v; want websocket.publish.acme", got, err)
	}
	if got, err := wspublish.Subject("notify", "acme"); err != nil || got != "notify.acme" {
		t.Fatalf("Subject(notify) = %q, %v", got, err)
	}
}

func TestSubjectRejectsBadTokens(t *testing.T) {
	for _, tenant := range []string{"", "a.b", "a*", "a>", "a b"} {
		if _, err := wspublish.Subject(wspublish.DefaultRoot, tenant); !errors.Is(err, apperr.ErrInvalidArgument) {
			t.Errorf("Subject(tenant %q) = %v, want invalid argument", tenant, err)
		}
	}
	for _, root := range []string{"", ".x", "x.", "x..y", "x.*", "x.>", "X", "x y"} {
		if _, err := wspublish.Subject(root, "acme"); !errors.Is(err, apperr.ErrInvalidArgument) {
			t.Errorf("Subject(root %q) = %v, want invalid argument", root, err)
		}
	}
}

func TestValidKindIsOneToken(t *testing.T) {
	if err := wspublish.ValidKind("mention"); err != nil {
		t.Fatalf("ValidKind(mention) = %v", err)
	}
	for _, kind := range []string{"", "a.b", "*", ">", "a\tb"} {
		if err := wspublish.ValidKind(kind); !errors.Is(err, apperr.ErrInvalidArgument) {
			t.Errorf("ValidKind(%q) = %v, want invalid argument", kind, err)
		}
	}
}

func users(n int) []string {
	out := make([]string, n)
	for i := range out {
		out[i] = fmt.Sprintf("u%05d", n-i)
	}
	return out
}

func TestPagesAreSortedAndChunked(t *testing.T) {
	in := users(2500)
	pages := wspublish.Pages(in, 1000)
	if got := []int{len(pages[0]), len(pages[1]), len(pages[2])}; len(pages) != 3 || !slices.Equal(got, []int{1000, 1000, 500}) {
		t.Fatalf("page sizes %d pages %v, want 1000/1000/500", len(pages), got)
	}
	all := slices.Concat(pages...)
	if !slices.IsSorted(all) || len(all) != 2500 || all[0] != "u00001" {
		t.Fatalf("pages not one sorted run of every user: first %s len %d", all[0], len(all))
	}
	if in[0] != "u02500" {
		t.Fatal("Pages changed the caller's slice")
	}
	if again := wspublish.Pages(slices.Clone(in), 1000); !reflect.DeepEqual(again, pages) {
		t.Fatal("Pages is not deterministic")
	}
}

func TestPagesEdges(t *testing.T) {
	if got := wspublish.Pages(nil, 10); len(got) != 0 {
		t.Fatalf("Pages(nil) = %v, want none", got)
	}
	if got := wspublish.Pages([]string{"b", "a"}, 0); len(got) != 1 || !slices.Equal(got[0], []string{"a", "b"}) {
		t.Fatalf("Pages(size 0) = %v, want one sorted page", got)
	}
	if got := wspublish.Pages([]string{"a", "b"}, 2); len(got) != 1 {
		t.Fatalf("Pages(exact) = %v, want one page", got)
	}
}

func TestPageIDIsStableAndFollowsTheUsers(t *testing.T) {
	id := wspublish.PageID("77-0-5-m1-all", 2, []string{"an", "binh"})
	if id != wspublish.PageID("77-0-5-m1-all", 2, []string{"binh", "an"}) {
		t.Fatal("PageID depends on the order of the users")
	}
	prefix := "77-0-5-m1-all-p2-"
	if len(id) != len(prefix)+16 || id[:len(prefix)] != prefix {
		t.Fatalf("PageID = %q, want %s plus 16 hex chars", id, prefix)
	}
	if id == wspublish.PageID("77-0-5-m1-all", 2, []string{"an"}) {
		t.Fatal("PageID unchanged when a user left the page")
	}
	if id == wspublish.PageID("77-0-5-m1-all", 1, []string{"an", "binh"}) {
		t.Fatal("PageID unchanged for another part")
	}
}

func TestStreamConfig(t *testing.T) {
	got := wspublish.StreamConfig(wspublish.DefaultStream, wspublish.DefaultRoot, 3, wspublish.DefaultMaxAge, wspublish.DefaultDuplicates)
	want := jetstream.StreamConfig{
		Name: "CHATIM_WS", Subjects: []string{"websocket.publish.>"}, Retention: jetstream.LimitsPolicy,
		Storage: jetstream.FileStorage, Replicas: 3, MaxAge: 24 * time.Hour, Duplicates: 6 * time.Minute,
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("StreamConfig = %+v, want %+v", got, want)
	}
}

type fakeAdmin struct {
	got    jetstream.StreamConfig
	refuse error
}

func (a *fakeAdmin) CreateOrUpdateStream(_ context.Context, cfg jetstream.StreamConfig) (jetstream.Stream, error) {
	a.got = cfg
	return nil, a.refuse
}

func TestEnsureStream(t *testing.T) {
	cfg := wspublish.StreamConfig("CHATIM_WS", "websocket.publish", 1, time.Hour, time.Minute)
	admin := &fakeAdmin{}
	if err := wspublish.EnsureStream(context.Background(), admin, cfg); err != nil || !reflect.DeepEqual(admin.got, cfg) {
		t.Fatalf("EnsureStream = %v, sent %+v", err, admin.got)
	}
	if err := wspublish.EnsureStream(context.Background(), nil, cfg); !errors.Is(err, apperr.ErrInvalidArgument) {
		t.Fatalf("EnsureStream(nil) = %v", err)
	}
	boom := errors.New("boom")
	if err := wspublish.EnsureStream(context.Background(), &fakeAdmin{refuse: boom}, cfg); !errors.Is(err, boom) {
		t.Fatalf("EnsureStream refused = %v", err)
	}
}

func TestEnsureStreamRejectsABadConfig(t *testing.T) {
	bad := map[string]jetstream.StreamConfig{
		"name":     wspublish.StreamConfig("chatim.ws", "websocket.publish", 1, time.Hour, time.Minute),
		"root":     wspublish.StreamConfig("CHATIM_WS", "web*", 1, time.Hour, time.Minute),
		"replicas": wspublish.StreamConfig("CHATIM_WS", "websocket.publish", 0, time.Hour, time.Minute),
		"window":   wspublish.StreamConfig("CHATIM_WS", "websocket.publish", 1, time.Minute, time.Hour),
	}
	for name, cfg := range bad {
		admin := &fakeAdmin{}
		if err := wspublish.EnsureStream(context.Background(), admin, cfg); !errors.Is(err, apperr.ErrInvalidArgument) || admin.got.Name != "" {
			t.Errorf("%s: EnsureStream = %v, sent %+v; want refused before the call", name, err, admin.got)
		}
	}
}
