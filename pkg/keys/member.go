package keys

const (
	roomLen       = 8
	MaxMemberUser = 64
)

func Member(room uint64, user string) []byte {
	b := make([]byte, roomLen, roomLen+len(user))
	be.PutUint64(b, room)
	return append(b, user...)
}

func ParseMember(b []byte) (room uint64, user string, err error) {
	if len(b) <= roomLen || len(b) > roomLen+MaxMemberUser {
		return 0, "", ErrLength
	}
	return be.Uint64(b[:roomLen]), string(b[roomLen:]), nil
}
