package e2e

import (
	"context"
	"fmt"
	"sync"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"

	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

const (
	FrameEvent        = "event"
	FrameNotification = "notification"
	FrameRoomClosed   = "room_closed"
	FrameResync       = "resync"
	FrameReady        = "ready"
	FrameGap          = "gap"
)

type WSEntry struct {
	Frame string
	Kind  string
	ID    string
	Room  string
	Seq   uint64
	To    uint64
	User  string
	Ver   uint64
}

type WSMatch WSEntry

func (m WSMatch) Matches(e WSEntry) bool {
	return same(m.Frame, e.Frame) && same(m.Kind, e.Kind) && same(m.ID, e.ID) && same(m.Room, e.Room) &&
		same(m.User, e.User) && (m.Seq == 0 || m.Seq == e.Seq) && (m.Ver == 0 || m.Ver == e.Ver)
}

func (m WSMatch) String() string {
	return fmt.Sprintf("%s %s id=%q room=%q seq=%d user=%q", m.Frame, m.Kind, m.ID, m.Room, m.Seq, m.User)
}

func same(want, got string) bool { return want == "" || want == got }

type WSFeed struct {
	mu      sync.Mutex
	entries []WSEntry
	changed chan struct{}
	events  *EventCheck
	typing  TypingView
}

func NewWSFeed() *WSFeed { return &WSFeed{changed: make(chan struct{}), events: NewEventCheck()} }

func (f *WSFeed) Event(ev *chatimv1.Event) {
	if reads := ev.GetReadPositions(); reads != nil {
		for _, e := range reads.GetEntries() {
			f.add(WSEntry{Frame: FrameEvent, Kind: "read_positions", ID: ev.GetId(), Room: ev.GetRoomId(), Seq: e.GetReadSeq(), User: e.GetUser(), Ver: e.GetReadVer()})
		}
		return
	}
	kind, user := payloadOf(ev, ev.ProtoReflect().Descriptor().Oneofs().ByName("payload"))
	f.add(WSEntry{Frame: FrameEvent, Kind: kind, ID: ev.GetId(), Room: ev.GetRoomId(), Seq: ev.GetSeq(), User: user})
}

func (f *WSFeed) Notification(n *chatimv1.Notification) {
	kind, _ := payloadOf(n, n.ProtoReflect().Descriptor().Oneofs().ByName("payload"))
	f.add(WSEntry{Frame: FrameNotification, Kind: kind, ID: n.GetId(), Room: n.GetRoomId(), Seq: n.GetSeq(), User: n.GetUser()})
}

func (f *WSFeed) RoomClosed(room string, reason chatimv1.RoomClosedReason) {
	f.add(WSEntry{Frame: FrameRoomClosed, Kind: reason.String(), Room: room})
}

func (f *WSFeed) Resync(reason chatimv1.ResyncReason) {
	f.add(WSEntry{Frame: FrameResync, Kind: reason.String()})
}

func (f *WSFeed) Ready() { f.add(WSEntry{Frame: FrameReady}) }

func (f *WSFeed) Gap(g Gap) {
	f.add(WSEntry{Frame: FrameGap, Room: g.Room, Seq: g.From, To: g.To})
}

func (f *WSFeed) Len() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.entries)
}

func (f *WSFeed) Entries(from int) []WSEntry {
	f.mu.Lock()
	defer f.mu.Unlock()
	if from >= len(f.entries) {
		return nil
	}
	return append([]WSEntry(nil), f.entries[max(from, 0):]...)
}

func (f *WSFeed) Find(from int, m WSMatch) (int, WSEntry, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.find(from, m)
}

func (f *WSFeed) Count(m WSMatch) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, e := range f.entries {
		if m.Matches(e) {
			n++
		}
	}
	return n
}

func (f *WSFeed) Wait(ctx context.Context, from int, m WSMatch) (int, error) {
	for {
		f.mu.Lock()
		i, _, ok := f.find(from, m)
		changed := f.changed
		f.mu.Unlock()
		if ok {
			return i, nil
		}
		select {
		case <-changed:
		case <-ctx.Done():
			return -1, fmt.Errorf("waiting for %v: %w", m, ctx.Err())
		}
	}
}

func (f *WSFeed) add(e WSEntry) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.entries = append(f.entries, e)
	close(f.changed)
	f.changed = make(chan struct{})
}

func (f *WSFeed) find(from int, m WSMatch) (int, WSEntry, bool) {
	for i := max(from, 0); i < len(f.entries); i++ {
		if m.Matches(f.entries[i]) {
			return i, f.entries[i], true
		}
	}
	return -1, WSEntry{}, false
}

func payloadOf(m proto.Message, oneof protoreflect.OneofDescriptor) (string, string) {
	msg := m.ProtoReflect()
	fd := msg.WhichOneof(oneof)
	if fd == nil {
		return "", ""
	}
	payload := msg.Get(fd).Message()
	user := ""
	if uf := payload.Descriptor().Fields().ByName("user"); uf != nil && uf.Kind() == protoreflect.StringKind {
		user = payload.Get(uf).String()
	}
	return string(fd.Name()), user
}
