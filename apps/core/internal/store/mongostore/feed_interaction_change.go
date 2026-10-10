package mongostore

import (
	"errors"
	"fmt"

	"go.mongodb.org/mongo-driver/v2/bson"

	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/pkg/keys"
)

var errSkipChange = errors.New("mongostore: change does not enter the feed")

func decodeInteractionChange(ev changeDoc) (store.Change, error) {
	if ev.OperationType == "update" {
		r, err := reactionFromUpdate(ev)
		if err != nil {
			return store.Change{}, err
		}
		return store.Change{Kind: store.ReactionChanged, Reaction: r, CommittedAt: ev.WallTime}, nil
	}
	var d interactionDoc
	if err := bson.Unmarshal(ev.FullDocument, &d); err != nil {
		return store.Change{}, fmt.Errorf("%w: interaction document: %w", errCorrupt, err)
	}
	if err := skipUnlessReaction(d.ID); err != nil {
		return store.Change{}, err
	}
	r, err := decodeReaction(d)
	if err != nil {
		return store.Change{}, err
	}
	return store.Change{Kind: store.ReactionChanged, Reaction: r, CommittedAt: ev.WallTime}, nil
}

func skipUnlessReaction(id []byte) error {
	_, kind, _, err := keys.ParseInteraction(id)
	switch {
	case err != nil:
		return fmt.Errorf("%w: interaction _id: %w", errCorrupt, err)
	case kind != keys.ReactionKind:
		return errSkipChange
	}
	return nil
}

func reactionFromUpdate(ev changeDoc) (domain.Reaction, error) {
	_, id, ok := ev.DocumentKey.ID.BinaryOK()
	if !ok {
		return domain.Reaction{}, fmt.Errorf("%w: interaction update without a binary _id", errCorrupt)
	}
	if err := skipUnlessReaction(id); err != nil {
		return domain.Reaction{}, err
	}
	head, err := decodeHead(interactionDoc{ID: id, State: interactionLive}, keys.ReactionKind)
	if err != nil {
		return domain.Reaction{}, fmt.Errorf("reaction update: %w", err)
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
	return domain.Reaction{Room: head.key.Room, Thread: head.key.Thread, Seq: head.key.Seq, User: head.user, N: n}, nil
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
