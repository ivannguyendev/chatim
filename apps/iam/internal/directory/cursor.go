package directory

import (
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"math"
	"strings"
	"unicode/utf8"

	"github.com/ivannguyendev/chatim/apps/iam/internal/store"
	"github.com/ivannguyendev/chatim/pkg/apperr"
)

const xidBytes = 8

var (
	cursorEncoding = base64.RawURLEncoding.Strict()
	errBadCursor   = fmt.Errorf("%w: malformed directory cursor", apperr.ErrInvalidArgument)
)

func EncodePosition(p store.Position) string {
	if p == (store.Position{}) {
		return ""
	}
	buf := make([]byte, 0, xidBytes+2*binary.MaxVarintLen64+len(p.Tenant)+len(p.ID))
	buf = binary.BigEndian.AppendUint64(buf, p.Xid)
	buf = binary.AppendVarint(buf, int64(p.Kind))
	buf = binary.AppendUvarint(buf, uint64(len(p.Tenant)))
	buf = append(buf, p.Tenant...)
	buf = append(buf, p.ID...)
	return cursorEncoding.EncodeToString(buf)
}

func DecodePosition(cursor string) (store.Position, error) {
	if cursor == "" {
		return store.Position{}, nil
	}
	if strings.ContainsAny(cursor, "\r\n") {
		return store.Position{}, errBadCursor
	}
	raw, err := cursorEncoding.DecodeString(cursor)
	if err != nil || len(raw) < xidBytes {
		return store.Position{}, errBadCursor
	}
	xid, rest := binary.BigEndian.Uint64(raw), raw[xidBytes:]
	kind, n := binary.Varint(rest)
	if n <= 0 || kind < 0 || kind > math.MaxInt16 {
		return store.Position{}, errBadCursor
	}
	rest = rest[n:]
	tenantLen, n := binary.Uvarint(rest)
	if n <= 0 {
		return store.Position{}, errBadCursor
	}
	rest = rest[n:]
	if tenantLen > uint64(len(rest)) {
		return store.Position{}, errBadCursor
	}
	p := store.Position{Xid: xid, Kind: store.ChangeKind(kind), Tenant: string(rest[:tenantLen]), ID: string(rest[tenantLen:])}
	if !validText(p.Tenant) || !validText(p.ID) {
		return store.Position{}, errBadCursor
	}
	return p, nil
}

func validText(s string) bool {
	return utf8.ValidString(s) && !strings.ContainsRune(s, 0)
}
