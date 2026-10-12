package mongostore

import (
	"fmt"
	"math"

	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
)

type reactionsDoc struct {
	Counts  []countDoc `bson:"c"`
	Version int64      `bson:"v"`
}

type countDoc struct {
	Emoji string `bson:"e"`
	N     int64  `bson:"n"`
}

func encodeSummary(s domain.ReactionSummary) (reactionsDoc, error) {
	v, err := toInt64("reactions version", s.Version)
	if err != nil {
		return reactionsDoc{}, err
	}
	counts := make([]countDoc, len(s.Counts))
	for i, c := range s.Counts {
		counts[i] = countDoc{Emoji: c.Emoji, N: int64(c.Count)}
	}
	return reactionsDoc{Counts: counts, Version: v}, nil
}

func decodeSummary(d *reactionsDoc) (domain.ReactionSummary, error) {
	if d == nil {
		return domain.ReactionSummary{}, nil
	}
	v, err := toUint64("reactions version", d.Version)
	if err != nil {
		return domain.ReactionSummary{}, err
	}
	raw := make([]domain.RawCount, len(d.Counts))
	for i, c := range d.Counts {
		if err := withinUint32("reaction count", c.N); err != nil {
			return domain.ReactionSummary{}, err
		}
		raw[i] = domain.RawCount{Emoji: c.Emoji, N: c.N}
	}
	return domain.SettleReactions(raw, v), nil
}

func withinUint32(field string, v int64) error {
	if v > math.MaxUint32 {
		return fmt.Errorf("%w: %s %d out of range", errCorrupt, field, v)
	}
	return nil
}

func narrowUint32(field string, v int64) (uint32, error) {
	if v < 0 || v > math.MaxUint32 {
		return 0, fmt.Errorf("%w: %s %d out of range", errCorrupt, field, v)
	}
	return uint32(v), nil
}
