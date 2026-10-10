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

const bookmarkCursorSize = 8 + 24

var errBadCursor = fmt.Errorf("%w: cursor", apperr.ErrInvalidArgument)

func encodeBookmarkCursor(b domain.Bookmark) string {
	raw := binary.BigEndian.AppendUint64(make([]byte, 0, bookmarkCursorSize), uint64(b.At.UnixMilli()))
	raw = append(raw, keys.Msg(b.Room, b.Thread, b.Seq)...)
	return base64.RawURLEncoding.EncodeToString(raw)
}

func decodeBookmarkCursor(s string) (store.BookmarkCursor, error) {
	if s == "" {
		return store.BookmarkCursor{}, nil
	}
	raw, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil || len(raw) != bookmarkCursorSize {
		return store.BookmarkCursor{}, errBadCursor
	}
	ms := binary.BigEndian.Uint64(raw[:8])
	room, thread, seq, err := keys.ParseMsg(raw[8:])
	key := store.MsgKey{Room: room, Thread: thread, Seq: seq}
	if err != nil || ms == 0 || ms > math.MaxInt64 || key.Validate() != nil {
		return store.BookmarkCursor{}, errBadCursor
	}
	return store.BookmarkCursor{At: time.UnixMilli(int64(ms)).UTC(), Key: key}, nil
}
