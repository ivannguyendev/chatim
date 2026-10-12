package view

import "github.com/ivannguyendev/chatim/apps/core/internal/model/domain"

type sendKey struct {
	from, cid string
}

func CollapseRetried(_ Viewer, msgs []domain.Message) []domain.Message {
	first := make(map[sendKey]uint64, len(msgs))
	for _, m := range msgs {
		if m.CID == "" {
			continue
		}
		k := sendKey{m.From, m.CID}
		if seq, ok := first[k]; !ok || m.Seq < seq {
			first[k] = m.Seq
		}
	}
	out := make([]domain.Message, 0, len(msgs))
	for _, m := range msgs {
		if m.CID != "" && first[sendKey{m.From, m.CID}] != m.Seq {
			continue
		}
		out = append(out, m)
	}
	return out
}
