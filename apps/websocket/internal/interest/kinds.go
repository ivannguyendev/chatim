package interest

import (
	"slices"

	"google.golang.org/protobuf/proto"

	"github.com/ivannguyendev/chatim/apps/websocket/internal/feed"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

type level int

const (
	levelMember level = iota
	levelOpen
	levelOwner
	levelCount
)

type audience int

const (
	toMembers audience = iota
	toViewers
	toOwner
	toMembership
	toReaders
)

const (
	dataMessage = "message"
	dataMention = "mention"
	dataRoom    = "room"
	dataMember  = "member"
	kindAdded   = "member_added"
)

type subjectOf func(*chatimv1.Event) (string, uint32)

type rule struct {
	audience audience
	data     []string
	subject  subjectOf
}

var decodeOptions = proto.UnmarshalOptions{DiscardUnknown: true}

var kinds = map[string]rule{
	"msg_created":              {toMembers, []string{dataMessage, dataMention}, nil},
	"msg_edited":               {toMembers, []string{dataMessage}, nil},
	"msg_deleted":              {toMembers, []string{dataMessage}, nil},
	"room_created":             {toMembers, []string{dataRoom}, nil},
	"room_updated":             {toMembers, []string{dataRoom}, nil},
	kindAdded:                  {toMembership, []string{dataMember}, memberAdded},
	"member_removed":           {toMembership, []string{dataMember}, memberRemoved},
	"member_role_changed":      {toViewers, []string{dataMember}, nil},
	"member_priority_changed":  {toViewers, []string{dataMember}, nil},
	"member_count_changed":     {toViewers, []string{dataRoom}, nil},
	"reaction_changed":         {toViewers, []string{dataMessage}, nil},
	"counts_changed":           {toViewers, []string{dataMessage}, nil},
	"msg_pinned":               {toViewers, []string{dataRoom}, nil},
	"msg_unpinned":             {toViewers, []string{dataRoom}, nil},
	"message_hidden":           {toOwner, []string{dataMember}, messageHidden},
	"history_cleared":          {toOwner, []string{dataMember}, historyCleared},
	"bookmark_changed":         {toOwner, []string{dataMember}, bookmarkChanged},
	"room_preferences_changed": {toOwner, []string{dataMember}, preferencesChanged},
	"read_positions":           {toReaders, []string{dataMember}, nil},
}

func (r rule) accepts(data string) bool { return slices.Contains(r.data, data) }

func decode(env feed.Envelope) (*chatimv1.Event, bool) {
	var ev chatimv1.Event
	if decodeOptions.Unmarshal(env.Data, &ev) != nil || ev.GetTenant() != env.Tenant || ev.GetRoomId() != env.Room {
		return nil, false
	}
	return &ev, true
}

func (r rule) user(env feed.Envelope) (string, uint32, bool) {
	ev, ok := decode(env)
	if !ok {
		return "", 0, false
	}
	id, ver := r.subject(ev)
	return id, ver, id != ""
}

func readers(env feed.Envelope) ([]string, bool) {
	ev, ok := decode(env)
	if !ok || ev.GetReadPositions() == nil {
		return nil, false
	}
	entries := ev.GetReadPositions().GetEntries()
	out := make([]string, 0, len(entries))
	for _, e := range entries {
		if e.GetUser() != "" {
			out = append(out, e.GetUser())
		}
	}
	return out, true
}

func memberAdded(ev *chatimv1.Event) (string, uint32) {
	p := ev.GetMemberAdded()
	return p.GetUser(), p.GetVer()
}

func memberRemoved(ev *chatimv1.Event) (string, uint32) {
	p := ev.GetMemberRemoved()
	return p.GetUser(), p.GetVer()
}

func messageHidden(ev *chatimv1.Event) (string, uint32) { return ev.GetMessageHidden().GetUser(), 0 }

func historyCleared(ev *chatimv1.Event) (string, uint32) { return ev.GetHistoryCleared().GetUser(), 0 }

func bookmarkChanged(ev *chatimv1.Event) (string, uint32) {
	return ev.GetBookmarkChanged().GetUser(), 0
}

func preferencesChanged(ev *chatimv1.Event) (string, uint32) {
	return ev.GetRoomPreferencesChanged().GetUser(), 0
}
