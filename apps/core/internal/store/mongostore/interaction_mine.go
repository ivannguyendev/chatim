package mongostore

import (
	"context"
	"fmt"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/pkg/keys"
)

var myInteractionFields = bson.D{{Key: "_id", Value: 1}, {Key: "value", Value: 1}, {Key: "state", Value: 1}, {Key: "ver", Value: 1}}

func (r *Interactions) MyInteractions(ctx context.Context, user string, ks []store.MsgKey) (map[store.MsgKey]store.MyInteraction, error) {
	if err := store.ValidateMyInteractions(user, ks); err != nil {
		return nil, err
	}
	out := make(map[store.MsgKey]store.MyInteraction)
	if len(ks) == 0 {
		return out, nil
	}
	ids := make(bson.A, 0, 2*len(ks))
	for _, k := range ks {
		ids = append(ids, userID(k, keys.ReactionKind, user), userID(k, keys.BookmarkKind, user))
	}
	filter := bson.D{{Key: "_id", Value: bson.D{{Key: "$in", Value: ids}}}, {Key: "state", Value: interactionLive}}
	docs, err := r.find(ctx, filter, options.Find().SetProjection(myInteractionFields))
	if err != nil {
		return nil, fmt.Errorf("interactions of %q on %d messages: %w", user, len(ks), err)
	}
	for _, d := range docs {
		h, err := decodeHead(d, 0)
		if err != nil {
			return nil, err
		}
		mine := out[h.key]
		if h.kind == keys.BookmarkKind {
			mine.Bookmarked = true
		} else {
			mine.Reaction = d.Value
		}
		out[h.key] = mine
	}
	return out, nil
}
