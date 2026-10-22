package e2e

import (
	"errors"
	"fmt"
)

var errRemovedNeverTold = errors.New("the removed user got neither member_removed nor resync")

type RemovedCheck struct {
	Removed  bool
	Resynced bool
	Late     []uint64
	After    int
}

func CheckRemoved(entries []WSEntry, room, user string, lastBefore uint64) RemovedCheck {
	var c RemovedCheck
	told := false
	for _, e := range entries {
		switch {
		case e.Frame == FrameEvent && e.Kind == "member_removed" && e.Room == room && e.User == user:
			c.Removed = true
			continue
		case e.Frame == FrameRoomClosed && e.Room == room:
			told = true
			continue
		case e.Frame == FrameResync:
			c.Resynced, told = true, true
			continue
		}
		if e.Frame != FrameEvent || e.Room != room {
			continue
		}
		if e.Kind == "message_created" && e.Seq > lastBefore {
			c.Late = append(c.Late, e.Seq)
		} else if told {
			c.After++
		}
	}
	return c
}

func (c RemovedCheck) Err() error {
	switch {
	case len(c.Late) > 0:
		return fmt.Errorf("the removed user received %d messages sent after the removal (first %v)", len(c.Late), c.Late[:min(len(c.Late), 10)])
	case c.After > 0:
		return fmt.Errorf("the removed user received %d events of the group after room_closed or resync", c.After)
	case !c.Removed && !c.Resynced:
		return errRemovedNeverTold
	}
	return nil
}

func (c RemovedCheck) String() string {
	return fmt.Sprintf("member_removed %v, resync %v, %d late messages, %d events after", c.Removed, c.Resynced, len(c.Late), c.After)
}
