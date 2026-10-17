package mongostore

import (
	"context"
	"errors"
	"fmt"

	"go.mongodb.org/mongo-driver/v2/mongo"
)

const indexNotFoundCode = 27

var replacedIndexes = []struct{ coll, name string }{
	{membersCollection, "tenant_1_user_id_1_state_1_room_id_1_joined_at_1_cleared_at_1_read_seq_1"},
	{interactionsCollection, "message_key_1_kind_1_state_1_value_1"},
}

func dropReplacedIndexes(ctx context.Context, db *mongo.Database) error {
	for _, ix := range replacedIndexes {
		if err := db.Collection(ix.coll).Indexes().DropOne(ctx, ix.name); err != nil && !indexNotFound(err) {
			return fmt.Errorf("bootstrap %s: drop replaced index %s: %w", ix.coll, ix.name, err)
		}
	}
	return nil
}

func indexNotFound(err error) bool {
	se, ok := errors.AsType[mongo.ServerError](err)
	return ok && se.HasErrorCode(indexNotFoundCode)
}
