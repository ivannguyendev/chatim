package e2e

import (
	"fmt"
	"slices"
	"strconv"
	"strings"

	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

type Want struct {
	ID      string
	Kind    string
	Subject string
	Payload string
}

type MemberReply struct {
	Changed bool
	Ver     uint32
	Detail  string
}

func CheckLive(rooms []string, wants []Want, events []Event) ([]string, error) {
	byID := make(map[string]Want, len(wants))
	for _, w := range wants {
		byID[w.ID] = w
	}
	seen := make(map[string]bool, len(wants))
	for _, ev := range events {
		if !slices.Contains(rooms, ev.Room) {
			continue
		}
		w, ok := byID[ev.ID]
		switch {
		case !ok && ev.IsMember():
			return nil, fmt.Errorf("unexpected %s event %s (%s)", ev.Kind, ev.ID, ev.Text)
		case !ok:
			continue
		case ev.Kind != w.Kind:
			return nil, fmt.Errorf("event %s is %s, want %s", ev.ID, ev.Kind, w.Kind)
		case ev.Subject != w.Subject:
			return nil, fmt.Errorf("event %s came on subject %s, want %s", ev.ID, ev.Subject, w.Subject)
		case w.Payload != "" && ev.Text != w.Payload:
			return nil, fmt.Errorf("event %s carries %q, want %q", ev.ID, ev.Text, w.Payload)
		}
		seen[ev.ID] = true
	}
	var missing []string
	for _, w := range wants {
		if !seen[w.ID] {
			missing = append(missing, w.ID)
		}
	}
	return missing, nil
}

func CheckMemberReply(what string, want, got MemberReply) error {
	if got.Changed != want.Changed || got.Ver != want.Ver || (want.Detail != "" && got.Detail != want.Detail) {
		return fmt.Errorf("%s returned changed %v ver %d %s, want changed %v ver %d %s",
			what, got.Changed, got.Ver, got.Detail, want.Changed, want.Ver, want.Detail)
	}
	return nil
}

func CheckReadReply(what string, wantSeq, wantVer, seq, ver uint64) error {
	if seq != wantSeq || ver != wantVer {
		return fmt.Errorf("%s returned read_seq %d read_ver %d, want read_seq %d read_ver %d", what, seq, ver, wantSeq, wantVer)
	}
	return nil
}

func FormatAdded(added []*chatimv1.AddedMember) string {
	parts := make([]string, len(added))
	for i, m := range added {
		parts[i] = m.GetUser() + ":" + strconv.FormatUint(uint64(m.GetVer()), 10)
	}
	slices.Sort(parts)
	return strings.Join(parts, ",")
}

func CheckClearedPage(got []*chatimv1.Message, hidden uint64, shown Ack) error {
	if len(got) != 2 {
		return fmt.Errorf("history returned %d messages, want seq %d hidden and seq %d shown", len(got), hidden, shown.Seq)
	}
	h, s := got[0], got[1]
	switch {
	case h.GetSeq() != hidden || !h.GetHidden() || h.GetText() != "":
		return fmt.Errorf("seq %d is hidden %v with text %q, want seq %d hidden with no text", h.GetSeq(), h.GetHidden(), h.GetText(), hidden)
	case s.GetSeq() != shown.Seq || s.GetHidden() || s.GetCid() != shown.CID || s.GetText() != TextFor(shown.CID):
		return fmt.Errorf("seq %d is hidden %v with cid %q text %q, want seq %d shown with cid %q", s.GetSeq(), s.GetHidden(), s.GetCid(), s.GetText(), shown.Seq, shown.CID)
	}
	return nil
}
