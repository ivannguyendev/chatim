package main

import (
	"fmt"
	"time"

	"github.com/ivannguyendev/chatim/tools/corecli/internal/e2e"
)

func (r *wsEphemeralRun) outsiderDropped() error {
	if err := r.dial(r.url, r.d); err != nil {
		return err
	}
	markB := r.b.feed.Len()
	start := time.Now()
	if err := r.d.ws.Typing(r.room); err != nil {
		return fmt.Errorf("peer D typing: %w", err)
	}
	if !r.pauseUntil(start.Add(typingQuiet)) {
		return r.base.Err()
	}
	if err := r.b.absent(markB, e2e.WSMatch{Frame: e2e.FrameTyping, User: r.d.user}); err != nil {
		return err
	}
	r.d.close()
	r.dClosedAt = time.Now()
	return nil
}

func (r *wsEphemeralRun) presenceSeen() error {
	mark := r.b.feed.Len()
	if err := r.b.ws.WatchPresence([]string{r.a.user, r.d.user}); err != nil {
		return fmt.Errorf("peer B watches presence: %w", err)
	}
	if _, err := r.b.await(mark, e2e.PresenceMatch(r.a.user, true)); err != nil {
		return err
	}
	if _, err := r.b.awaitUntil(mark, e2e.PresenceMatch(r.d.user, false), r.dClosedAt.Add(offlineWithin)); err != nil {
		return err
	}
	offline := time.Since(r.dClosedAt)
	_, _, seenOnline := r.b.feed.Find(mark, e2e.PresenceMatch(r.d.user, true))
	r.notes = append(r.notes, fmt.Sprintf("A online on B's watch; D offline %s after closing (seen online first: %v)", offline.Round(time.Millisecond), seenOnline))
	return nil
}

func (r *wsEphemeralRun) reconnectStaysOnline() error {
	r.a2.close()
	r.a.close()
	closedAt := time.Now()
	mark := r.b.feed.Len()
	if !r.pauseUntil(closedAt.Add(reconnectAfter)) {
		return r.base.Err()
	}
	if err := r.dial(r.url, r.a); err != nil {
		return err
	}
	if !r.pauseUntil(closedAt.Add(stayOnlineFor)) {
		return r.base.Err()
	}
	answers, err := e2e.StayedOnline(r.b.feed.Entries(mark), r.a.user)
	if err != nil {
		return fmt.Errorf("peer B watching A across its reconnect: %w", err)
	}
	r.notes = append(r.notes, fmt.Sprintf("A reconnected after %s and stayed online in %d answers over %s", reconnectAfter, answers, stayOnlineFor))
	return nil
}
