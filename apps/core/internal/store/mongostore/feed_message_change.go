package mongostore

import (
	"fmt"

	"go.mongodb.org/mongo-driver/v2/bson"

	"github.com/ivannguyendev/chatim/apps/core/internal/store"
)

func decodeMessageChange(ev changeDoc) (store.Change, error) {
	var d messageDoc
	if err := bson.Unmarshal(ev.FullDocument, &d); err != nil {
		return store.Change{}, fmt.Errorf("%w: message document: %w", errCorrupt, err)
	}
	m, err := decodeMessage(d)
	if err != nil {
		return store.Change{}, err
	}
	return store.Change{Kind: store.MessageInserted, Msg: m, ReplyMentionFlags: store.ReplyMentionFlagsOf(m), CommittedAt: ev.WallTime}, nil
}
