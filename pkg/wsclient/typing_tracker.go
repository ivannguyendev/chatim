package wsclient

import (
	"cmp"
	"slices"
	"time"

	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

const TypingTimeout = 6 * time.Second

type typist struct {
	room string
	user string
}

type typingTracker struct {
	shown map[typist]time.Time
	timer *time.Timer
	armed bool
}

func newTypingTracker() typingTracker {
	return typingTracker{shown: map[typist]time.Time{}}
}

func (t *typingTracker) arm(wait time.Duration) {
	t.armed = true
	if t.timer == nil {
		t.timer = time.NewTimer(wait)
		return
	}
	t.timer.Reset(wait)
}

func (t *typingTracker) due() <-chan time.Time {
	if t.timer == nil {
		return nil
	}
	return t.timer.C
}

func (t *typingTracker) stop() {
	if t.timer != nil {
		t.timer.Stop()
	}
}

func (t *typingTracker) expire(now time.Time) []typist {
	t.armed = false
	var ended []typist
	var next time.Time
	for who, deadline := range t.shown {
		if !deadline.After(now) {
			ended = append(ended, who)
			delete(t.shown, who)
		} else if next.IsZero() || deadline.Before(next) {
			next = deadline
		}
	}
	if !next.IsZero() {
		t.arm(next.Sub(now))
	}
	slices.SortFunc(ended, func(a, b typist) int {
		return cmp.Or(cmp.Compare(a.room, b.room), cmp.Compare(a.user, b.user))
	})
	return ended
}

func (c *Client) typingNotice(n *chatimv1.TypingNotice) {
	if c.opts.onTyping == nil {
		return
	}
	who := typist{room: n.GetRoomId(), user: n.GetUserId()}
	if n.GetStopped() {
		c.typingEnded(who)
		return
	}
	c.typists.shown[who] = time.Now().Add(TypingTimeout)
	if !c.typists.armed {
		c.typists.arm(TypingTimeout)
	}
	c.opts.onTyping(who.room, who.user, true)
}

func (c *Client) typingEnded(who typist) {
	if _, shown := c.typists.shown[who]; !shown {
		return
	}
	delete(c.typists.shown, who)
	c.opts.onTyping(who.room, who.user, false)
}

func (c *Client) typingExpired() {
	for _, who := range c.typists.expire(time.Now()) {
		c.opts.onTyping(who.room, who.user, false)
	}
}

func (c *Client) typingEndedByMessage(ev *chatimv1.Event) {
	if ev.GetMessageCreated() == nil || c.opts.onTyping == nil {
		return
	}
	c.typingEnded(typist{room: ev.GetRoomId(), user: ev.GetActor()})
}
