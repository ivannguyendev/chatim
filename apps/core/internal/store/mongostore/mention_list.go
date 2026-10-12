package mongostore

import (
	"context"
	"fmt"
	"slices"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
)

func (m *Mentions) ListByTarget(ctx context.Context, q store.TargetMentionQuery) ([]domain.Mention, error) {
	if err := store.ValidateTargetMentionQuery(q); err != nil {
		return nil, err
	}
	return m.list(ctx, targetMentionFilter(q), q.Limit)
}

func (m *Mentions) ListShared(ctx context.Context, q store.SharedMentionQuery) ([]domain.Mention, error) {
	if err := store.ValidateSharedMentionQuery(q); err != nil {
		return nil, err
	}
	filter, err := sharedMentionFilter(q)
	if err != nil {
		return nil, err
	}
	return m.list(ctx, filter, q.Limit)
}

func (m *Mentions) list(ctx context.Context, filter bson.D, limit int) ([]domain.Mention, error) {
	opts := options.Find().SetSort(mentionListSort()).SetLimit(int64(limit))
	cur, err := m.coll.Find(ctx, filter, opts)
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

func targetMentionFilter(q store.TargetMentionQuery) bson.D {
	states := bson.A{mentionLive, mentionRead}
	if q.UnreadOnly {
		states = bson.A{mentionLive}
	}
	filter := bson.D{{Key: "tenant", Value: q.Tenant}, {Key: "target", Value: q.Target}, {Key: "state", Value: bson.D{{Key: "$in", Value: states}}}}
	return withMentionSpan(filter, nil, q.Before)
}

func sharedMentionFilter(q store.SharedMentionQuery) (bson.D, error) {
	groups := make(bson.A, len(q.Groups))
	for i, g := range q.Groups {
		groups[i] = domain.MentionTarget{Kind: domain.MentionGroup, ID: g}.Name(0)
	}
	branches := make(bson.A, 0, 2*len(q.Rooms))
	for _, r := range q.Rooms {
		room, err := toInt64("room id", r.Room)
		if err != nil {
			return nil, err
		}
		all := sharedBranch(room, q.Tenant, domain.MentionAll)
		branches = append(branches, withMentionSpan(all, after(r.Since), q.Before))
		if len(groups) == 0 {
			continue
		}
		group := append(sharedBranch(room, q.Tenant, domain.MentionGroup), bson.E{Key: "target", Value: bson.D{{Key: "$in", Value: groups}}})
		lower := after(r.Since)
		if q.GroupSince.After(r.Since) {
			lower = bson.D{{Key: "$gte", Value: q.GroupSince}}
		}
		branches = append(branches, withMentionSpan(group, lower, q.Before))
	}
	return bson.D{{Key: "$or", Value: branches}}, nil
}

func sharedBranch(room int64, tenant string, kind domain.MentionKind) bson.D {
	return bson.D{{Key: "room_id", Value: room}, {Key: "kind", Value: mentionKindNames[kind]}, {Key: "tenant", Value: tenant}, {Key: "state", Value: mentionLive}}
}

func after(since time.Time) bson.D {
	if since.IsZero() {
		return nil
	}
	return bson.D{{Key: "$gt", Value: since}}
}

func withMentionSpan(filter, lower bson.D, before store.MentionCursor) bson.D {
	span := slices.Clone(lower)
	if !before.At.IsZero() {
		span = append(span, bson.E{Key: "$lte", Value: before.At})
	}
	if len(span) > 0 {
		filter = append(filter, bson.E{Key: "created_at", Value: span})
	}
	if !before.At.IsZero() {
		tied := bson.D{{Key: "created_at", Value: before.At}, {Key: "message_key", Value: bson.D{{Key: "$gte", Value: msgID(before.Key)}}}}
		filter = append(filter, bson.E{Key: "$nor", Value: bson.A{tied}})
	}
	return filter
}
