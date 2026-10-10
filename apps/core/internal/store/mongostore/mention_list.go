package mongostore

import (
	"context"
	"fmt"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
)

func (m *Mentions) List(ctx context.Context, q store.MentionQuery) ([]domain.Mention, error) {
	if err := store.ValidateMentionQuery(q); err != nil {
		return nil, err
	}
	opts := options.Find().SetSort(mentionListSort()).SetLimit(int64(q.Limit))
	cur, err := m.coll.Find(ctx, mentionListFilter(q), opts)
	if err != nil {
		return nil, fmt.Errorf("list mentions: %w", err)
	}
	var docs []mentionLinkDoc
	if err := cur.All(ctx, &docs); err != nil {
		return nil, fmt.Errorf("list mentions: %w", err)
	}
	out := make([]domain.Mention, 0, len(docs))
	for _, d := range docs {
		got, err := decodeMention(d)
		if err != nil {
			return nil, err
		}
		out = append(out, got)
	}
	return out, nil
}

func mentionListSort() bson.D {
	return bson.D{{Key: "created_at", Value: -1}, {Key: "message_key", Value: -1}}
}

func mentionListFilter(q store.MentionQuery) bson.D {
	branches := mentionBranches(q)
	if len(branches) == 1 {
		return branches[0]
	}
	return bson.D{{Key: "$or", Value: branches}}
}

func mentionBranches(q store.MentionQuery) []bson.D {
	var plain bson.A
	var branches []bson.D
	for _, w := range q.Targets {
		if w.Since.IsZero() {
			plain = append(plain, w.Target)
			continue
		}
		branches = append(branches, mentionBranch(q, w.Target, w.Since))
	}
	if len(plain) > 0 {
		branches = append([]bson.D{mentionBranch(q, bson.D{{Key: "$in", Value: plain}}, time.Time{})}, branches...)
	}
	return branches
}

func mentionBranch(q store.MentionQuery, target any, since time.Time) bson.D {
	branch := bson.D{{Key: "tenant", Value: q.Tenant}, {Key: "target", Value: target}, {Key: "state", Value: mentionLive}}
	var span bson.D
	if !since.IsZero() {
		span = append(span, bson.E{Key: "$gte", Value: since})
	}
	if !q.Before.At.IsZero() {
		span = append(span, bson.E{Key: "$lte", Value: q.Before.At})
	}
	if len(span) > 0 {
		branch = append(branch, bson.E{Key: "created_at", Value: span})
	}
	if at := q.Before.At; !at.IsZero() {
		tied := bson.D{{Key: "created_at", Value: at}, {Key: "message_key", Value: bson.D{{Key: "$gte", Value: msgID(q.Before.Key)}}}}
		branch = append(branch, bson.E{Key: "$nor", Value: bson.A{tied}})
	}
	return branch
}
