package e2e

import (
	"fmt"
	"strconv"

	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

const missingListed = 10

type Coverage struct {
	Distinct     int
	Duplicates   int
	MissingCount int
	Missing      []uint64
}

func CheckAcks(acks []Ack) error {
	cids := make(map[string]uint64, len(acks))
	for i, a := range acks {
		want := uint64(i + 1)
		switch prev, dup := cids[a.CID]; {
		case a.Seq != want:
			return fmt.Errorf("ack %d (cid %s) has seq %d, want %d", i+1, a.CID, a.Seq, want)
		case dup:
			return fmt.Errorf("cid %s acked twice, at seq %d and %d", a.CID, prev, a.Seq)
		}
		cids[a.CID] = a.Seq
	}
	return nil
}

func CheckPage(want []Ack, got []*chatimv1.Message, room, sender string) error {
	if len(got) != len(want) {
		return fmt.Errorf("history returned %d messages, want %d", len(got), len(want))
	}
	for i, m := range got {
		a := want[i]
		switch {
		case m.GetSeq() != a.Seq:
			return fmt.Errorf("message %d has seq %d, want %d", i, m.GetSeq(), a.Seq)
		case m.GetCid() != a.CID:
			return fmt.Errorf("seq %d has cid %q, want %q", a.Seq, m.GetCid(), a.CID)
		case m.GetRoomId() != room:
			return fmt.Errorf("seq %d belongs to room %s, want %s", a.Seq, m.GetRoomId(), room)
		case m.GetThreadRoot() != 0:
			return fmt.Errorf("seq %d is in thread %d, want the main timeline", a.Seq, m.GetThreadRoot())
		case m.GetSender() != sender:
			return fmt.Errorf("seq %d has sender %q, want %q", a.Seq, m.GetSender(), sender)
		case m.GetText() != TextFor(a.CID):
			return fmt.Errorf("seq %d has text %q, want %q", a.Seq, m.GetText(), TextFor(a.CID))
		}
	}
	return nil
}

func MessageEventID(room string, seq uint64) string {
	return room + "-0-" + strconv.FormatUint(seq, 10)
}

func CheckEvents(acks []Ack, room string, events []Event) (Coverage, error) {
	bySeq := make(map[uint64]Ack, len(acks))
	for _, a := range acks {
		bySeq[a.Seq] = a
	}
	seen := make(map[uint64]bool, len(acks))
	var cov Coverage
	for _, ev := range events {
		a, known := bySeq[ev.Seq]
		switch {
		case ev.Room != room:
			return cov, fmt.Errorf("live event for other room %s (seq %d), want only room %s", ev.Room, ev.Seq, room)
		case !known:
			return cov, fmt.Errorf("live event with unexpected seq %d (cid %q)", ev.Seq, ev.CID)
		case ev.CID != a.CID:
			return cov, fmt.Errorf("live event seq %d has cid %q, want %q", ev.Seq, ev.CID, a.CID)
		case ev.ID != MessageEventID(room, ev.Seq):
			return cov, fmt.Errorf("live event seq %d has id %q, want %q", ev.Seq, ev.ID, MessageEventID(room, ev.Seq))
		case seen[ev.Seq]:
			cov.Duplicates++
			continue
		}
		seen[ev.Seq] = true
		cov.Distinct++
	}
	for _, a := range acks {
		if seen[a.Seq] {
			continue
		}
		cov.MissingCount++
		if len(cov.Missing) < missingListed {
			cov.Missing = append(cov.Missing, a.Seq)
		}
	}
	return cov, nil
}
