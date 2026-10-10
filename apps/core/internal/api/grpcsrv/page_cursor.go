package grpcsrv

import (
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"math"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/pkg/apperr"
	"github.com/ivannguyendev/chatim/pkg/keys"
)

const pageCursorSize = 8 + 24

var errBadCursor = fmt.Errorf("%w: cursor", apperr.ErrInvalidArgument)

func encodePageCursor(at time.Time, key store.MsgKey) string {
	raw := binary.BigEndian.AppendUint64(make([]byte, 0, pageCursorSize), uint64(at.UnixMilli()))
	raw = append(raw, keys.Msg(key.Room, key.Thread, key.Seq)...)
	return base64.RawURLEncoding.EncodeToString(raw)
}

func decodePageCursor(s string) (time.Time, store.MsgKey, error) {
	if s == "" {
		return time.Time{}, store.MsgKey{}, nil
	}
	raw, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil || len(raw) != pageCursorSize {
		return time.Time{}, store.MsgKey{}, errBadCursor
	}
	ms := binary.BigEndian.Uint64(raw[:8])
	room, thread, seq, err := keys.ParseMsg(raw[8:])
	key := store.MsgKey{Room: room, Thread: thread, Seq: seq}
	if err != nil || ms == 0 || ms > math.MaxInt64 || key.Validate() != nil {
		return time.Time{}, store.MsgKey{}, errBadCursor
	}
	return time.UnixMilli(int64(ms)).UTC(), key, nil
}

func encodeBookmarkCursor(b domain.Bookmark) string {
	return encodePageCursor(b.At, store.BookmarkKeyOf(b))
}

func decodeBookmarkCursor(s string) (store.BookmarkCursor, error) {
	at, key, err := decodePageCursor(s)
	return store.BookmarkCursor{At: at, Key: key}, err
}
