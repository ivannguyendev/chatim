package interest

import (
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

type backlogState uint8

const (
	flowing backlogState = iota
	skipping
	owed
)

const clientBacklog = chatimv1.ResyncReason_RESYNC_REASON_CLIENT_BACKLOG

func (c *Conn) deliver(frame []byte, catchingUp, resumes bool) bool {
	c.sendMu.Lock()
	defer c.sendMu.Unlock()
	if c.backlog != flowing {
		if catchingUp || !resumes {
			if c.backlog == owed {
				c.offerBacklogResync()
			}
			return false
		}
		if !c.settleBacklog() {
			return false
		}
	}
	if c.sink.TrySend(frame) {
		return true
	}
	c.fallBehind()
	c.offerBacklogResync()
	return false
}

func (c *Conn) sendLoadState(frame []byte) {
	c.sendMu.Lock()
	defer c.sendMu.Unlock()
	if !c.pushState(frame) {
		c.owedState = frame
		return
	}
	c.owedState = nil
	c.settled()
}

func (c *Conn) sendState(frame []byte) {
	c.sendMu.Lock()
	defer c.sendMu.Unlock()
	c.pushState(frame)
}

func (c *Conn) pushState(frame []byte) bool {
	if c.sink.TrySend(frame) {
		return true
	}
	c.fallBehind()
	return false
}

func (c *Conn) fallBehind() {
	c.backlog = owed
	c.table.markBacklog(c)
}

func (c *Conn) offerBacklogResync() {
	if c.repay() {
		c.backlog = skipping
	}
}

func (c *Conn) settleBacklog() bool {
	if c.backlog == owed && !c.repay() {
		return false
	}
	c.settled()
	return true
}

func (c *Conn) repay() bool {
	if !c.sink.Drained() {
		return false
	}
	if c.owedState != nil {
		if !c.sink.TrySend(c.owedState) {
			return false
		}
		c.owedState = nil
		return true
	}
	if !c.sink.TrySend(resyncFrames[clientBacklog]) {
		return false
	}
	c.table.stats.countResync(clientBacklog)
	return true
}

func (c *Conn) resync(reason chatimv1.ResyncReason) {
	c.sendMu.Lock()
	defer c.sendMu.Unlock()
	c.owedState = nil
	if !c.pushState(resyncFrames[reason]) {
		return
	}
	c.table.stats.countResync(reason)
	c.settled()
}

func (c *Conn) settled() {
	if c.backlog != flowing {
		c.backlog = flowing
		c.table.unmarkBacklog(c)
	}
}

func (t *Table) markBacklog(c *Conn) {
	t.backlogMu.Lock()
	defer t.backlogMu.Unlock()
	if _, ok := t.backlog[c]; !ok {
		t.backlog[c] = struct{}{}
		t.backlogged.Add(1)
	}
}

func (t *Table) unmarkBacklog(c *Conn) {
	t.backlogMu.Lock()
	defer t.backlogMu.Unlock()
	if _, ok := t.backlog[c]; ok {
		delete(t.backlog, c)
		t.backlogged.Add(-1)
	}
}

func (t *Table) caughtUp() {
	if t.backlogged.Load() == 0 {
		return
	}
	t.backlogMu.Lock()
	conns := make([]*Conn, 0, len(t.backlog))
	for c := range t.backlog {
		conns = append(conns, c)
	}
	t.backlogMu.Unlock()
	for _, c := range conns {
		c.sendMu.Lock()
		if c.backlog != flowing {
			c.settleBacklog()
		}
		c.sendMu.Unlock()
	}
}
