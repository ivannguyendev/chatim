package projection_test

import (
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/event/eventkit"
	"github.com/ivannguyendev/chatim/apps/core/internal/event/projection"
	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/model/pbconv"
	"github.com/ivannguyendev/chatim/pkg/apperr"
)

func TestMentionsOfANewMessageGetOneDocPerTarget(t *testing.T) {
	rg := newIndexRig(t)
	noErrors(t, rg.handle(t, rg.send(t, 1, mentioning)), 1)
	got := rg.mentionsOf(t, 1)
	want := domain.Mention{
		Key: domain.MsgKey{Room: roomA, Seq: 1}, Tenant: tenant, Target: mentionMinh, Sender: "alice", Live: true,
		CreatedAt: indexedAt, UpdatedAt: indexedAt.Add(time.Hour),
	}
	if len(got) != 2 || got[mentionMinh] != want || !got[mentionAll].Live {
		t.Fatalf("mentions = %+v, want %+v and a live @all", got, want)
	}
	if armed, _ := rg.timers.checks(); len(armed) != 0 || len(rg.queue.ids()) != 0 {
		t.Fatalf("armed %v events %v; a mention alone arms and enqueues nothing", armed, rg.queue.ids())
	}
}

func TestAPlainNewMessageWritesNoMention(t *testing.T) {
	rg := newIndexRig(t)
	d := rg.deps()
	d.Mentions = failingMentions{err: errBoom}
	noErrors(t, rg.build(t, d).Handle(t.Context(), []projection.Event{rg.send(t, 1, nil)}), 1)
}

func TestEditsDeliveredOutOfOrderEndAtTheNewestVersion(t *testing.T) {
	rg := newIndexRig(t)
	lan := domain.MentionTarget{Kind: domain.MentionUser, ID: "lan"}
	created := rg.send(t, 1, mentioning)
	v2 := rg.edit(t, 1, 2, domain.EditText, lan)
	v3 := rg.edit(t, 1, 3, domain.EditText, mentionMinh)
	noErrors(t, rg.handle(t, v3, created, v2), 3)
	got := rg.mentionsOf(t, 1)
	if m := got[mentionMinh]; !m.Live || m.Ver != 3 {
		t.Fatalf("minh = %+v, want live at v3", m)
	}
	for _, target := range []domain.MentionTarget{lan, mentionAll} {
		if m, ok := got[target]; ok && m.Live {
			t.Fatalf("%v = %+v, want absent or retired after v3", target, m)
		}
	}
}

func TestADeletedMessageRetiresItsMentions(t *testing.T) {
	rg := newIndexRig(t)
	noErrors(t, rg.handle(t, rg.send(t, 1, mentioning)), 1)
	noErrors(t, rg.handle(t, rg.edit(t, 1, 1, domain.EditDelete)), 1)
	got := rg.mentionsOf(t, 1)
	if len(got) != 2 {
		t.Fatalf("mentions = %+v, want the two docs kept as tombstones", got)
	}
	for target, m := range got {
		if m.Live || m.Ver != 1 {
			t.Errorf("%v = %+v, want retired at v1", target, m)
		}
	}
}

func TestAnAllMentionRaisesTheSharedMentionTime(t *testing.T) {
	rg := newIndexRig(t)
	noErrors(t, rg.handle(t, rg.send(t, 1, mentioning)), 1)
	times, err := rg.rooms.SharedMentionTimes(t.Context(), []uint64{roomA})
	if err != nil || !times[roomA].Equal(indexedAt) {
		t.Fatalf("shared mention times = %v, %v; want %v", times, err, indexedAt)
	}
}

func TestAFailedSharedMentionRaiseNaks(t *testing.T) {
	rg := newIndexRig(t)
	d := rg.deps()
	d.Shared = failingShared{err: errBoom}
	errs := rg.build(t, d).Handle(t.Context(), []projection.Event{rg.send(t, 1, mentioning)})
	if !errors.Is(errs[0], errBoom) || eventkit.Gone(errs[0]) {
		t.Fatalf("errs = %v, want the raise failure so the event is Nak'd", errs)
	}
}

func TestAGoneReplyDoesNotHideAMentionFailure(t *testing.T) {
	rg := newIndexRig(t)
	d := rg.deps()
	d.Mentions = failingMentions{err: errBoom}
	ev := rg.send(t, 2, func(m *domain.Message) { replyTo(1)(m); mentioning(m) })
	errs := rg.build(t, d).Handle(t.Context(), []projection.Event{ev})
	if !errors.Is(errs[0], errBoom) || eventkit.Gone(errs[0]) {
		t.Fatalf("errs = %v, want the mention failure, not a drop for the missing parent", errs)
	}
}

func TestMentionReplyIndexRefusesOtherEvents(t *testing.T) {
	rg := newIndexRig(t)
	errs := rg.handle(t, decoded(t, reactionChanged(roomA, 1, at(1))))
	if !errors.Is(errs[0], apperr.ErrInvalidArgument) {
		t.Fatalf("errs = %v, want ErrInvalidArgument", errs)
	}
}

func TestMentionReplyIndexSubscribesToMessageChanges(t *testing.T) {
	rg := newIndexRig(t)
	want := []string{"evt.*.message.*.msg_created", "evt.*.message.*.msg_edited", "evt.*.message.*.msg_deleted"}
	if got := rg.index.Subjects("evt"); !slices.Equal(got, want) || rg.index.Name() != projection.MentionReplyIndexName {
		t.Fatalf("name %q subjects %v, want %q %v", rg.index.Name(), got, projection.MentionReplyIndexName, want)
	}
	if projection.MentionReplyIndexName != "mention_reply_index" || pbconv.RepliesCounter != "replies" {
		t.Fatalf("name %q counter %q", projection.MentionReplyIndexName, pbconv.RepliesCounter)
	}
}

func TestNewMentionReplyIndexRejectsMissingDeps(t *testing.T) {
	rg := newIndexRig(t)
	bad := map[string]func(*projection.MentionReplyIndexDeps){
		"no replies":  func(d *projection.MentionReplyIndexDeps) { d.Replies = nil },
		"no threads":  func(d *projection.MentionReplyIndexDeps) { d.Threads = nil },
		"no counts":   func(d *projection.MentionReplyIndexDeps) { d.Counts = nil },
		"no mentions": func(d *projection.MentionReplyIndexDeps) { d.Mentions = nil },
		"no shared":   func(d *projection.MentionReplyIndexDeps) { d.Shared = nil },
		"no timers":   func(d *projection.MentionReplyIndexDeps) { d.Timers = nil },
		"no events":   func(d *projection.MentionReplyIndexDeps) { d.Events = nil },
	}
	for name, edit := range bad {
		d := rg.deps()
		edit(&d)
		if _, err := projection.NewMentionReplyIndex(d); !errors.Is(err, apperr.ErrInvalidArgument) {
			t.Errorf("%s: err = %v, want ErrInvalidArgument", name, err)
		}
	}
}
