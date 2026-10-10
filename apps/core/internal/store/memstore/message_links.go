package memstore

import (
	"slices"

	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
)

func detached(m domain.Message) domain.Message {
	if m.ReplyTo != nil {
		r := *m.ReplyTo
		m.ReplyTo = &r
	}
	if m.Forward != nil {
		f := *m.Forward
		m.Forward = &f
	}
	m.Mentions = slices.Clone(m.Mentions)
	m.Reactions.Counts = slices.Clone(m.Reactions.Counts)
	return m
}

func detachedAll(msgs []domain.Message) []domain.Message {
	out := make([]domain.Message, len(msgs))
	for i, m := range msgs {
		out[i] = detached(m)
	}
	return out
}
