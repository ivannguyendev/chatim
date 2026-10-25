package interest

import "github.com/ivannguyendev/chatim/pkg/ids"

type offerResult uint8

const (
	offerSkipped offerResult = iota
	offerAccepted
	offerRefused
)

func (c *Conn) SignalRoom(raw string) (uint64, bool) {
	room, err := ids.ParseRoomID(raw)
	if err != nil {
		return 0, false
	}
	u := c.user
	u.mu.Lock()
	defer u.mu.Unlock()
	if c.gone || !u.live() {
		return 0, false
	}
	if _, member := u.rooms[room]; !member {
		return 0, false
	}
	return room, true
}

func (t *Table) Signal(tenant string, room uint64, frame []byte, skipUser string) (delivered, dropped int) {
	for _, c := range t.viewers(roomKey{tenant: tenant, room: room}) {
		if c.user.key.user == skipUser {
			continue
		}
		switch c.offer(frame) {
		case offerAccepted:
			delivered++
		case offerRefused:
			dropped++
		case offerSkipped:
		}
	}
	return delivered, dropped
}

func (c *Conn) offer(frame []byte) offerResult {
	u := c.user
	u.mu.Lock()
	defer u.mu.Unlock()
	if c.gone || !u.live() {
		return offerSkipped
	}
	c.sendMu.Lock()
	defer c.sendMu.Unlock()
	if c.backlog != flowing {
		return offerSkipped
	}
	if c.sink.Offer(frame) {
		return offerAccepted
	}
	return offerRefused
}
