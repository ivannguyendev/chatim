package slotmap

import (
	"encoding/binary"
	"hash/fnv"
)

const Count = 1024

func Of(room uint64) uint16 { return uint16(mix64(room) % Count) }

func Score(slot uint16, core string) uint64 {
	h := fnv.New64a()
	var b [2]byte
	binary.BigEndian.PutUint16(b[:], slot)
	_, _ = h.Write(b[:])
	_, _ = h.Write([]byte(core))
	return mix64(h.Sum64())
}

func Preferred(slot uint16, cores []string) string {
	best, bestScore := "", uint64(0)
	for _, c := range cores {
		s := Score(slot, c)
		if best == "" || s > bestScore || (s == bestScore && c < best) {
			best, bestScore = c, s
		}
	}
	return best
}

func mix64(x uint64) uint64 {
	x ^= x >> 33
	x *= 0xff51afd7ed558ccd
	x ^= x >> 33
	x *= 0xc4ceb9fe1a85ec53
	x ^= x >> 33
	return x
}
