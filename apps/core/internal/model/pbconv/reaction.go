package pbconv

import (
	"strconv"
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

const ReactionsCounter = "reactions"

func ReactionEventID(room, thread, seq uint64, user string, change uint32) string {
	return MessageEventID(room, thread, seq) + "-" + user + "-n" + strconv.FormatUint(uint64(change), 10)
}

func ReactionCountsEventID(room, thread, seq, version uint64) string {
	return MessageCountsEventID(room, thread, seq, ReactionsCounter, version)
}

func ReactionSummary(s domain.ReactionSummary) *chatimv1.ReactionSummary {
	if s.Version == 0 && len(s.Counts) == 0 {
		return nil
	}
	counts := make([]*chatimv1.ReactionCount, len(s.Counts))
	for i, c := range s.Counts {
		counts[i] = &chatimv1.ReactionCount{Emoji: c.Emoji, Count: c.Count}
	}
	return &chatimv1.ReactionSummary{Counts: counts, Ver: s.Version}
}

func ReactionChanged(roomType domain.RoomType, r domain.Reaction) *chatimv1.Event {
	return &chatimv1.Event{
		Id:         ReactionEventID(r.Room, r.Thread, r.Seq, r.User, r.N),
		Tenant:     r.Tenant,
		RoomId:     RoomID(r.Room),
		RoomType:   RoomType(roomType),
		ThreadRoot: r.Thread,
		Seq:        r.Seq,
		Actor:      r.User,
		Ts:         timestamppb.New(r.At),
		Payload: &chatimv1.Event_ReactionChanged{ReactionChanged: &chatimv1.ReactionChanged{
			User: r.User, Emoji: r.Emoji, PreviousEmoji: r.Prev, Change: r.N,
		}},
	}
}

func CountsChanged(roomType domain.RoomType, m domain.Message, at time.Time) *chatimv1.Event {
	return &chatimv1.Event{
		Id:         ReactionCountsEventID(m.Room, m.Thread, m.Seq, m.Reactions.Version),
		Tenant:     m.Tenant,
		RoomId:     RoomID(m.Room),
		RoomType:   RoomType(roomType),
		ThreadRoot: m.Thread,
		Seq:        m.Seq,
		Ts:         timestamppb.New(at),
		Payload: &chatimv1.Event_CountsChanged{CountsChanged: &chatimv1.CountsChanged{
			Counter: ReactionsCounter, Reactions: ReactionSummary(m.Reactions),
		}},
	}
}
