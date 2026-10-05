package mongostore

import (
	"fmt"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"

	"github.com/ivannguyendev/chatim/apps/core/internal/store"
)

type changeDoc struct {
	Token        bson.Raw       `bson:"_id"`
	ClusterTime  bson.Timestamp `bson:"clusterTime"`
	WallTime     time.Time      `bson:"wallTime"`
	NS           changeNS       `bson:"ns"`
	FullDocument bson.Raw       `bson:"fullDocument"`
}

type changeNS struct {
	Coll string `bson:"coll"`
}

func decodeChange(ev changeDoc) (store.Change, error) {
	switch ev.NS.Coll {
	case messagesCollection:
		var d messageDoc
		if err := bson.Unmarshal(ev.FullDocument, &d); err != nil {
			return store.Change{}, fmt.Errorf("%w: message document: %w", errCorrupt, err)
		}
		m, err := decodeMessage(d)
		if err != nil {
			return store.Change{}, err
		}
		return store.Change{Kind: store.MessageInserted, Msg: m, CommittedAt: ev.WallTime}, nil
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
	default:
		return store.Change{}, fmt.Errorf("%w: change on collection %q", errCorrupt, ev.NS.Coll)
	}
}
