package view

import (
	"slices"

	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
)

func MaskDeleted(_ Viewer, msgs []domain.Message) []domain.Message {
	return eachCopy(msgs, func(m *domain.Message) {
		if m.Deleted {
			dropContent(m)
		}
	})
}

func HideForViewer(v Viewer, msgs []domain.Message) []domain.Message {
	return eachCopy(msgs, func(m *domain.Message) {
		if v.Cleared(m.CreatedAt) || v.HiddenSeqs[m.Seq] {
			m.Hidden = true
			dropContent(m)
			m.ReplyTo = nil
		}
	})
}

func dropContent(m *domain.Message) {
	m.Text = ""
	m.Mentions, m.MentionAll = nil, false
	m.Forward = nil
	m.Reactions = domain.ReactionSummary{}
	m.Replies = domain.ReplyCount{}
}

func eachCopy(msgs []domain.Message, f func(m *domain.Message)) []domain.Message {
	out := slices.Clone(msgs)
	for i := range out {
		f(&out[i])
	}
	return out
}
