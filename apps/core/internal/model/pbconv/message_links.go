package pbconv

import (
	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

func ReplyRef(r *domain.ReplyRef) *chatimv1.ReplyRef {
	if r == nil {
		return nil
	}
	return &chatimv1.ReplyRef{ThreadRoot: r.Thread, Seq: r.Seq}
}

func DomainReplyRef(r *chatimv1.ReplyRef) *domain.ReplyRef {
	if r == nil {
		return nil
	}
	return &domain.ReplyRef{Thread: r.GetThreadRoot(), Seq: r.GetSeq()}
}

func ForwardRef(f *domain.ForwardRef) *chatimv1.ForwardRef {
	if f == nil {
		return nil
	}
	return &chatimv1.ForwardRef{
		RoomId: RoomID(f.Room), ThreadRoot: f.Thread, Seq: f.Seq, Author: f.Author, SentAt: optionalTime(f.SentAt),
	}
}

func MentionTargets(targets []domain.MentionTarget) []*chatimv1.MentionTarget {
	if len(targets) == 0 {
		return nil
	}
	out := make([]*chatimv1.MentionTarget, len(targets))
	for i, t := range targets {
		out[i] = &chatimv1.MentionTarget{Kind: mentionKind(t.Kind), Id: t.ID}
	}
	return out
}

func DomainMentionTargets(targets []*chatimv1.MentionTarget) []domain.MentionTarget {
	if len(targets) == 0 {
		return nil
	}
	out := make([]domain.MentionTarget, len(targets))
	for i, t := range targets {
		out[i] = domain.MentionTarget{Kind: domainMentionKind(t.GetKind()), ID: t.GetId()}
	}
	return out
}

func ReplyCount(c domain.ReplyCount) *chatimv1.ReplyCount {
	if c.N == 0 && c.Version == 0 {
		return nil
	}
	return &chatimv1.ReplyCount{Count: c.N, Ver: c.Version}
}

func mentionKind(k domain.MentionKind) chatimv1.MentionKind {
	switch k {
	case domain.MentionUser:
		return chatimv1.MentionKind_MENTION_KIND_USER
	case domain.MentionGroup:
		return chatimv1.MentionKind_MENTION_KIND_GROUP
	default:
		return chatimv1.MentionKind_MENTION_KIND_UNSPECIFIED
	}
}

func domainMentionKind(k chatimv1.MentionKind) domain.MentionKind {
	switch k {
	case chatimv1.MentionKind_MENTION_KIND_USER:
		return domain.MentionUser
	case chatimv1.MentionKind_MENTION_KIND_GROUP:
		return domain.MentionGroup
	default:
		return 0
	}
}
