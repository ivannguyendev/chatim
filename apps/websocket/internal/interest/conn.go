package interest

import (
	"strconv"
	"sync"
	"time"

	"github.com/ivannguyendev/chatim/pkg/ids"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

type pendingOpen struct {
	timer *time.Timer
}

type Conn struct {
	table   *Table
	user    *user
	sink    Sink
	gone    bool
	readied bool
	early   []string
	open    map[uint64]struct{}
	pending map[uint64]*pendingOpen
	sendMu  sync.Mutex
	backlog backlogState
}

func newConn(t *Table, u *user, sink Sink) *Conn {
	return &Conn{table: t, user: u, sink: sink, open: map[uint64]struct{}{}, pending: map[uint64]*pendingOpen{}}
}

func (c *Conn) Open(raw string) {
	u := c.user
	u.mu.Lock()
	defer u.mu.Unlock()
	switch {
	case c.gone || u.gone:
	case !u.ready || u.suspended:
		c.early = append(c.early, raw)
	default:
		c.openRoom(raw)
	}
}

func (c *Conn) LoadRooms() {
	u := c.user
	u.mu.Lock()
	again := !c.gone && !u.gone && u.failed
	if again {
		u.failed = false
	}
	u.mu.Unlock()
	if again {
		c.table.loadAgain(u)
	}
}

func (c *Conn) Close(raw string) {
	room, err := ids.ParseRoomID(raw)
	if err != nil {
		return
	}
	u := c.user
	u.mu.Lock()
	defer u.mu.Unlock()
	if c.gone {
		return
	}
	kept := c.early[:0]
	for _, early := range c.early {
		if id, err := ids.ParseRoomID(early); err != nil || id != room {
			kept = append(kept, early)
		}
	}
	c.early = kept
	c.closeRoom(room)
}

func (c *Conn) openRoom(raw string) {
	room, err := ids.ParseRoomID(raw)
	if err != nil {
		c.reject(raw)
		return
	}
	if _, ok := c.open[room]; ok {
		return
	}
	if _, ok := c.user.rooms[room]; ok {
		c.activate(room)
		return
	}
	if _, ok := c.pending[room]; ok {
		return
	}
	p := &pendingOpen{}
	p.timer = time.AfterFunc(c.table.cfg.OpenPending, func() { c.expire(room, p) })
	c.pending[room] = p
}

func (c *Conn) activate(room uint64) {
	c.open[room] = struct{}{}
	c.table.setOpen(c, room, true)
}

func (c *Conn) closeRoom(room uint64) bool {
	was := false
	if _, ok := c.open[room]; ok {
		delete(c.open, room)
		c.table.setOpen(c, room, false)
		was = true
	}
	if p, ok := c.pending[room]; ok {
		p.timer.Stop()
		delete(c.pending, room)
		was = true
	}
	return was
}

func (c *Conn) expire(room uint64, p *pendingOpen) {
	u := c.user
	u.mu.Lock()
	defer u.mu.Unlock()
	if c.gone || c.pending[room] != p {
		return
	}
	delete(c.pending, room)
	c.reject(strconv.FormatUint(room, 10))
}

func (c *Conn) reject(raw string) {
	c.table.stats.openRejected.Add(1)
	c.sink.Send(roomClosedFrame(raw, chatimv1.RoomClosedReason_ROOM_CLOSED_REASON_NOT_MEMBER))
}

func (c *Conn) becameReady() {
	c.readied = true
	c.sink.Send(readyFrame)
	c.openEarly()
}

func (c *Conn) resumed(reason chatimv1.ResyncReason) {
	c.resync(reason)
	c.openEarly()
}

func (c *Conn) openEarly() {
	early := c.early
	c.early = nil
	for _, raw := range early {
		c.openRoom(raw)
	}
}

func (c *Conn) suspend() {
	for room, p := range c.pending {
		p.timer.Stop()
		delete(c.pending, room)
		c.early = append(c.early, strconv.FormatUint(room, 10))
	}
	c.sendMu.Lock()
	defer c.sendMu.Unlock()
	if c.backlog != flowing {
		c.backlog = flowing
		c.table.unmarkBacklog(c)
	}
}

func (c *Conn) admitted(room uint64) {
	if p, ok := c.pending[room]; ok {
		p.timer.Stop()
		delete(c.pending, room)
		c.activate(room)
	}
}

func (c *Conn) removed(room uint64) {
	if c.closeRoom(room) {
		c.sink.Send(roomClosedFrame(strconv.FormatUint(room, 10), chatimv1.RoomClosedReason_ROOM_CLOSED_REASON_REMOVED))
	}
}

func (c *Conn) release() {
	c.gone = true
	for room := range c.open {
		c.table.setOpen(c, room, false)
	}
	for _, p := range c.pending {
		p.timer.Stop()
	}
	c.open, c.pending, c.early = nil, nil, nil
	c.table.unmarkBacklog(c)
}
