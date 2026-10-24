package e2e

import (
	"errors"
	"fmt"

	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

const (
	FrameTyping   = "typing"
	FramePresence = "presence"

	typingOn    = "typing"
	typingOff   = "stopped"
	presenceOn  = "online"
	presenceOff = "offline"
)

var errNoPresenceAnswer = errors.New("no presence answer about the user")

func (f *WSFeed) Typing(room, user string, typing bool) {
	f.add(WSEntry{Frame: FrameTyping, Kind: typingKind(typing), Room: room, User: user})
}

func (f *WSFeed) Presence(entries []*chatimv1.PresenceEntry) {
	for _, e := range entries {
		f.add(WSEntry{Frame: FramePresence, Kind: presenceKind(e.GetOnline()), User: e.GetUserId()})
	}
}

func TypingMatch(room, user string, typing bool) WSMatch {
	return WSMatch{Frame: FrameTyping, Kind: typingKind(typing), Room: room, User: user}
}

func PresenceMatch(user string, online bool) WSMatch {
	return WSMatch{Frame: FramePresence, Kind: presenceKind(online), User: user}
}

func StayedOnline(entries []WSEntry, user string) (int, error) {
	answers := 0
	for _, e := range entries {
		if e.Frame != FramePresence || e.User != user {
			continue
		}
		if e.Kind != presenceOn {
			return answers, fmt.Errorf("%s answered %s after %d online answers, want online throughout", user, e.Kind, answers)
		}
		answers++
	}
	if answers == 0 {
		return 0, fmt.Errorf("%s: %w", user, errNoPresenceAnswer)
	}
	return answers, nil
}

func typingKind(typing bool) string {
	if typing {
		return typingOn
	}
	return typingOff
}

func presenceKind(online bool) string {
	if online {
		return presenceOn
	}
	return presenceOff
}
