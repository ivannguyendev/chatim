package e2e

import (
	"fmt"
	"maps"
	"slices"

	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

type markWant struct {
	kind string
	seq  uint64
	text string
}

func CheckReactReply(want Reaction, change uint32, got *chatimv1.ReactionSummary) error {
	counts, wantCounts := FormatCounts(got.GetCounts()), want.Emoji+"=1"
	if change != want.Change || got.GetVer() != want.Version || counts != wantCounts {
		return fmt.Errorf("react on seq %d returned change %d counts %q version %d, want change %d counts %q version %d",
			want.Seq, change, counts, got.GetVer(), want.Change, wantCounts, want.Version)
	}
	return nil
}

func CheckPinReply(want Pin, by string, version uint64, pins []*chatimv1.Pin) error {
	if version != want.Version || len(pins) != 1 {
		return fmt.Errorf("pin of seq %d returned version %d with %d pin(s), want version %d with one pin", want.Seq, version, len(pins), want.Version)
	}
	p := pins[0]
	if p.GetSeq() != want.Seq || p.GetThreadRoot() != 0 || p.GetBy() != by || p.GetPinVer() != want.Version || p.GetPinnedAt() == nil {
		return fmt.Errorf("pin = (seq %d thread %d by %q version %d), want (seq %d thread 0 by %q version %d)",
			p.GetSeq(), p.GetThreadRoot(), p.GetBy(), p.GetPinVer(), want.Seq, by, want.Version)
	}
	return nil
}

func CheckReactions(want []Reaction, got []*chatimv1.Message) error {
	bySeq := make(map[uint64]Reaction, len(want))
	for _, r := range want {
		bySeq[r.Seq] = r
	}
	seen := 0
	for _, m := range got {
		counts, version := FormatCounts(m.GetReactions().GetCounts()), m.GetReactions().GetVer()
		r, ok := bySeq[m.GetSeq()]
		switch {
		case !ok && (counts != "" || version != 0):
			return fmt.Errorf("seq %d has reactions %q (version %d), want none", m.GetSeq(), counts, version)
		case ok && (counts != r.Emoji+"=1" || version < r.Version):
			return fmt.Errorf("seq %d has reactions %q (version %d), want %q at version %d or later", m.GetSeq(), counts, version, r.Emoji+"=1", r.Version)
		case ok:
			seen++
		}
	}
	if seen != len(bySeq) {
		return fmt.Errorf("%d of %d reacted seq missing from history", len(bySeq)-seen, len(bySeq))
	}
	return nil
}

func CheckMarkEvents(room, user string, reactions []Reaction, pins []Pin, events []Event) ([]string, error) {
	want := map[string]markWant{}
	reacted, pinned := map[uint64]bool{}, map[uint64]bool{}
	for _, r := range reactions {
		want[ReactionEventID(room, r.Seq, user, r.Change)] = markWant{KindReaction, r.Seq, r.Emoji}
		want[CountsEventID(room, r.Seq, r.Version)] = markWant{KindCounts, r.Seq, r.Emoji + "=1"}
		reacted[r.Seq] = true
	}
	for _, p := range pins {
		want[PinEventID(room, p.Version)] = markWant{KindPinned, p.Seq, ""}
		pinned[p.Seq] = true
	}
	seen := make(map[string]bool, len(want))
	for _, ev := range events {
		if !ev.IsMark() {
			continue
		}
		if err := checkMark(room, user, ev, reacted, pinned); err != nil {
			return nil, err
		}
		w, ok := want[ev.ID]
		if !ok {
			continue
		}
		if ev.Kind != w.kind || ev.Seq != w.seq || (w.kind != KindPinned && ev.Text != w.text) {
			return nil, fmt.Errorf("event %s is %s seq %d %q, want %s seq %d %q", ev.ID, ev.Kind, ev.Seq, ev.Text, w.kind, w.seq, w.text)
		}
		seen[ev.ID] = true
	}
	var missing []string
	for _, id := range slices.Sorted(maps.Keys(want)) {
		if !seen[id] {
			missing = append(missing, id)
		}
	}
	return missing, nil
}

func checkMark(room, user string, ev Event, reacted, pinned map[uint64]bool) error {
	onReaction := ev.Kind == KindReaction || ev.Kind == KindCounts
	switch {
	case ev.Room != room:
		return fmt.Errorf("%s event %s is for room %s, want %s", ev.Kind, ev.ID, ev.Room, room)
	case onReaction && !reacted[ev.Seq], !onReaction && !pinned[ev.Seq]:
		return fmt.Errorf("unexpected %s event %s on seq %d", ev.Kind, ev.ID, ev.Seq)
	case ev.Kind == KindReaction && ev.User != user:
		return fmt.Errorf("reaction event %s by %q, want %q", ev.ID, ev.User, user)
	}
	return nil
}
