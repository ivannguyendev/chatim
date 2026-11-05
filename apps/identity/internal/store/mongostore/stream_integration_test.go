package mongostore

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/ivannguyendev/chatim/apps/identity/internal/store"
	"github.com/ivannguyendev/chatim/pkg/core/apperr"
)

const (
	streamWait = 15 * time.Second
	idleBound  = 3 * time.Second
)

func streamStart(t *testing.T, s *Store) store.StreamToken {
	t.Helper()
	token, err := s.StreamStart(t.Context())
	if err != nil {
		t.Fatalf("StreamStart: %v", err)
	}
	if len(token) == 0 {
		t.Fatal("StreamStart gave an empty token")
	}
	return token
}

func openStream(t *testing.T, s *Store, from store.StreamToken) store.ChangeStream {
	t.Helper()
	cs, err := s.OpenStream(t.Context(), from)
	if err != nil {
		t.Fatalf("OpenStream: %v", err)
	}
	t.Cleanup(func() { _ = cs.Close(context.Background()) })
	return cs
}

func nextResult(t *testing.T, cs store.ChangeStream) (store.StreamEvent, error) {
	t.Helper()
	deadline := time.Now().Add(streamWait)
	for time.Now().Before(deadline) {
		ev, ok, err := cs.Next(t.Context())
		if err != nil || ok {
			return ev, err
		}
	}
	t.Fatalf("no event within %v", streamWait)
	return store.StreamEvent{}, nil
}

func nextEvent(t *testing.T, cs store.ChangeStream) store.StreamEvent {
	t.Helper()
	ev, err := nextResult(t, cs)
	if err != nil {
		t.Fatalf("Next: %v", err)
	}
	if len(ev.Token) == 0 {
		t.Fatalf("event %+v carries no token", ev)
	}
	return ev
}

func expectUser(t *testing.T, ev store.StreamEvent, id, name string, ver int64) {
	t.Helper()
	if !slices.Equal(ev.Kinds, []store.EventKind{store.EventUser}) || ev.User.ID != id || ev.User.DisplayName != name || ev.User.Ver != ver {
		t.Fatalf("event = %+v; want user %s %q ver %d", ev, id, name, ver)
	}
}

func expectKinds(t *testing.T, ev store.StreamEvent, want ...store.EventKind) {
	t.Helper()
	if !slices.Equal(ev.Kinds, want) {
		t.Fatalf("event kinds = %v; want %v", ev.Kinds, want)
	}
}

func idleNext(t *testing.T, cs store.ChangeStream) store.StreamToken {
	t.Helper()
	began := time.Now()
	ev, ok, err := cs.Next(t.Context())
	if err != nil || ok {
		t.Fatalf("idle Next = %+v, %v, %v; want no event", ev, ok, err)
	}
	if took := time.Since(began); took > idleBound {
		t.Fatalf("idle Next took %v; want under %v", took, idleBound)
	}
	if len(ev.Token) == 0 {
		t.Fatal("idle Next carries no token")
	}
	return ev.Token
}

func TestStreamStartWithNoWritesGivesAToken(t *testing.T) {
	s := testDatabase(t)
	streamStart(t, s)
}

func TestOpenStreamSeesExactlyTheUserWrittenAfterTheStart(t *testing.T) {
	s := testDatabase(t)
	mustCreateUser(t, s, "shop", "before", "Before", "")
	from := streamStart(t, s)
	mustCreateUser(t, s, "shop", "u1", "Lan", "https://img/lan.png")
	cs := openStream(t, s, from)
	ev := nextEvent(t, cs)
	expectUser(t, ev, "u1", "Lan", 1)
	want := store.DirectoryEntry{TenantID: "shop", ID: "u1", DisplayName: "Lan", AvatarURL: "https://img/lan.png", State: store.StateActive, Ver: 1}
	if ev.User != want {
		t.Fatalf("user = %+v; want %+v", ev.User, want)
	}
	idleNext(t, cs)
}

