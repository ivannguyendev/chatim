package e2e

import (
	"fmt"
	"slices"
	"strconv"
	"strings"

	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

const (
	KindRoomUpdated = "room_updated"
	KindPreferences = "room_preferences_changed"
)

type RoomLine struct {
	Room    string
	Unread  uint32
	Preview uint64
	Sender  string
	Kind    string
}

func RoomInfoEventID(room string, infoVer uint64) string {
	return room + "-info-v" + strconv.FormatUint(infoVer, 10)
}

func PreferencesEventID(room, user string, preferenceVer uint64) string {
	return room + "-pf-" + user + "-v" + strconv.FormatUint(preferenceVer, 10)
}

func RoomInfoPayload(name, avatar string, infoVer uint64) string {
	return "name=" + name + " avatar=" + avatar + " info_ver=" + strconv.FormatUint(infoVer, 10)
}

func PreferencesPayload(pinned, muted, archived bool, ver uint64) string {
	return fmt.Sprintf("pinned=%v muted=%v archived=%v ver=%d", pinned, muted, archived, ver)
}

func StatePayload(s *chatimv1.MyRoomState) string {
	return PreferencesPayload(s.GetPinnedAt() != nil, s.GetMutedUntil() != nil, s.GetArchivedAt() != nil, s.GetPreferenceVer())
}

func CreatedPayload(m *chatimv1.Message) string {
	var parts []string
	if links := LinksPayload(m); links != "" {
		parts = append(parts, links)
	}
	if m.GetKind() == chatimv1.MessageKind_MESSAGE_KIND_SYSTEM {
		parts = append(parts, "kind=system")
	}
	if m.GetSkipUnread() {
		parts = append(parts, "skip_unread")
	}
	if len(m.GetMeta()) > 0 {
		parts = append(parts, "meta="+string(m.GetMeta()))
	}
	return strings.Join(parts, " ")
}

func roomOf(subject string, ev *chatimv1.Event) (Event, bool) {
	out := Event{Room: ev.GetRoomId(), ID: ev.GetId(), Subject: subject}
	switch {
	case ev.GetRoomUpdated() != nil:
		r := ev.GetRoomUpdated().GetRoom()
		out.Kind, out.Text = KindRoomUpdated, RoomInfoPayload(r.GetName(), r.GetAvatar(), r.GetInfoVer())
	case ev.GetRoomPreferencesChanged() != nil:
		p := ev.GetRoomPreferencesChanged()
		out.Kind, out.User, out.Text = KindPreferences, p.GetUser(), StatePayload(p.GetState())
	default:
		return Event{}, false
	}
	return out, true
}

func LineOf(it *chatimv1.RoomItem) RoomLine {
	line := RoomLine{Room: it.GetRoom().GetId(), Unread: it.GetUnread().GetCount()}
	if p := it.GetPreview(); p != nil {
		line.Preview, line.Sender = p.GetSeq(), p.GetSender()
		line.Kind = strings.ToLower(strings.TrimPrefix(p.GetKind().String(), "MESSAGE_KIND_"))
	}
	return line
}

func CheckFirst(items []*chatimv1.RoomItem, want RoomLine) error {
	if len(items) == 0 {
		return fmt.Errorf("room list is empty, want room %s first", want.Room)
	}
	if got := LineOf(items[0]); got != want {
		return fmt.Errorf("room list starts with %+v, want %+v", got, want)
	}
	return nil
}

func CheckLine(items []*chatimv1.RoomItem, want RoomLine) error {
	i := slices.IndexFunc(items, func(it *chatimv1.RoomItem) bool { return it.GetRoom().GetId() == want.Room })
	if i < 0 {
		return fmt.Errorf("room list %v misses room %s", RoomIDsOf(items), want.Room)
	}
	if got := LineOf(items[i]); got != want {
		return fmt.Errorf("room list has %+v, want %+v", got, want)
	}
	return nil
}

func RoomIDsOf(items []*chatimv1.RoomItem) []string {
	out := make([]string, len(items))
	for i, it := range items {
		out[i] = it.GetRoom().GetId()
	}
	return out
}

func CheckOrder(what string, items []*chatimv1.RoomItem, want []string) error {
	if got := RoomIDsOf(items); !slices.Equal(got, want) {
		return fmt.Errorf("%s lists rooms %v, want %v", what, got, want)
	}
	return nil
}
