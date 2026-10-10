package pbconv

import "strconv"

func BookmarkEventID(room, thread, seq uint64, user string, ver uint32) string {
	return RoomID(room) + "-bm-" + strconv.FormatUint(thread, 10) + "-" + strconv.FormatUint(seq, 10) + "-" + user + "-v" + strconv.FormatUint(uint64(ver), 10)
}
