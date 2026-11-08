package memstore

import (
	"slices"

	"github.com/ivannguyendev/chatim/apps/chat/internal/model/domain"
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
	if len(m.Meta) == 0 {
		m.Meta = nil
	}
	m.Meta = slices.Clone(m.Meta)
	m.Attachments = detachedFiles(m.Attachments)
	return m
}

func detachedFiles(list []domain.Attachment) []domain.Attachment {
	if len(list) == 0 {
		return nil
	}
	out := make([]domain.Attachment, len(list))
	for i, a := range list {
		if len(a.Dim) == 0 {
			a.Dim = nil
		}
		a.Dim = slices.Clone(a.Dim)
		out[i] = a
	}
	return out
}

func detachedAll(msgs []domain.Message) []domain.Message {
	out := make([]domain.Message, len(msgs))
	for i, m := range msgs {
		out[i] = detached(m)
	}
	return out
}
