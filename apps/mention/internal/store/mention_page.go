package store

import (
	"slices"
	"time"

	"github.com/ivannguyendev/chatim/apps/mention/internal/domain"
)

type MentionSource struct {
	Items []domain.Mention
	End   MentionCursor
}

type MentionPage struct {
	Items []domain.Mention
	Next  MentionCursor
}

type FetchMentions func(before MentionCursor) ([]domain.Mention, error)

type KeepMentions func(raw []domain.Mention) ([]domain.Mention, error)

func ScanMentions(limit int, before MentionCursor, fetch FetchMentions, keep KeepMentions) (MentionSource, error) {
	var kept []domain.Mention
	for round := 1; ; round++ {
		raw, err := fetch(before)
		if err != nil {
			return MentionSource{}, err
		}
		got, err := keep(raw)
		if err != nil {
			return MentionSource{}, err
		}
		kept = MergeMentions(limit, kept, got)
		if len(raw) < limit || len(kept) >= limit {
			return MentionSource{Items: kept}, nil
		}
		before = CursorOf(raw[len(raw)-1])
		if round == MentionRounds {
			return MentionSource{Items: kept, End: before}, nil
		}
	}
}

func JoinMentionSources(limit int, sources ...MentionSource) MentionSource {
	var out MentionSource
	lists := make([][]domain.Mention, len(sources))
	for i, src := range sources {
		lists[i] = src.Items
		if !src.End.At.IsZero() && (out.End.At.IsZero() || newerCursor(src.End, out.End)) {
			out.End = src.End
		}
	}
	out.Items = MergeMentions(limit, lists...)
	return out
}

func PageMentions(limit int, sources ...MentionSource) MentionPage {
	src := JoinMentionSources(limit, sources...)
	items := src.Items
	if !src.End.At.IsZero() {
		items = slices.DeleteFunc(items, src.End.Holds)
	}
	switch {
	case len(items) == limit:
		return MentionPage{Items: items, Next: CursorOf(items[len(items)-1])}
	case !src.End.At.IsZero():
		return MentionPage{Items: items, Next: src.End}
	default:
		return MentionPage{Items: items}
	}
}

func MentionsSettled(limit int, src MentionSource, next time.Time) bool {
	if !src.End.At.IsZero() && next.Before(src.End.At) {
		return true
	}
	page := PageMentions(limit, src)
	return len(page.Items) == limit && page.Items[limit-1].CreatedAt.After(next)
}

func newerCursor(a, b MentionCursor) bool {
	return CompareMentions(domain.Mention{Key: a.Key, CreatedAt: a.At}, domain.Mention{Key: b.Key, CreatedAt: b.At}) > 0
}
