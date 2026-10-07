package mongostore

import (
	"fmt"

	"go.mongodb.org/mongo-driver/v2/bson"

	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/pkg/keys"
)

func decodeReactionChange(ev changeDoc) (store.Change, error) {
	if ev.OperationType == "update" {
		r, err := reactionFromUpdate(ev)
		if err != nil {
			return store.Change{}, err
		}
		return store.Change{Kind: store.ReactionChanged, Reaction: r, CommittedAt: ev.WallTime}, nil
	}
	var d reactionDoc
	if err := bson.Unmarshal(ev.FullDocument, &d); err != nil {
		return store.Change{}, fmt.Errorf("%w: reaction document: %w", errCorrupt, err)
	}
	r, err := decodeReaction(d)
	if err != nil {
		return store.Change{}, err
	}
	return store.Change{Kind: store.ReactionChanged, Reaction: r, CommittedAt: ev.WallTime}, nil
}

func reactionFromUpdate(ev changeDoc) (domain.Reaction, error) {
	_, id, ok := ev.DocumentKey.ID.BinaryOK()
	if !ok {
		return domain.Reaction{}, fmt.Errorf("%w: reaction update without a binary _id", errCorrupt)
	}
	room, thread, seq, user, err := keys.ParseReaction(id)
	if err != nil {
		return domain.Reaction{}, fmt.Errorf("%w: reaction update _id: %w", errCorrupt, err)
	}
	if err := domain.ValidUser(user); err != nil {
		return domain.Reaction{}, fmt.Errorf("%w: reaction update user: %w", errCorrupt, err)
	}
	raw, ok := ev.UpdateDescription.UpdatedFields.Lookup("ver").AsInt64OK()
	if !ok {
		return domain.Reaction{}, fmt.Errorf("%w: reaction update without a numeric ver", errCorrupt)
	}
	n, err := narrowUint32("reaction change", raw)
	if err != nil {
		return domain.Reaction{}, err
	}
	if n == 0 {
		return domain.Reaction{}, fmt.Errorf("%w: reaction update with change 0", errCorrupt)
	}
	return domain.Reaction{Room: room, Thread: thread, Seq: seq, User: user, N: n}, nil
}

func decodePinChange(ev changeDoc) (store.Change, error) {
	var d pinActionDoc
	if err := bson.Unmarshal(ev.FullDocument, &d); err != nil {
		return store.Change{}, fmt.Errorf("%w: pin document: %w", errCorrupt, err)
	}
	a, err := decodePinAction(d)
	if err != nil {
		return store.Change{}, err
	}
	return store.Change{Kind: store.PinInserted, Pin: a, CommittedAt: ev.WallTime}, nil
}
