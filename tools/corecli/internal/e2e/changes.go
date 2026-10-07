package e2e

import (
	"fmt"
	"maps"
	"slices"
	"strconv"

	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

const (
	KindCreated = "msg_created"
	KindEdited  = "msg_edited"
	KindDeleted = "msg_deleted"
)

type Change struct {
	Seq     uint64 `json:"seq"`
	Version uint32 `json:"version"`
	Deleted bool   `json:"deleted,omitempty"`
	Text    string `json:"text,omitempty"`
}

func EditOf(seq uint64) Change { return Change{Seq: seq, Version: 1, Text: EditTextFor(seq)} }

func DeleteOf(seq uint64) Change { return Change{Seq: seq, Version: 1, Deleted: true} }

func EditTextFor(seq uint64) string { return "e2e edited seq " + strconv.FormatUint(seq, 10) }

func (c Change) Kind() string {
	if c.Deleted {
		return KindDeleted
	}
	return KindEdited
}

func ChangeEventID(room string, seq uint64, version uint32) string {
	return MessageEventID(room, seq) + "-v" + strconv.FormatUint(uint64(version), 10)
}

func expected(a Ack, bySeq map[uint64]Change) (text string, version uint32, deleted bool) {
	c, ok := bySeq[a.Seq]
	if !ok {
		return TextFor(a.CID), 0, false
	}
	return c.Text, c.Version, c.Deleted
}

func CheckChangeEvents(room string, changes []Change, events []Event) ([]string, error) {
	want := make(map[string]Change, len(changes))
	for _, c := range changes {
		want[ChangeEventID(room, c.Seq, c.Version)] = c
	}
	seen := make(map[string]bool, len(want))
	for _, ev := range events {
		if !ev.IsChange() {
			continue
		}
		c, ok := want[ev.ID]
		switch {
		case !ok:
			return nil, fmt.Errorf("unexpected change event %s (%s)", ev.ID, ev.Kind)
		case ev.Kind != c.Kind():
			return nil, fmt.Errorf("change event %s is %s, want %s", ev.ID, ev.Kind, c.Kind())
		case ev.Room != room || ev.Seq != c.Seq || ev.Version != c.Version:
			return nil, fmt.Errorf("change event %s carries room %s seq %d version %d", ev.ID, ev.Room, ev.Seq, ev.Version)
		case ev.Text != c.Text:
			return nil, fmt.Errorf("change event %s has text %q, want %q", ev.ID, ev.Text, c.Text)
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

func CheckVersions(acks []Ack, c Change, got []*chatimv1.MessageVersion, author string) error {
	if c.Deleted {
		if len(got) != 0 {
			return fmt.Errorf("deleted seq %d has %d versions, want none", c.Seq, len(got))
		}
		return nil
	}
	i := slices.IndexFunc(acks, func(a Ack) bool { return a.Seq == c.Seq })
	if i < 0 {
		return fmt.Errorf("seq %d was never acked", c.Seq)
	}
	want := []*chatimv1.MessageVersion{
		{Ver: 0, Kind: chatimv1.EditKind_EDIT_KIND_ORIGINAL, Text: TextFor(acks[i].CID), By: author},
		{Ver: c.Version, Kind: chatimv1.EditKind_EDIT_KIND_TEXT, Text: c.Text, By: author},
	}
	if len(got) != len(want) {
		return fmt.Errorf("seq %d has %d versions, want %d", c.Seq, len(got), len(want))
	}
	for j, w := range want {
		g := got[j]
		if g.GetVer() != w.GetVer() || g.GetKind() != w.GetKind() || g.GetText() != w.GetText() || g.GetBy() != w.GetBy() || g.GetAt() == nil {
			return fmt.Errorf("seq %d entry %d = (v%d %s %q by %q), want (v%d %s %q by %q)",
				c.Seq, j, g.GetVer(), g.GetKind(), g.GetText(), g.GetBy(), w.GetVer(), w.GetKind(), w.GetText(), w.GetBy())
		}
	}
	return nil
}
