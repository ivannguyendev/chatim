package grpcsrv

import (
	"slices"
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/ivannguyendev/chatim/modules/mention/internal/domain"
	"github.com/ivannguyendev/chatim/modules/mention/internal/store"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

func (r mentionRead) supersededBy(m *chatimv1.Message, d domain.Mention) bool {
	room, ok := r.rooms[d.Key.Room]
	if !ok {
		return false
	}
	since := store.MentionSince(room)
	own := d.Target.Identity()
	return slices.ContainsFunc(messageTargets(m), func(t domain.Target) bool {
		return t.Identity() != own && t.At.After(d.CreatedAt) && r.reaches(t, since)
	})
}

func (r mentionRead) reaches(t domain.Target, roomSince time.Time) bool {
	if !t.At.After(roomSince) {
		return false
	}
	switch t.Kind {
	case domain.TargetUser:
		return t.ID == r.who.User
	case domain.TargetGroup:
		since, mine := r.groups[t.ID]
		return mine && !t.At.Before(since)
	case domain.TargetAll:
		return true
	default:
		return false
	}
}

func messageTargets(m *chatimv1.Message) []domain.Target {
	out := make([]domain.Target, 0, len(m.GetMentionTargets())+1)
	for _, mt := range m.GetMentionTargets() {
		kind, ok := mentionKinds[mt.GetKind()]
		if ok {
			out = append(out, domain.Target{Kind: kind, ID: mt.GetId(), At: targetTime(mt.GetAt(), m)})
		}
	}
	if m.GetMentionAll() {
		out = append(out, domain.Target{Kind: domain.TargetAll, At: targetTime(m.GetMentionAllAt(), m)})
	}
	return out
}

var mentionKinds = map[chatimv1.MentionKind]domain.TargetKind{
	chatimv1.MentionKind_MENTION_KIND_USER:  domain.TargetUser,
	chatimv1.MentionKind_MENTION_KIND_GROUP: domain.TargetGroup,
}

func targetTime(at *timestamppb.Timestamp, m *chatimv1.Message) time.Time {
	if at == nil || at.CheckValid() != nil {
		at = m.GetCreatedAt()
	}
	if at == nil || at.CheckValid() != nil {
		return time.Time{}
	}
	return at.AsTime().UTC().Truncate(time.Millisecond)
}
