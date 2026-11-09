package mongostore

import (
	"context"
	"errors"
	"slices"
	"testing"
	"testing/synctest"
	"time"

	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.uber.org/goleak"

	"github.com/ivannguyendev/chatim/apps/identity/internal/store"
	"github.com/ivannguyendev/chatim/pkg/core/apperr"
)

const (
	lanLinkKey = "t│api.acme│0901000000"
	lanCand    = "cand"
)

type linkRead func(ctx context.Context, id string) (string, bool, error)

type linkCase struct {
	links   func(ctx context.Context, ids []string) error
	read    linkRead
	users   func(ctx context.Context, ids []string) error
	want    store.LinkedUser
	wantErr error
	reads   int
	userIDs [][]string
}

func lanNewUser() store.NewUser {
	return store.NewUser{
		TenantID: "t", Realm: "api.acme", Sub: "0901000000", CandidateID: lanCand,
		DisplayName: "Lan", At: time.Unix(1700000000, 0).UTC(), By: "jit:api.acme",
	}
}

func duplicateEach(_ context.Context, ids []string) error {
	return duplicates(ids)
}

func duplicates(ids []string) mongo.BulkWriteException {
	var bwe mongo.BulkWriteException
	for i := range ids {
		bwe.WriteErrors = append(bwe.WriteErrors, mongo.BulkWriteError{WriteError: mongo.WriteError{Index: i, Code: duplicateKeyCode}})
	}
	return bwe
}

func storedLink(id string) linkRead {
	return func(context.Context, string) (string, bool, error) { return id, true, nil }
}

func linkTestStore(t *testing.T, links, users *fakeInserts, read linkRead, reads *int) *Store {
	t.Helper()
	cfg := testJITConfig()
	return &Store{
		links: newTestBatcher(t, cfg, links), users: newTestBatcher(t, cfg, users), jitTimeout: cfg.timeout,
		readLink: func(ctx context.Context, id string) (string, bool, error) {
			*reads++
			if id != lanLinkKey {
				t.Errorf("link read of %q; want %q", id, lanLinkKey)
			}
			return read(ctx, id)
		},
	}
}

func runLinkCase(t *testing.T, c linkCase) {
	defer goleak.VerifyNone(t, goleak.IgnoreCurrent())
	synctest.Test(t, func(t *testing.T) {
		links, users := &fakeInserts{reply: c.links}, &fakeInserts{reply: c.users}
		reads := 0
		s := linkTestStore(t, links, users, c.read, &reads)
		got, err := s.LinkUser(t.Context(), lanNewUser())
		if got != c.want || !errors.Is(err, c.wantErr) || (c.wantErr == nil && err != nil) {
			t.Fatalf("LinkUser = %+v, %v; want %+v, %v", got, err, c.want, c.wantErr)
		}
		if reads != c.reads {
			t.Fatalf("link reads = %d; want %d", reads, c.reads)
		}
		if !slices.EqualFunc(users.batches, c.userIDs, slices.Equal) {
			t.Fatalf("user inserts = %v; want %v", users.batches, c.userIDs)
		}
	})
}

func TestLinkUserResolvesTheUserID(t *testing.T) {
	cases := map[string]linkCase{
		"fresh subject": {
			want: store.LinkedUser{ID: lanCand, Result: store.LinkCreated}, userIDs: [][]string{{"t│cand"}},
		},
		"link and user already there": {
			links: duplicateEach, read: storedLink("winner"), users: duplicateEach, reads: 1,
			want: store.LinkedUser{ID: "winner", Result: store.LinkExisting}, userIDs: [][]string{{"t│winner"}},
		},
		"link without its user": {
			links: duplicateEach, read: storedLink("winner"), reads: 1,
			want: store.LinkedUser{ID: "winner", Result: store.LinkHealed}, userIDs: [][]string{{"t│winner"}},
		},
		"new link whose user a racing healer wrote": {
			users: duplicateEach,
			want:  store.LinkedUser{ID: lanCand, Result: store.LinkCreated}, userIDs: [][]string{{"t│cand"}},
		},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) { runLinkCase(t, c) })
	}
}

