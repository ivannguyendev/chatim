package e2e

import (
	"fmt"
	"sync"

	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

type Gap struct {
	Room   string
	Thread uint64
	From   uint64
	To     uint64
}

func (g Gap) Empty() bool { return g.From == 0 }

type EventStats struct {
	Delivered  uint64
	Duplicates uint64
	Gaps       uint64
}

type timeline struct {
	room   string
	thread uint64
}

type timelineSeq struct {
	timeline
	seq uint64
}

type EventCheck struct {
	mu    sync.Mutex
	ids   map[string]struct{}
	seqs  map[timelineSeq]struct{}
	last  map[timeline]uint64
	stats EventStats
}

func NewEventCheck() *EventCheck {
	return &EventCheck{ids: map[string]struct{}{}, seqs: map[timelineSeq]struct{}{}, last: map[timeline]uint64{}}
}

func (c *EventCheck) Apply(ev *chatimv1.Event) (bool, Gap) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if id := ev.GetId(); id != "" {
		if _, seen := c.ids[id]; seen {
			c.stats.Duplicates++
			return false, Gap{}
		}
		c.ids[id] = struct{}{}
	}
	var gap Gap
	if ev.GetMessageCreated() != nil {
		key := timelineSeq{timeline: timeline{room: ev.GetRoomId(), thread: ev.GetThreadRoot()}, seq: ev.GetSeq()}
		if _, seen := c.seqs[key]; seen {
			c.stats.Duplicates++
			return false, Gap{}
		}
		c.seqs[key] = struct{}{}
		gap = c.advance(key)
	}
	c.stats.Delivered++
	return true, gap
}

func (c *EventCheck) Stats() EventStats {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.stats
}

func (c *EventCheck) advance(key timelineSeq) Gap {
	last, known := c.last[key.timeline]
	if key.seq <= last {
		return Gap{}
	}
	c.last[key.timeline] = key.seq
	if !known || key.seq == last+1 {
		return Gap{}
	}
	c.stats.Gaps++
	return Gap{Room: key.room, Thread: key.thread, From: last + 1, To: key.seq - 1}
}

func CheckEventStats(who string, s EventStats) error {
	if s.Duplicates != 0 || s.Gaps != 0 {
		return fmt.Errorf("%s saw %d duplicate events and %d gaps, want none (%d delivered)", who, s.Duplicates, s.Gaps, s.Delivered)
	}
	return nil
}
