package mongostore

import (
	"context"
	"errors"
	"fmt"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/pkg/keys"
)

func (r *Interactions) SetBookmark(ctx context.Context, b domain.Bookmark) (domain.Bookmark, bool, error) {
	if err := store.ValidateBookmark(b); err != nil {
		return domain.Bookmark{}, false, err
	}
	key := store.BookmarkKeyOf(b)
	if !b.On {
		return r.removeBookmark(ctx, key, b.User, b.At)
	}
	room, err := toInt64("room id", b.Room)
	if err != nil {
		return domain.Bookmark{}, false, err
	}
	head := interactionFields(key, room, b.Tenant, keys.BookmarkKind, b.User)
	opts := options.FindOneAndUpdate().SetUpsert(true).SetReturnDocument(options.Before)
	var d interactionDoc
	err = r.coll.FindOneAndUpdate(ctx, idIs(userID(key, keys.BookmarkKind, b.User)), setBookmark(head, b.At), opts).Decode(&d)
	switch {
	case errors.Is(err, mongo.ErrNoDocuments):
		return bookmarkResult(b, 0), true, nil
	case err != nil:
		return domain.Bookmark{}, false, fmt.Errorf("bookmark %d/%d/%d for %q: %w", b.Room, b.Thread, b.Seq, b.User, err)
	}
	before, err := decodeBookmark(d)
	switch {
	case err != nil:
		return domain.Bookmark{}, false, err
	case before.On:
		return before, false, nil
	}
	return bookmarkResult(b, before.Ver), true, nil
}

func bookmarkResult(b domain.Bookmark, ver uint32) domain.Bookmark {
	b.Ver, b.At = ver+1, time.UnixMilli(b.At.UnixMilli()).UTC()
	return b
}

func (r *Interactions) removeBookmark(ctx context.Context, key store.MsgKey, user string, at time.Time) (domain.Bookmark, bool, error) {
	filter := bson.D{{Key: "_id", Value: userID(key, keys.BookmarkKind, user)}, {Key: "state", Value: interactionLive}}
	var d interactionDoc
	err := r.coll.FindOneAndUpdate(ctx, filter, removeInteraction(at, false), options.FindOneAndUpdate().SetReturnDocument(options.After)).Decode(&d)
	switch {
	case errors.Is(err, mongo.ErrNoDocuments):
		cur, _, err := r.GetBookmark(ctx, key, user)
		return cur, false, err
	case err != nil:
		return domain.Bookmark{}, false, fmt.Errorf("remove bookmark %d/%d/%d for %q: %w", key.Room, key.Thread, key.Seq, user, err)
	}
	got, err := decodeBookmark(d)
	return got, err == nil, err
}

func (r *Interactions) GetBookmark(ctx context.Context, key store.MsgKey, user string) (domain.Bookmark, bool, error) {
	d, found, err := r.get(ctx, userID(key, keys.BookmarkKind, user))
	if err != nil || !found {
		return domain.Bookmark{}, false, err
	}
	got, err := decodeBookmark(d)
	return got, err == nil, err
}

func (r *Interactions) Bookmarks(ctx context.Context, tenant, user string, before store.BookmarkCursor, limit int) ([]domain.Bookmark, error) {
	if err := store.ValidateBookmarkQuery(tenant, user, limit); err != nil {
		return nil, err
	}
	filter, sort := bookmarkQuery(tenant, user, before)
	docs, err := r.find(ctx, filter, options.Find().SetSort(sort).SetLimit(int64(limit)))
	if err != nil {
		return nil, fmt.Errorf("bookmarks of %q: %w", user, err)
	}
	return decodeAll(docs, decodeBookmark)
}

func bookmarkQuery(tenant, user string, before store.BookmarkCursor) (filter, sort bson.D) {
	filter = bson.D{
		{Key: "tenant", Value: tenant}, {Key: "actor_id", Value: user},
		{Key: "kind", Value: interactionKindNames[keys.BookmarkKind]}, {Key: "state", Value: interactionLive},
	}
	if !before.At.IsZero() {
		older := bson.D{{Key: "updated_at", Value: bson.D{{Key: "$lt", Value: before.At}}}}
		tied := bson.D{{Key: "updated_at", Value: before.At}, {Key: "message_key", Value: bson.D{{Key: "$lt", Value: msgID(before.Key)}}}}
		filter = append(filter,
			bson.E{Key: "updated_at", Value: bson.D{{Key: "$lte", Value: before.At}}},
			bson.E{Key: "$or", Value: bson.A{older, tied}},
		)
	}
	return filter, bson.D{{Key: "updated_at", Value: -1}, {Key: "message_key", Value: -1}}
}
