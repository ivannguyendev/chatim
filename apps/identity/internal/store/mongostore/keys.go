package mongostore

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	"github.com/ivannguyendev/chatim/apps/identity/internal/store"
	"github.com/ivannguyendev/chatim/pkg/core/apperr"
)

const activeClaimID = "active"

type signingKeyDoc struct {
	ID            string         `bson:"_id"`
	State         store.KeyState `bson:"state"`
	PublicKey     []byte         `bson:"public_key"`
	PrivateSealed []byte         `bson:"private_key_sealed"`
	CreatedAt     time.Time      `bson:"created_at"`
	ActivatedAt   time.Time      `bson:"activated_at"`
	RetiredAt     time.Time      `bson:"retired_at"`
}

func (s *Store) CreateSigningKey(ctx context.Context, k store.SigningKey) (time.Time, error) {
	filter := bson.D{{Key: "_id", Value: k.ID}, {Key: "state", Value: bson.D{{Key: "$exists", Value: false}}}}
	update := mongo.Pipeline{{{Key: "$set", Value: bson.D{
		{Key: "state", Value: literal(store.KeyPending)},
		{Key: "public_key", Value: literal(k.PublicKey)},
		{Key: "private_key_sealed", Value: literal(k.PrivateSealed)},
		{Key: "created_at", Value: "$$NOW"},
	}}}}
	opts := options.FindOneAndUpdate().SetUpsert(true).SetReturnDocument(options.After).
		SetProjection(bson.D{{Key: "created_at", Value: 1}})
	var doc signingKeyDoc
	if err := s.db.Collection(signingKeysCollection).FindOneAndUpdate(ctx, filter, update, opts).Decode(&doc); err != nil {
		return time.Time{}, s.mapErr(err)
	}
	return doc.CreatedAt, nil
}

func (s *Store) ActivateSigningKey(ctx context.Context, id string, lead time.Duration) error {
	return s.runOnce(ctx, func(ctx context.Context) error { return s.activateKey(ctx, id, lead) })
}

func (s *Store) ActivateDueSigningKey(ctx context.Context, lead time.Duration) (string, error) {
	var kid string
	err := s.runOnce(ctx, func(ctx context.Context) error {
		due, err := s.dueKey(ctx, lead)
		if err != nil || due == "" {
			return err
		}
		if err := s.activateKey(ctx, due, lead); err != nil {
			return err
		}
		kid = due
		return nil
	})
	if err != nil {
		return "", err
	}
	return kid, nil
}

func (s *Store) SigningKeys(ctx context.Context) ([]store.SigningKey, error) {
	var docs []signingKeyDoc
	if err := s.findAll(ctx, signingKeysCollection, bson.D{}, &docs); err != nil {
		return nil, err
	}
	keys := make([]store.SigningKey, 0, len(docs))
	for _, d := range docs {
		keys = append(keys, store.SigningKey{
			ID: d.ID, State: d.State, PublicKey: d.PublicKey, PrivateSealed: d.PrivateSealed,
			CreatedAt: d.CreatedAt, ActivatedAt: d.ActivatedAt, RetiredAt: d.RetiredAt,
		})
	}
	slices.SortFunc(keys, func(a, b store.SigningKey) int {
		return cmp.Or(a.CreatedAt.Compare(b.CreatedAt), cmp.Compare(a.ID, b.ID))
	})
	return keys, nil
}

