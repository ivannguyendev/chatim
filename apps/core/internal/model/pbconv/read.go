package pbconv

import (
	"cmp"
	"crypto/sha256"
	"encoding/hex"
	"slices"
	"strconv"
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

func HiddenEventID(room uint64, user string, thread, seq uint64) string {
	return RoomID(room) + "-hd-" + user + "-" + strconv.FormatUint(thread, 10) + "-" + strconv.FormatUint(seq, 10)
}

func ClearedEventID(room uint64, user string, at time.Time) string {
	return RoomID(room) + "-cl-" + user + "-" + strconv.FormatInt(at.UnixMilli(), 10)
}

func MessageHidden(r domain.Room, user string, thread, seq uint64, at time.Time) *chatimv1.Event {
	ev := roomEnvelope(r.ID, r.Tenant, r.Type, HiddenEventID(r.ID, user, thread, seq), user, at)
	ev.ThreadRoot, ev.Seq = thread, seq
	ev.Payload = &chatimv1.Event_MessageHidden{MessageHidden: &chatimv1.MessageHidden{User: user, ThreadRoot: thread, Seq: seq}}
	return ev
}

func HistoryCleared(r domain.Room, user string, clearedAt, at time.Time) *chatimv1.Event {
	ev := roomEnvelope(r.ID, r.Tenant, r.Type, ClearedEventID(r.ID, user, clearedAt), user, at)
	ev.Payload = &chatimv1.Event_HistoryCleared{HistoryCleared: &chatimv1.HistoryCleared{User: user, ClearedAt: timestamppb.New(clearedAt)}}
	return ev
}

func ReadPositionsEventID(room uint64, entries []*chatimv1.ReadEntry) string {
	h := sha256.New()
	line := make([]byte, 0, 96)
	for i, e := range sortedReadEntries(entries) {
		line = line[:0]
		if i > 0 {
			line = append(line, '\n')
		}
		line = append(line, e.GetUser()...)
		line = append(line, ':')
		line = strconv.AppendUint(line, e.GetReadSeq(), 10)
		line = append(line, ':')
		line = strconv.AppendUint(line, e.GetReadVer(), 10)
		h.Write(line)
	}
	return RoomID(room) + "-rp-" + hex.EncodeToString(h.Sum(nil))[:16]
}

func ReadPositions(r domain.Room, entries []*chatimv1.ReadEntry, at time.Time) *chatimv1.Event {
	sorted := sortedReadEntries(entries)
	ev := roomEnvelope(r.ID, r.Tenant, r.Type, ReadPositionsEventID(r.ID, sorted), "", at)
	ev.Payload = &chatimv1.Event_ReadPositions{ReadPositions: &chatimv1.ReadPositions{Entries: sorted}}
	return ev
}

func sortedReadEntries(entries []*chatimv1.ReadEntry) []*chatimv1.ReadEntry {
	if slices.IsSortedFunc(entries, compareReadEntries) {
		return entries
	}
	return slices.SortedFunc(slices.Values(entries), compareReadEntries)
}

func compareReadEntries(a, b *chatimv1.ReadEntry) int {
	return cmp.Compare(a.GetUser(), b.GetUser())
}
