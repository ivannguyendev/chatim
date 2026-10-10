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
		return interactionFromUpdate(ev)
	}
	var d interactionDoc
	if err := bson.Unmarshal(ev.FullDocument, &d); err != nil {
		return store.Change{}, fmt.Errorf("%w: interaction document: %w", errCorrupt, err)
	}
	kind, err := interactionChangeKind(d.ID)
	if err != nil {
		return store.Change{}, err
	}
	c := store.Change{Kind: kind, CommittedAt: ev.WallTime}
	if kind == store.BookmarkChanged {
		c.Bookmark, err = decodeBookmark(d)
	} else {
		c.Reaction, err = decodeReaction(d)
	}
	if err != nil {
		return store.Change{}, err
	}
	return c, nil
}

func interactionChangeKind(id []byte) (store.ChangeKind, error) {
	_, kind, _, err := keys.ParseInteraction(id)
	switch {
	case err != nil:
		return 0, fmt.Errorf("%w: interaction _id: %w", errCorrupt, err)
	case kind == keys.ReactionKind:
		return store.ReactionChanged, nil
	case kind == keys.BookmarkKind:
		return store.BookmarkChanged, nil
	default:
		return 0, errSkipChange
	}
}

func interactionFromUpdate(ev changeDoc) (store.Change, error) {
	_, id, ok := ev.DocumentKey.ID.BinaryOK()
	if !ok {
		return store.Change{}, fmt.Errorf("%w: interaction update without a binary _id", errCorrupt)
	}
	kind, err := interactionChangeKind(id)
	if err != nil {
		return store.Change{}, err
	}
	head, err := decodeHead(interactionDoc{ID: id, State: interactionLive}, 0)
	if err != nil {
		return store.Change{}, fmt.Errorf("interaction update: %w", err)
	}
	ver, err := updatedVer(ev)
	if err != nil {
		return store.Change{}, err
	}
	k, c := head.key, store.Change{Kind: kind, CommittedAt: ev.WallTime}
	if kind == store.BookmarkChanged {
		c.Bookmark = domain.Bookmark{Room: k.Room, Thread: k.Thread, Seq: k.Seq, User: head.user, Ver: ver}
	} else {
		c.Reaction = domain.Reaction{Room: k.Room, Thread: k.Thread, Seq: k.Seq, User: head.user, N: ver}
	}
	return c, nil
}

func updatedVer(ev changeDoc) (uint32, error) {
	raw, ok := ev.UpdateDescription.UpdatedFields.Lookup("ver").AsInt64OK()
	if !ok {
		return 0, fmt.Errorf("%w: interaction update without a numeric ver", errCorrupt)
	}
	n, err := narrowUint32("interaction change", raw)
	if err != nil {
		return 0, err
	}
	if n == 0 {
		return 0, fmt.Errorf("%w: interaction update with change 0", errCorrupt)
	}
	return n, nil
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
