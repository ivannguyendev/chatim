package view

import (
	"slices"

	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
)

func MaskDeleted(_ Viewer, msgs []domain.Message) []domain.Message {
	return eachCopy(msgs, func(m *domain.Message) {
		*m = m.WithoutDeletedContent()
	})
}

func HideForViewer(v Viewer, msgs []domain.Message) []domain.Message {
	return eachCopy(msgs, func(m *domain.Message) {
		if v.Cleared(m.CreatedAt) || v.HiddenSeqs[m.Seq] {
			m.Hidden, m.Text = true, ""
			m.Mentions, m.MentionAll, m.Forward, m.ReplyTo = nil, false, nil, nil
			m.Reactions, m.Replies = domain.ReactionSummary{}, domain.ReplyCount{}
		}
	})
}

func eachCopy(msgs []domain.Message, f func(m *domain.Message)) []domain.Message {
	out := slices.Clone(msgs)
	for i := range out {
		f(&out[i])
	}
	return out
}
