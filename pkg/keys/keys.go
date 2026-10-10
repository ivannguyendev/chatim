package keys

import (
	"encoding/binary"
	"errors"
)

const (
	MsgLen   = 24
	EventLen = 16
	EditLen  = 28
	PinLen   = 16
)

var ErrLength = errors.New("keys: invalid key length")

var be = binary.BigEndian

func Msg(room, threadRoot, seq uint64) []byte {
	b := make([]byte, MsgLen)
	putMsg(b, room, threadRoot, seq)
	return b
}

func ParseMsg(b []byte) (room, threadRoot, seq uint64, err error) {
	if len(b) != MsgLen {
		return 0, 0, 0, ErrLength
	}
	return be.Uint64(b[0:8]), be.Uint64(b[8:16]), be.Uint64(b[16:24]), nil
}

func MsgRange(room, threadRoot, fromSeq, toSeq uint64) (lo, hi []byte) {
	return Msg(room, threadRoot, fromSeq), Msg(room, threadRoot, toSeq)
}

func Event(room, pts uint64) []byte {
	b := make([]byte, EventLen)
	be.PutUint64(b[0:8], room)
	be.PutUint64(b[8:16], pts)
	return b
}

func ParseEvent(b []byte) (room, pts uint64, err error) {
	if len(b) != EventLen {
		return 0, 0, ErrLength
	}
	return be.Uint64(b[0:8]), be.Uint64(b[8:16]), nil
}

func Edit(room, threadRoot, seq uint64, version uint32) []byte {
	b := make([]byte, EditLen)
	putMsg(b, room, threadRoot, seq)
	be.PutUint32(b[24:28], version)
	return b
}

func ParseEdit(b []byte) (room, threadRoot, seq uint64, version uint32, err error) {
	if len(b) != EditLen {
		return 0, 0, 0, 0, ErrLength
	}
	return be.Uint64(b[0:8]), be.Uint64(b[8:16]), be.Uint64(b[16:24]), be.Uint32(b[24:28]), nil
}

func Pin(room, pv uint64) []byte {
	b := make([]byte, PinLen)
	be.PutUint64(b[0:8], room)
	be.PutUint64(b[8:16], pv)
	return b
}

func ParsePin(b []byte) (room, pv uint64, err error) {
	if len(b) != PinLen {
		return 0, 0, ErrLength
	}
	return be.Uint64(b[0:8]), be.Uint64(b[8:16]), nil
}

func putMsg(b []byte, room, threadRoot, seq uint64) {
	be.PutUint64(b[0:8], room)
	be.PutUint64(b[8:16], threadRoot)
	be.PutUint64(b[16:24], seq)
}