func TestStreamCarriesTheRenameInTheSecondEvent(t *testing.T) {
	s := testDatabase(t)
	from := streamStart(t, s)
	mustCreateUser(t, s, "shop", "u1", "Lan", "")
	name := "Lan Anh"
	if err := s.SetProfile(t.Context(), "shop", "u1", store.ProfileUpdate{DisplayName: &name}, itAt, "cli:ops"); err != nil {
		t.Fatalf("SetProfile: %v", err)
	}
	cs := openStream(t, s, from)
	expectUser(t, nextEvent(t, cs), "u1", "Lan", 1)
	expectUser(t, nextEvent(t, cs), "u1", name, 2)
}

func expectOnlyMarker(t *testing.T, s *Store, from store.StreamToken) {
	t.Helper()
	mustCreateUser(t, s, "shop", "marker", "Marker", "")
	expectUser(t, nextEvent(t, openStream(t, s, from)), "marker", "Marker", 1)
}

func TestStreamSkipsAUserStateWriteThatChangesNothing(t *testing.T) {
	s := testDatabase(t)
	mustCreateUser(t, s, "shop", "u1", "Lan", "")
	from := streamStart(t, s)
	if err := s.SetUserState(t.Context(), "shop", "u1", store.StateActive, itAt, "cli:ops"); err != nil {
		t.Fatalf("SetUserState: %v", err)
	}
	expectOnlyMarker(t, s, from)
}

func TestStreamSkipsAGroupMoveThatChangesOnlyAncestors(t *testing.T) {
	s := testDatabase(t)
	mustCreateTenant(t, s, "shop")
	mustCreateChain(t, s, "shop", "A", "B", "C")
	mustCreateGroup(t, s, "shop", "E", "")
	from := streamStart(t, s)
	if err := moveGroup(t, s, "shop", "B", "E"); err != nil {
		t.Fatalf("move B under E: %v", err)
	}
	expectAncestors(t, s, "shop", map[string][]string{"C": {"E", "B"}})
	expectOnlyMarker(t, s, from)
}

func TestStreamSkipsTheTenantGroupTreeVersion(t *testing.T) {
	s := testDatabase(t)
	mustCreateTenant(t, s, "shop")
	from := streamStart(t, s)
	mustCreateGroup(t, s, "shop", "A", "")
	if got := readTenant(t, s, "shop"); got.GroupTreeVer != 1 || got.Ver != 1 {
		t.Fatalf("tenant = %+v; want group_tree_ver 1 and ver 1", got)
	}
	expectOnlyMarker(t, s, from)
}

func TestStreamReportsTrustedKeyWritesAsTrust(t *testing.T) {
	s := testDatabase(t)
	mustCreateTenant(t, s, "shop")
	mustCreateIssuer(t, s, "shop-backend", "shop")
	from := streamStart(t, s)
	mustAddKey(t, s, "shop-backend", "k1", itAt)
	if err := s.RemoveTrustedKey(t.Context(), "shop-backend", "k1"); err != nil {
		t.Fatalf("RemoveTrustedKey: %v", err)
	}
	cs := openStream(t, s, from)
	expectKinds(t, nextEvent(t, cs), store.EventTrust)
	expectKinds(t, nextEvent(t, cs), store.EventTrust)
}

func TestStreamReportsATenantDisableAsTrustAndRevocation(t *testing.T) {
	s := testDatabase(t)
	mustCreateTenant(t, s, "shop")
	from := streamStart(t, s)
	if err := s.SetTenantState(t.Context(), "shop", store.StateDisabled, store.Revocation{By: "cli:ops", At: itAt}); err != nil {
		t.Fatalf("disable: %v", err)
	}
	ev := nextEvent(t, openStream(t, s, from))
	expectKinds(t, ev, store.EventTrust, store.EventRevocation)
	if ev.User != (store.DirectoryEntry{}) {
		t.Fatalf("tenant event user = %+v; want none", ev.User)
	}
}

func TestOpenStreamRefusesAnEmptyToken(t *testing.T) {
	s := testDatabase(t)
	if _, err := s.OpenStream(t.Context(), nil); !errors.Is(err, apperr.ErrInvalidArgument) {
		t.Fatalf("OpenStream(nil) = %v; want invalid argument", err)
	}
}
