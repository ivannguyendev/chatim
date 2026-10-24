package e2e

import (
	"fmt"
	"time"

	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

const (
	FrameAuthRequired = "auth_required"
	FrameError        = "frame_error"
	FrameRetryLater   = "retry_later"

	kindCreated = "message_created"
)

func (f *WSFeed) AuthRequired(reason chatimv1.AuthRequiredReason) {
	f.add(WSEntry{Frame: FrameAuthRequired, Kind: reason.String()})
}

func (f *WSFeed) FrameError(reason chatimv1.FrameErrorReason) {
	f.add(WSEntry{Frame: FrameError, Kind: reason.String()})
}

func (f *WSFeed) RetryLater(after time.Duration) {
	f.add(WSEntry{Frame: FrameRetryLater, Kind: after.String()})
}

func AuthRequiredMatch(reason chatimv1.AuthRequiredReason) WSMatch {
	return WSMatch{Frame: FrameAuthRequired, Kind: reason.String()}
}

func FrameErrorMatch(reason chatimv1.FrameErrorReason) WSMatch {
	return WSMatch{Frame: FrameError, Kind: reason.String()}
}

func CreatedMatch(room string, seq uint64) WSMatch {
	return WSMatch{Frame: FrameEvent, Kind: kindCreated, ID: MessageEventID(room, seq)}
}

func CheckSilentWhileWaiting(entries []WSEntry) error {
	for _, e := range entries {
		if e.Frame != FrameAuthRequired {
			return fmt.Errorf("a waiting connection received %+v, want only auth_required", e)
		}
	}
	return nil
}

func CheckMissedWhileWaiting(entries []WSEntry, room string, missed uint64) error {
	gaps := 0
	next := false
	for _, e := range entries {
		switch {
		case CreatedMatch(room, missed).Matches(e):
			return fmt.Errorf("seq %d of room %s, sent while the connection waited, was delivered", missed, room)
		case CreatedMatch(room, missed+1).Matches(e):
			next = true
		case e.Frame == FrameGap && e.Room == room:
			if e.Seq != missed || e.To != missed {
				return fmt.Errorf("gap %d..%d of room %s, want %d..%d", e.Seq, e.To, room, missed, missed)
			}
			gaps++
		}
	}
	switch {
	case gaps != 1:
		return fmt.Errorf("%d gaps of room %s, want one for seq %d sent while the connection waited", gaps, room, missed)
	case !next:
		return fmt.Errorf("seq %d of room %s, sent after the new token, not received", missed+1, room)
	}
	return nil
}

func CheckClosedWith(what string, code uint16, closed bool, want uint16) error {
	switch {
	case !closed:
		return fmt.Errorf("%s ended without a close code, want %d", what, want)
	case code != want:
		return fmt.Errorf("%s closed with %d, want %d", what, code, want)
	}
	return nil
}
