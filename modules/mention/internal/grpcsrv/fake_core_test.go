package grpcsrv_test

import (
	"context"
	"sync"
	"time"

	"google.golang.org/protobuf/proto"

	"github.com/ivannguyendev/chatim/modules/mention/internal/domain"
	"github.com/ivannguyendev/chatim/pkg/apperr"
	"github.com/ivannguyendev/chatim/pkg/ids"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

type fakeCore struct {
	mu       sync.Mutex
	members  map[uint64]map[string]domain.MemberRoom
	messages map[domain.MessageKey]*chatimv1.Message
	sent     map[domain.MessageKey]time.Time
	err      error
}

func newFakeCore() *fakeCore {
	return &fakeCore{
		members:  map[uint64]map[string]domain.MemberRoom{},
		messages: map[domain.MessageKey]*chatimv1.Message{},
		sent:     map[domain.MessageKey]time.Time{},
	}
}

func (c *fakeCore) fail(err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.err = err
}

func (c *fakeCore) setMember(room uint64, user string, update func(*domain.MemberRoom)) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.members[room] == nil {
		c.members[room] = map[string]domain.MemberRoom{}
	}
	m, ok := c.members[room][user]
	if !ok {
		m = domain.MemberRoom{Room: room}
	}
	update(&m)
	c.members[room][user] = m
}

func (c *fakeCore) putMessage(key domain.MessageKey, at time.Time, m *chatimv1.Message) {
	c.mu.Lock()
	defer c.mu.Unlock()
	m.RoomId, m.ThreadRoot, m.Seq = ids.FormatRoomID(key.Room), key.Thread, key.Seq
	c.messages[key], c.sent[key] = m, at
}

func (c *fakeCore) editMessage(key domain.MessageKey, edit func(*chatimv1.Message)) {
	c.mu.Lock()
	defer c.mu.Unlock()
	edit(c.messages[key])
}

func (c *fakeCore) RoomsOf(_ context.Context, _, user string) ([]domain.MemberRoom, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.err != nil {
		return nil, c.err
	}
	var out []domain.MemberRoom
	for _, users := range c.members {
		if m, ok := users[user]; ok {
			out = append(out, m)
		}
	}
	return out, nil
}

func (c *fakeCore) SentTimes(_ context.Context, keys []domain.MessageKey) (map[domain.MessageKey]time.Time, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := map[domain.MessageKey]time.Time{}
	for _, k := range keys {
		if at, ok := c.sent[k]; ok {
			out[k] = at
		}
	}
	return out, nil
}

func (c *fakeCore) VisibleMessages(_ context.Context, _, user string, keys []domain.MessageKey) ([]*chatimv1.Message, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	var out []*chatimv1.Message
	for _, k := range keys {
		member, in := c.members[k.Room][user]
		m, ok := c.messages[k]
		if ok && in && c.sent[k].After(member.ClearedAt) && !m.GetDeleted() {
			out = append(out, proto.CloneOf(m))
		}
	}
	return out, nil
}

func (c *fakeCore) Authorize(_ context.Context, _, user string, room uint64, _ domain.Action) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	users, ok := c.members[room]
	if !ok {
		return apperr.ErrNotFound
	}
	if _, in := users[user]; !in {
		return apperr.ErrPermissionDenied
	}
	return nil
}

func (c *fakeCore) RoomMemberCount(_ context.Context, room uint64) (int64, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return int64(len(c.members[room])), c.err
}

func (c *fakeCore) RoomMemberIDs(_ context.Context, room uint64) ([]string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	var out []string
	for user := range c.members[room] {
		out = append(out, user)
	}
	return out, c.err
}
