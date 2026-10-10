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
	messagesCollection        = "messages"
	roomsCollection           = "rooms"
	membersCollection         = "members"
	reconcilerStateCollection = "reconciler_state"
	editsCollection           = "message_edits"
	hiddenCollection          = "hidden"
	interactionsCollection    = "message_interactions"
	pinActionsCollection      = "pin_actions"
)

var (
	_ store.Messages          = (*Store)(nil)
	_ store.Rooms             = (*Store)(nil)
	_ store.MessageEditor     = (*Store)(nil)
	_ store.HistoryClearer    = (*Store)(nil)
	_ store.Edits             = (*Store)(nil)
	_ store.ReactionSummaries = (*Store)(nil)
	_ store.PinProjector      = (*Store)(nil)
	_ store.Interactions      = (*Interactions)(nil)
	_ store.Pins              = (*Pins)(nil)
	_ store.Hidden            = (*Hidden)(nil)
	_ store.MemberWriter      = (*Store)(nil)
	_ store.MemberReader      = (*Store)(nil)
	_ store.OwnerChanges      = (*Store)(nil)
	_ store.MemberCounts      = (*Store)(nil)
	_ store.ReadPositions     = (*Store)(nil)
)

type Options struct {
	WriteConcern *writeconcern.WriteConcern
}

type Store struct {
	client           *mongo.Client
	committedMembers *mongo.Collection
	messages         *mongo.Collection
	committed        *mongo.Collection
	rooms            *mongo.Collection
	members          *mongo.Collection
	edits            *mongo.Collection
	hidden           *Hidden
	interactions     *Interactions
	pins             *Pins
}

func New(db *mongo.Database, opts Options) *Store {
	wc := cmp.Or(opts.WriteConcern, writeconcern.Majority())
	primary := options.Collection().SetWriteConcern(wc).SetReadPreference(readpref.Primary())
	local := options.Collection().SetWriteConcern(wc).SetReadPreference(readpref.Primary()).SetReadConcern(readconcern.Local())
	majority := options.Collection().SetReadPreference(readpref.Primary()).SetReadConcern(readconcern.Majority())
	interacted := options.Collection().SetWriteConcern(wc).SetReadPreference(readpref.Primary()).SetReadConcern(readconcern.Majority())
	return &Store{
		client:           db.Client(),
		committedMembers: db.Collection(membersCollection, majority),
		messages:         db.Collection(messagesCollection, local),
		committed:        db.Collection(messagesCollection, majority),
		rooms:            db.Collection(roomsCollection, primary),
		members:          db.Collection(membersCollection, primary),
		edits:            db.Collection(editsCollection, primary),
		hidden:           &Hidden{coll: db.Collection(hiddenCollection, primary)},
		interactions:     &Interactions{coll: db.Collection(interactionsCollection, interacted), client: db.Client()},
		pins:             &Pins{coll: db.Collection(pinActionsCollection, primary)},
	}
}

func (s *Store) Interactions() *Interactions { return s.interactions }

func (s *Store) Pins() *Pins { return s.pins }

func (s *Store) Hidden() *Hidden { return s.hidden }
