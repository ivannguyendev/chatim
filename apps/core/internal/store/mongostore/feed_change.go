package mongostore

import (
	"fmt"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"

	"github.com/ivannguyendev/chatim/apps/core/internal/store"
)

type changeDoc struct {
	Token             bson.Raw       `bson:"_id"`
	OperationType     string         `bson:"operationType"`
	ClusterTime       bson.Timestamp `bson:"clusterTime"`
	WallTime          time.Time      `bson:"wallTime"`
	NS                changeNS       `bson:"ns"`
	DocumentKey       changeKey      `bson:"documentKey"`
	FullDocument      bson.Raw       `bson:"fullDocument"`
	UpdateDescription changeUpdate   `bson:"updateDescription"`
}

type changeNS struct {
	Coll string `bson:"coll"`
}

type changeKey struct {
	ID bson.RawValue `bson:"_id"`
}

type changeUpdate struct {
	UpdatedFields bson.Raw `bson:"updatedFields"`
}

func decodeChange(ev changeDoc) (store.Change, error) {
	switch ev.NS.Coll {
	case messagesCollection:
		return decodeMessageChange(ev)
	case roomsCollection:
		var d roomDoc
		if err := bson.Unmarshal(ev.FullDocument, &d); err != nil {
			return store.Change{}, fmt.Errorf("%w: room document: %w", errCorrupt, err)
		}
		r, err := decodeRoom(d)
		if err != nil {
			return store.Change{}, err
		}
		return store.Change{Kind: store.RoomInserted, Room: r, CommittedAt: ev.WallTime}, nil
	case editsCollection:
		var d editDoc
		if err := bson.Unmarshal(ev.FullDocument, &d); err != nil {
			return store.Change{}, fmt.Errorf("%w: edit document: %w", errCorrupt, err)
		}
		e, err := decodeEdit(d)
		if err != nil {
			return store.Change{}, err
		}
		return store.Change{Kind: store.EditInserted, Edit: e, CommittedAt: ev.WallTime}, nil
	case interactionsCollection:
		return decodeInteractionChange(ev)
	case pinActionsCollection:
		return decodePinChange(ev)
	case membersCollection:
		return decodeMemberChange(ev)
	case hiddenCollection:
		return decodeHiddenChange(ev)
	default:
		return store.Change{}, fmt.Errorf("%w: change on collection %q", errCorrupt, ev.NS.Coll)
	}
}
