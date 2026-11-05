package mongostore

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	"github.com/ivannguyendev/chatim/apps/identity/internal/store"
	"github.com/ivannguyendev/chatim/pkg/core/apperr"
)

const (
	slotLease          = 30 * time.Second
	slotReleaseTimeout = 2 * time.Second
)

type leaseSlot struct {
	s    *Store
	id   string
	once sync.Once
}

func newHolder() string {
	host, _ := os.Hostname()
	suffix := make([]byte, 8)
	_, _ = rand.Read(suffix)
	return host + "-" + hex.EncodeToString(suffix)
}

func (s *Store) TryLoadSlot(ctx context.Context, purpose string, slots int) (store.Slot, bool, error) {
	if purpose == "" || strings.Contains(purpose, keySeparator) {
		return nil, false, fmt.Errorf("%w: slot purpose must be non-empty without %q", apperr.ErrInvalidArgument, keySeparator)
	}
	if slots <= 0 {
		return nil, false, fmt.Errorf("%w: slots must be positive", apperr.ErrInvalidArgument)
	}
	for k := range slots {
		id := purpose + keySeparator + strconv.Itoa(k)
		got, err := s.claimSlot(ctx, purpose, id)
		if err != nil {
			return nil, false, err
		}
		if got {
			return &leaseSlot{s: s, id: id}, true, nil
		}
	}
	return nil, false, nil
}

func (s *Store) claimSlot(ctx context.Context, purpose, id string) (bool, error) {
	free := bson.D{{Key: "$lt", Value: bson.A{"$expires_at", "$$NOW"}}}
	set := bson.D{
		{Key: "purpose", Value: when(free, literal(purpose), "$purpose")},
		{Key: "holder", Value: when(free, literal(s.holder), "$holder")},
		{Key: "expires_at", Value: when(free, leaseEnd(), "$expires_at")},
	}
	opts := options.FindOneAndUpdate().SetUpsert(true).SetReturnDocument(options.After).
		SetProjection(bson.D{{Key: "holder", Value: 1}})
	var doc struct {
		Holder string `bson:"holder"`
	}
	err := s.db.Collection(slotsCollection).FindOneAndUpdate(ctx, bson.D{{Key: "_id", Value: id}},
		mongo.Pipeline{{{Key: "$set", Value: set}}}, opts).Decode(&doc)
	switch {
	case err == nil:
		return doc.Holder == s.holder, nil
	case mongo.IsDuplicateKeyError(err):
		return false, nil
	default:
		return false, s.mapErr(err)
	}
}

func (l *leaseSlot) Renew(ctx context.Context) (bool, error) {
	res, err := l.s.db.Collection(slotsCollection).UpdateOne(ctx, l.ownFilter(),
		mongo.Pipeline{{{Key: "$set", Value: bson.D{{Key: "expires_at", Value: leaseEnd()}}}}})
	if err != nil {
		return false, l.s.mapErr(err)
	}
	return res.MatchedCount > 0, nil
}

func (l *leaseSlot) Release() {
	l.once.Do(func() {
		ctx, cancel := context.WithTimeout(context.Background(), slotReleaseTimeout)
		defer cancel()
		_, _ = l.s.db.Collection(slotsCollection).DeleteOne(ctx, l.ownFilter())
	})
}

func (l *leaseSlot) ownFilter() bson.D {
	return bson.D{{Key: "_id", Value: l.id}, {Key: "holder", Value: l.s.holder}}
}

func leaseEnd() bson.D {
	return bson.D{{Key: "$add", Value: bson.A{"$$NOW", slotLease.Milliseconds()}}}
}
