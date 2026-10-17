package grpcsrv

import (
	"context"
	"slices"

	"github.com/ivannguyendev/chatim/apps/mention/internal/domain"
	"github.com/ivannguyendev/chatim/apps/mention/internal/store"
	"github.com/ivannguyendev/chatim/pkg/ids"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

func (s *Service) mentionResponse(ctx context.Context, r mentionRead, page store.MentionPage, reasons map[domain.MessageKey][]domain.Target) (*chatimv1.ListMentionsResponse, error) {
	resp := &chatimv1.ListMentionsResponse{}
	if !page.Next.At.IsZero() {
		resp.Next = encodePageCursor(page.Next)
	}
	if len(page.Items) == 0 {
		return resp, nil
	}
	keys := make([]domain.MessageKey, len(page.Items))
	for i, d := range page.Items {
		keys[i] = d.Key
	}
	msgs, err := s.core.VisibleMessages(ctx, r.who.Tenant, r.who.User, keys)
	if err != nil {
		return nil, dependencyErr("visible messages", err)
	}
	visible := make(map[domain.MessageKey]*chatimv1.Message, len(msgs))
	for _, m := range msgs {
		if key, ok := keyOf(m); ok {
			visible[key] = m
		}
	}
	for _, d := range page.Items {
		if m, ok := visible[d.Key]; ok && carriesAny(m, reasons[d.Key]) && !r.supersededBy(m, d) {
			resp.Items = append(resp.Items, &chatimv1.MentionItem{Message: m, Read: r.isRead(d)})
		}
	}
	return resp, nil
}

func mentionReasons(sources ...store.MentionSource) map[domain.MessageKey][]domain.Target {
	out := make(map[domain.MessageKey][]domain.Target)
	for _, src := range sources {
		for _, d := range src.Items {
			out[d.Key] = append(out[d.Key], d.Target)
		}
	}
	return out
}

func carriesAny(m *chatimv1.Message, targets []domain.Target) bool {
	return slices.ContainsFunc(targets, func(t domain.Target) bool { return carries(m, t) })
}

func keyOf(m *chatimv1.Message) (domain.MessageKey, bool) {
	room, err := ids.ParseRoomID(m.GetRoomId())
	return domain.MessageKey{Room: room, Thread: m.GetThreadRoot(), Seq: m.GetSeq()}, err == nil
}

func carries(m *chatimv1.Message, t domain.Target) bool {
	var kind chatimv1.MentionKind
	switch t.Kind {
	case domain.TargetAll:
		return m.GetMentionAll()
	case domain.TargetUser:
		kind = chatimv1.MentionKind_MENTION_KIND_USER
	case domain.TargetGroup:
		kind = chatimv1.MentionKind_MENTION_KIND_GROUP
	default:
		return false
	}
	return slices.ContainsFunc(m.GetMentionTargets(), func(mt *chatimv1.MentionTarget) bool {
		return mt.GetKind() == kind && mt.GetId() == t.ID
	})
}
