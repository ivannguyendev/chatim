package ids

import (
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"strconv"
)

var ErrInvalidRoomID = errors.New("ids: invalid room id")

func NewRoomID() uint64 {
	var b [8]byte
	for {
		_, _ = rand.Read(b[:])
		if id := binary.BigEndian.Uint64(b[:]) &^ (1 << 63); id != 0 {
			return id
		}
	}
}

func FormatRoomID(id uint64) string { return strconv.FormatUint(id, 10) }

func ParseRoomID(s string) (uint64, error) {
	id, err := strconv.ParseUint(s, 10, 63)
	if err != nil || id == 0 {
		return 0, fmt.Errorf("%w: %q", ErrInvalidRoomID, s)
	}
	return id, nil
}