func (s *Store) activateKey(ctx context.Context, id string, lead time.Duration) error {
	keys := s.db.Collection(signingKeysCollection)
	var key signingKeyDoc
	if err := keys.FindOne(ctx, bson.D{{Key: "_id", Value: id}}).Decode(&key); err != nil {
		return err
	}
	switch key.State {
	case store.KeyActive:
		return nil
	case store.KeyRetired:
		return fmt.Errorf("%w: signing key %s is retired", apperr.ErrFailedPrecondition, id)
	case store.KeyPending:
	}
	_, err := s.db.Collection(signingKeyClaimsCollection).UpdateOne(ctx,
		bson.D{{Key: "_id", Value: activeClaimID}},
		bson.D{{Key: "$set", Value: bson.D{{Key: "key_id", Value: id}}}, {Key: "$inc", Value: bson.D{{Key: "ver", Value: int64(1)}}}},
		options.UpdateOne().SetUpsert(true))
	if err != nil {
		return err
	}
	res, err := keys.UpdateOne(ctx,
		bson.D{{Key: "_id", Value: id}, {Key: "state", Value: store.KeyPending}, {Key: "$expr", Value: readyBy(lead)}},
		mongo.Pipeline{{{Key: "$set", Value: bson.D{
			{Key: "state", Value: literal(store.KeyActive)},
			{Key: "activated_at", Value: "$$NOW"},
			{Key: "retired_at", Value: "$$REMOVE"},
		}}}})
	if err != nil {
		return err
	}
	if res.MatchedCount == 0 {
		return fmt.Errorf("%w: signing key %s can be activated from %s, once every node has fetched it",
			apperr.ErrFailedPrecondition, id, key.CreatedAt.Add(lead).UTC().Format(time.RFC3339))
	}
	_, err = keys.UpdateMany(ctx,
		bson.D{{Key: "$or", Value: bson.A{
			bson.D{{Key: "state", Value: store.KeyActive}, {Key: "_id", Value: bson.D{{Key: "$ne", Value: id}}}},
			bson.D{{Key: "state", Value: store.KeyPending}, {Key: "created_at", Value: bson.D{{Key: "$lt", Value: key.CreatedAt}}}},
		}}},
		mongo.Pipeline{{{Key: "$set", Value: bson.D{
			{Key: "state", Value: literal(store.KeyRetired)},
			{Key: "retired_at", Value: "$$NOW"},
		}}}})
	return err
}

func (s *Store) dueKey(ctx context.Context, lead time.Duration) (string, error) {
	filter := bson.D{{Key: "state", Value: store.KeyPending}, {Key: "$expr", Value: readyBy(lead)}}
	activeCreated, found, err := s.activeKeyCreatedAt(ctx)
	if err != nil {
		return "", err
	}
	if found {
		filter = append(filter, bson.E{Key: "created_at", Value: bson.D{{Key: "$gt", Value: activeCreated}}})
	}
	var docs []signingKeyDoc
	cur, err := s.db.Collection(signingKeysCollection).Find(ctx, filter, options.Find().SetProjection(bson.D{{Key: "created_at", Value: 1}}))
	if err != nil {
		return "", err
	}
	if err := cur.All(ctx, &docs); err != nil {
		return "", err
	}
	if len(docs) == 0 {
		return "", nil
	}
	newest := slices.MaxFunc(docs, func(a, b signingKeyDoc) int {
		return cmp.Or(a.CreatedAt.Compare(b.CreatedAt), cmp.Compare(a.ID, b.ID))
	})
	return newest.ID, nil
}

func (s *Store) activeKeyCreatedAt(ctx context.Context) (time.Time, bool, error) {
	var claim struct {
		KeyID string `bson:"key_id"`
	}
	err := s.db.Collection(signingKeyClaimsCollection).FindOne(ctx, bson.D{{Key: "_id", Value: activeClaimID}}).Decode(&claim)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return time.Time{}, false, nil
	}
	if err != nil {
		return time.Time{}, false, err
	}
	var active signingKeyDoc
	err = s.db.Collection(signingKeysCollection).FindOne(ctx, bson.D{{Key: "_id", Value: claim.KeyID}},
		options.FindOne().SetProjection(bson.D{{Key: "created_at", Value: 1}})).Decode(&active)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return time.Time{}, false, nil
	}
	if err != nil {
		return time.Time{}, false, err
	}
	return active.CreatedAt, true, nil
}

func readyBy(lead time.Duration) bson.D {
	return bson.D{{Key: "$lte", Value: bson.A{
		bson.D{{Key: "$add", Value: bson.A{"$created_at", lead.Milliseconds()}}},
		"$$NOW",
	}}}
}
