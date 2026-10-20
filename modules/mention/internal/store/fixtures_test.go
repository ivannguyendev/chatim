package store_test

import (
	"time"

	"github.com/ivannguyendev/chatim/modules/mention/internal/domain"
)

var (
	minh  = domain.Target{Kind: domain.TargetUser, ID: "minh"}
	ops   = domain.Target{Kind: domain.TargetGroup, ID: "ops"}
	every = domain.Target{Kind: domain.TargetAll}
)

var mergeBase = time.UnixMilli(1_700_000_000_000).UTC()

func at(sec int) time.Time { return mergeBase.Add(time.Duration(sec) * time.Second) }

func hit(room, seq uint64, sec int, target domain.Target) domain.Mention {
	return domain.Mention{Key: domain.MessageKey{Room: room, Seq: seq}, Tenant: "acme", Target: target, State: domain.StateLive, CreatedAt: at(sec)}
}

func seqsOf(ms []domain.Mention) []uint64 {
	out := make([]uint64, len(ms))
	for i, m := range ms {
		out[i] = m.Key.Seq
	}
	return out
}
