package grpcsrv

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"math"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
)

func encodeThreadCursor(t store.ReplyThread) string {
	raw := binary.BigEndian.AppendUint64(make([]byte, 0, 16), uint64(t.LastReplyAt.UnixMilli()))
	return base64.RawURLEncoding.EncodeToString(binary.BigEndian.AppendUint64(raw, t.Parent.Seq))
}

func decodeThreadCursor(s string) (store.ReplyThreadCursor, error) {
	if s == "" {
		return store.ReplyThreadCursor{}, nil
	}
	raw, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil || len(raw) != 16 {
		return store.ReplyThreadCursor{}, errBadCursor
	}
	at, ok := cursorTime(raw[:8])
	seq := binary.BigEndian.Uint64(raw[8:])
	if !ok || seq == 0 || seq > math.MaxInt64 {
		return store.ReplyThreadCursor{}, errBadCursor
	}
	return store.ReplyThreadCursor{At: at, Seq: seq}, nil
}

func encodeReactorCursor(c store.ReactorCursor) string {
	raw := binary.BigEndian.AppendUint64(make([]byte, 0, 9+len(c.Emoji)+len(c.User)), uint64(c.At.UnixMilli()))
	raw = append(append(raw, c.User...), 0)
	return base64.RawURLEncoding.EncodeToString(append(raw, c.Emoji...))
}

func decodeReactorCursor(s string) (store.ReactorCursor, error) {
	if s == "" {
		return store.ReactorCursor{}, nil
	}
	raw, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil || len(raw) < 8 {
		return store.ReactorCursor{}, errBadCursor
	}
	at, ok := cursorTime(raw[:8])
	user, emoji, found := bytes.Cut(raw[8:], []byte{0})
	c := store.ReactorCursor{At: at, Emoji: string(emoji), User: string(user)}
	if !ok || !found || domain.ValidUser(c.User) != nil || (c.Emoji != "" && domain.ValidateEmoji(c.Emoji) != nil) {
		return store.ReactorCursor{}, errBadCursor
	}
	return c, nil
}

const memberCursorHead = 1 + 4 + 8

var memberRoleCodes = map[domain.Role]byte{domain.RoleOwner: 0, domain.RoleAdmin: 1, domain.RoleMember: 2}

func encodeMemberCursor(m domain.Member) string {
	raw := append(make([]byte, 0, memberCursorHead+len(m.User)), memberRoleCodes[m.Role])
	raw, _ = binary.Append(raw, binary.BigEndian, m.Priority)
	raw = binary.BigEndian.AppendUint64(raw, uint64(m.JoinedAt.UnixMilli()))
	return base64.RawURLEncoding.EncodeToString(append(raw, m.User...))
}

func decodeMemberCursor(s string) (store.MemberCursor, error) {
	if s == "" {
		return store.MemberCursor{}, nil
	}
	raw, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil || len(raw) <= memberCursorHead || int(raw[0]) >= len(store.MemberListRoles) {
		return store.MemberCursor{}, errBadCursor
	}
	at, ok := cursorTime(raw[5:memberCursorHead])
	c := store.MemberCursor{Role: store.MemberListRoles[raw[0]], JoinedAt: at, User: string(raw[memberCursorHead:])}
	if _, err := binary.Decode(raw[1:5], binary.BigEndian, &c.Priority); err != nil || !ok || domain.ValidUser(c.User) != nil {
		return store.MemberCursor{}, errBadCursor
	}
	return c, nil
}

func cursorTime(raw []byte) (time.Time, bool) {
	ms := binary.BigEndian.Uint64(raw)
	if ms == 0 || ms > math.MaxInt64 {
		return time.Time{}, false
	}
	return time.UnixMilli(int64(ms)).UTC(), true
}
