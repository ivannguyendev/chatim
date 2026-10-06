package counter_test

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/counter"
	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/apps/core/internal/store/memstore"
	"github.com/ivannguyendev/chatim/pkg/apperr"
)

var (
	at        = time.UnixMilli(1_700_000_000_000).UTC()
	key       = store.MsgKey{Room: 42, Seq: 1}
	errBroken = errors.New("store broken")
)

type rig struct {
	msgs      *memstore.Messages
	reactions *memstore.Reactions
}

func newRig(t *testing.T) rig {
	t.Helper()
	rg := rig{msgs: memstore.NewMessages(), reactions: memstore.NewReactions()}
	m := domain.Message{Room: 42, Seq: 1, Tenant: "acme", From: "alice", Kind: domain.KindText, Text: "hi", CID: "c-1", CreatedAt: at}
	if res := rg.msgs.Insert(t.Context(), []domain.Message{m}); res[0].Outcome != store.Inserted {
		t.Fatalf("Insert: %+v", res)
	}
	return rg
}

func (rg rig) react(t *testing.T, user, emoji string) store.Witness {
	t.Helper()
	r, _, err := rg.reactions.Set(t.Context(), domain.Reaction{Room: 42, Seq: 1, Tenant: "acme", User: user, Emoji: emoji, At: at})
	if err != nil {
		t.Fatalf("Set(%s by %s): %v", emoji, user, err)
	}
	return store.Witness{User: user, N: r.N}
}

func (rg rig) stored(t *testing.T) domain.ReactionSummary {
	t.Helper()
	got, err := rg.msgs.Find(t.Context(), 42, []store.MsgKey{key})
	if err != nil || len(got) != 1 {
		t.Fatalf("Find = %+v, %v", got, err)
	}
	return got[0].Reactions
}

func toucher(t *testing.T, msgs counter.Messages, reactions counter.Reactions) *counter.Toucher {
	t.Helper()
	tc, err := counter.New(msgs, reactions)
	if err != nil {
		t.Fatalf("counter.New: %v", err)
	}
	return tc
}

func same(a, b domain.ReactionSummary) bool {
	return a.Version == b.Version && slices.Equal(a.Counts, b.Counts)
}

type racer struct {
	*memstore.Messages
	raced bool
}

func (r *racer) SetReactions(ctx context.Context, k store.MsgKey, base uint64, s domain.ReactionSummary) (bool, error) {
	if !r.raced {
		r.raced = true
		if _, err := r.Messages.SetReactions(ctx, k, base, domain.ReactionSummary{Version: base + 1}); err != nil {
			return false, err
		}
	}
	return r.Messages.SetReactions(ctx, k, base, s)
}

type loser struct {
	*memstore.Messages
	tries int
	gone  bool
}

func (l *loser) SetReactions(context.Context, store.MsgKey, uint64, domain.ReactionSummary) (bool, error) {
	l.tries++
	return false, nil
}

func (l *loser) Find(ctx context.Context, room uint64, keys []store.MsgKey) ([]domain.Message, error) {
	if l.gone {
		return nil, nil
	}
	return l.Messages.Find(ctx, room, keys)
}

type brokenCount struct{}

func (brokenCount) Count(context.Context, store.MsgKey, []store.Witness) ([]domain.ReactionCount, error) {
	return nil, errBroken
}

func TestTouchWritesTheRecountAndBumpsTheVersion(t *testing.T) {
	rg := newRig(t)
	w := rg.react(t, "alice", "👍")
	rg.react(t, "bob", "👍")
	rg.react(t, "carol", "❤️")
	got, bumped, err := toucher(t, rg.msgs, rg.reactions).Touch(t.Context(), key, domain.ReactionSummary{}, []store.Witness{w}, 3)
	want := domain.ReactionSummary{Counts: []domain.ReactionCount{{Emoji: "👍", Count: 2}, {Emoji: "❤️", Count: 1}}, Version: 1}
	if err != nil || !bumped || !same(got, want) || !same(rg.stored(t), want) {
		t.Fatalf("Touch = %+v, %v, %v; stored %+v; want %+v bumped", got, bumped, err, rg.stored(t), want)
	}
}