func TestLinkUserFailsWithoutUsingTheCandidate(t *testing.T) {
	writeConcern := func(_ context.Context, ids []string) error {
		bwe := duplicates(ids)
		bwe.WriteConcernError = &mongo.WriteConcernError{Code: 64}
		return bwe
	}
	cases := map[string]linkCase{
		"link not visible yet": {
			links: duplicateEach, read: func(context.Context, string) (string, bool, error) { return "", false, nil }, reads: 1,
			want: store.LinkedUser{Result: store.LinkPending}, wantErr: apperr.ErrUnavailable,
		},
		"duplicate link with a write concern error": {
			links: writeConcern, wantErr: apperr.ErrUnavailable,
		},
		"link read fails": {
			links: duplicateEach, read: func(context.Context, string) (string, bool, error) { return "", false, errors.New("reset") }, reads: 1,
			wantErr: apperr.ErrUnavailable,
		},
		"stored link holds a bad user id": {
			links: duplicateEach, read: storedLink("bad id"), reads: 1, wantErr: store.ErrLinkCorrupt,
		},
		"link insert fails": {
			links: func(context.Context, []string) error { return errors.New("reset") }, wantErr: apperr.ErrUnavailable,
		},
		"existing link whose user insert has a write concern error": {
			links: duplicateEach, read: storedLink("winner"), users: writeConcern, reads: 1,
			wantErr: apperr.ErrUnavailable, userIDs: [][]string{{"t│winner"}},
		},
		"user insert fails": {
			users:   func(context.Context, []string) error { return errors.New("reset") },
			wantErr: apperr.ErrUnavailable, userIDs: [][]string{{"t│cand"}},
		},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) { runLinkCase(t, c) })
	}
}

func TestLinkUserBoundsTheLinkReadByTheInsertTimeout(t *testing.T) {
	defer goleak.VerifyNone(t, goleak.IgnoreCurrent())
	synctest.Test(t, func(t *testing.T) {
		reads := 0
		block := func(ctx context.Context, _ string) (string, bool, error) { <-ctx.Done(); return "", false, ctx.Err() }
		s := linkTestStore(t, &fakeInserts{reply: duplicateEach}, &fakeInserts{}, block, &reads)
		start := time.Now()
		_, err := s.LinkUser(t.Context(), lanNewUser())
		if waited := time.Since(start) - testJITConfig().window; !errors.Is(err, apperr.ErrUnavailable) || waited != s.jitTimeout {
			t.Fatalf("LinkUser = %v after a read of %v; want unavailable after %v", err, waited, s.jitTimeout)
		}
	})
}

func TestLinkUserRejectsBadInput(t *testing.T) {
	cases := map[string]func(*store.NewUser){
		"empty tenant":           func(u *store.NewUser) { u.TenantID = "" },
		"empty realm":            func(u *store.NewUser) { u.Realm = "" },
		"empty sub":              func(u *store.NewUser) { u.Sub = "" },
		"separator in realm":     func(u *store.NewUser) { u.Realm = "api│acme" },
		"separator in sub":       func(u *store.NewUser) { u.Sub = "09│01" },
		"empty candidate":        func(u *store.NewUser) { u.CandidateID = "" },
		"candidate not an ident": func(u *store.NewUser) { u.CandidateID = "a b" },
	}
	for name, change := range cases {
		t.Run(name, func(t *testing.T) {
			defer goleak.VerifyNone(t, goleak.IgnoreCurrent())
			synctest.Test(t, func(t *testing.T) {
				links, users := &fakeInserts{}, &fakeInserts{}
				reads := 0
				s := linkTestStore(t, links, users, storedLink("x"), &reads)
				u := lanNewUser()
				change(&u)
				if got, err := s.LinkUser(t.Context(), u); !errors.Is(err, apperr.ErrInvalidArgument) || got != (store.LinkedUser{}) {
					t.Fatalf("LinkUser = %+v, %v; want invalid argument", got, err)
				}
				if len(links.batches)+len(users.batches)+reads != 0 {
					t.Fatalf("writes %v %v, reads %d; want none", links.batches, users.batches, reads)
				}
			})
		})
	}
}
