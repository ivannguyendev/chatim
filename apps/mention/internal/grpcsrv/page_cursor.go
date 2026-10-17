package grpcsrv

import (
	"encoding/base64"
	"encoding/binary"
	"math"
	"time"

	"github.com/ivannguyendev/chatim/apps/mention/internal/domain"
	"github.com/ivannguyendev/chatim/apps/mention/internal/store"
	"github.com/ivannguyendev/chatim/pkg/keys"
)

const pageCursorSize = 8 + 24

func encodePageCursor(c store.MentionCursor) string {
	raw := binary.BigEndian.AppendUint64(make([]byte, 0, pageCursorSize), uint64(c.At.UnixMilli()))
	raw = append(raw, keys.Msg(c.Key.Room, c.Key.Thread, c.Key.Seq)...)
	return base64.RawURLEncoding.EncodeToString(raw)
}

func decodePageCursor(s string) (store.MentionCursor, error) {
	if s == "" {
		return store.MentionCursor{}, nil
	}
	raw, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil || len(raw) != pageCursorSize {
		return store.MentionCursor{}, invalid("cursor")
	}
	ms := binary.BigEndian.Uint64(raw[:8])
	room, thread, seq, err := keys.ParseMsg(raw[8:])
	key := domain.MessageKey{Room: room, Thread: thread, Seq: seq}
	if err != nil || ms == 0 || ms > math.MaxInt64 || key.Validate() != nil {
		return store.MentionCursor{}, invalid("cursor")
	}
	return store.MentionCursor{At: time.UnixMilli(int64(ms)).UTC(), Key: key}, nil
}