func TestTouchWithAnEqualCountWritesNothing(t *testing.T) {
	rg := newRig(t)
	tc := toucher(t, rg.msgs, rg.reactions)
	if got, bumped, err := tc.Touch(t.Context(), key, domain.ReactionSummary{}, nil, 3); err != nil || bumped || !same(got, domain.ReactionSummary{}) {
		t.Fatalf("Touch(no reactions) = %+v, %v, %v; want the zero summary unchanged", got, bumped, err)
	}
	rg.react(t, "alice", "👍")
	first, _, err := tc.Touch(t.Context(), key, domain.ReactionSummary{}, nil, 3)
	if err != nil {
		t.Fatalf("Touch: %v", err)
	}
	got, bumped, err := tc.Touch(t.Context(), key, first, nil, 3)
	if err != nil || bumped || !same(got, first) || rg.stored(t).Version != 1 {
		t.Fatalf("Touch(equal) = %+v, %v, %v; stored v%d; want %+v and no write", got, bumped, err, rg.stored(t).Version, first)
	}
}

func TestTouchRereadsTheSummaryAfterALostCAS(t *testing.T) {
	rg := newRig(t)
	rg.react(t, "alice", "👍")
	got, bumped, err := toucher(t, &racer{Messages: rg.msgs}, rg.reactions).Touch(t.Context(), key, domain.ReactionSummary{}, nil, 3)
	want := domain.ReactionSummary{Counts: []domain.ReactionCount{{Emoji: "👍", Count: 1}}, Version: 2}
	if err != nil || !bumped || !same(got, want) || !same(rg.stored(t), want) {
		t.Fatalf("Touch after a rival write = %+v, %v, %v; want %+v", got, bumped, err, want)
	}
}

func TestTouchGivesUpAfterItsTries(t *testing.T) {
	rg := newRig(t)
	rg.react(t, "alice", "👍")
	l := &loser{Messages: rg.msgs}
	_, bumped, err := toucher(t, l, rg.reactions).Touch(t.Context(), key, domain.ReactionSummary{}, nil, 3)
	if !errors.Is(err, counter.ErrContended) || !errors.Is(err, apperr.ErrUnavailable) || bumped || l.tries != 3 {
		t.Fatalf("Touch = %v, bumped %v after %d CAS tries; want ErrContended after 3", err, bumped, l.tries)
	}
}

func TestTouchReportsAMessageThatIsGone(t *testing.T) {
	rg := newRig(t)
	rg.react(t, "alice", "👍")
	l := &loser{Messages: rg.msgs, gone: true}
	if _, _, err := toucher(t, l, rg.reactions).Touch(t.Context(), key, domain.ReactionSummary{}, nil, 3); !errors.Is(err, domain.ErrMessageNotFound) || l.tries != 1 {
		t.Fatalf("Touch = %v after %d tries, want ErrMessageNotFound after 1", err, l.tries)
	}
}

func TestTouchStopsOnAStaleWitnessAndPassesStoreErrors(t *testing.T) {
	rg := newRig(t)
	rg.react(t, "alice", "👍")
	_, bumped, err := toucher(t, rg.msgs, rg.reactions).Touch(t.Context(), key, domain.ReactionSummary{}, []store.Witness{{User: "alice", N: 2}}, 3)
	if !errors.Is(err, store.ErrStaleRead) || bumped || rg.stored(t).Version != 0 {
		t.Fatalf("Touch(stale witness) = %v, bumped %v, stored v%d; want ErrStaleRead and no write", err, bumped, rg.stored(t).Version)
	}
	if _, _, err := toucher(t, rg.msgs, brokenCount{}).Touch(t.Context(), key, domain.ReactionSummary{}, nil, 3); !errors.Is(err, errBroken) {
		t.Fatalf("Touch(broken count) = %v, want %v", err, errBroken)
	}
}

func TestNewAndTouchRejectBadInput(t *testing.T) {
	if _, err := counter.New(nil, memstore.NewReactions()); !errors.Is(err, apperr.ErrInvalidArgument) {
		t.Fatalf("New(nil messages) = %v, want ErrInvalidArgument", err)
	}
	if _, err := counter.New(memstore.NewMessages(), nil); !errors.Is(err, apperr.ErrInvalidArgument) {
		t.Fatalf("New(nil reactions) = %v, want ErrInvalidArgument", err)
	}
	rg := newRig(t)
	if _, _, err := toucher(t, rg.msgs, rg.reactions).Touch(t.Context(), key, domain.ReactionSummary{}, nil, 0); !errors.Is(err, apperr.ErrInvalidArgument) {
		t.Fatalf("Touch(0 tries) = %v, want ErrInvalidArgument", err)
	}
}
