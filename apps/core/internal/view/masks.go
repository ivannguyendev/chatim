package view

import (
	"slices"

	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
)

func MaskDeleted(_ Viewer, msgs []domain.Message) []domain.Message {
	return eachCopy(msgs, func(m *domain.Message) {
		if m.Deleted {
			m.Text = ""
			m.Reactions = domain.ReactionSummary{}
		}
	})
}

func HideForViewer(v Viewer, msgs []domain.Message) []domain.Message {
	return eachCopy(msgs, func(m *domain.Message) {
		if v.Cleared(m.CreatedAt) || v.HiddenSeqs[m.Seq] {
			m.Hidden = true
			m.Text = ""
			m.Reactions = domain.ReactionSummary{}
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
