package mongostore

import (
	"cmp"

	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
	"go.mongodb.org/mongo-driver/v2/mongo/readconcern"
	"go.mongodb.org/mongo-driver/v2/mongo/readpref"
	"go.mongodb.org/mongo-driver/v2/mongo/writeconcern"

	"github.com/ivannguyendev/chatim/apps/core/internal/store"
)

const (
	messagesCollection = "messages"
	roomsCollection    = "rooms"
	membersCollection  = "members"
)

var (
	_ store.Messages = (*Store)(nil)
	_ store.Rooms    = (*Store)(nil)
)

type Options struct {
	WriteConcern *writeconcern.WriteConcern
}

type Store struct {
	messages  *mongo.Collection
	committed *mongo.Collection
	rooms     *mongo.Collection
	members   *mongo.Collection
}

func New(db *mongo.Database, opts Options) *Store {
	wc := cmp.Or(opts.WriteConcern, writeconcern.Majority())
	primary := options.Collection().SetWriteConcern(wc).SetReadPreference(readpref.Primary())
	local := options.Collection().SetWriteConcern(wc).SetReadPreference(readpref.Primary()).SetReadConcern(readconcern.Local())
	majority := options.Collection().SetReadPreference(readpref.Primary()).SetReadConcern(readconcern.Majority())
	return &Store{
		messages:  db.Collection(messagesCollection, local),
		committed: db.Collection(messagesCollection, majority),
		rooms:     db.Collection(roomsCollection, primary),
		members:   db.Collection(membersCollection, primary),
	}
}
