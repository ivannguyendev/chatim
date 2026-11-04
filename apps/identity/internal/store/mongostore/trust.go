package mongostore

import (
	"cmp"
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	"github.com/ivannguyendev/chatim/apps/identity/internal/store"
	"github.com/ivannguyendev/chatim/pkg/core/apperr"
)

const keyRangeEnd = "┃"

type trustedIssuerDoc struct {
	Issuer   string      `bson:"_id"`
	TenantID string      `bson:"tenant_id"`
	State    store.State `bson:"state"`
}

type tenantStateDoc struct {
	ID    string      `bson:"_id"`
	State store.State `bson:"state"`
}

type trustedKeyDoc struct {
	Issuer    string      `bson:"issuer"`
	KeyID     string      `bson:"key_id"`
	Algorithm string      `bson:"algorithm"`
	PublicKey string      `bson:"public_key"`
	State     store.State `bson:"state"`
	CreatedAt time.Time   `bson:"created_at"`
	CreatedBy string      `bson:"created_by"`
}

func (s *Store) CreateTrustedIssuer(ctx context.Context, issuer, tenantID string, at time.Time, by string) error {
	if err := checkKeyPart("issuer", issuer); err != nil {
		return err
	}
	if err := s.mustExist(ctx, tenantsCollection, tenantID); err != nil {
		return err
	}
	_, err := s.db.Collection(trustedIssuersCollection).InsertOne(ctx, bson.D{
		{Key: "_id", Value: issuer},
		{Key: "tenant_id", Value: tenantID},
		{Key: "state", Value: store.StateActive},
		{Key: "ver", Value: int64(1)},
		{Key: "created_at", Value: at},
		{Key: "updated_at", Value: at},
		{Key: "updated_by", Value: by},
	})
	return s.mapErr(err)
}

func (s *Store) SetTrustedIssuerState(ctx context.Context, issuer string, state store.State, at time.Time, by string) error {
	set := versionedSet(differs("state", state), bson.D{{Key: "state", Value: literal(state)}}, at, by)
	return s.updateExisting(ctx, trustedIssuersCollection, issuer, set)
}

func (s *Store) AddTrustedKey(ctx context.Context, k store.TrustedKey, at time.Time, by string) error {
	if err := checkIssuerKey(k.Issuer, k.KeyID); err != nil {
		return err
	}
	if err := s.mustExist(ctx, trustedIssuersCollection, k.Issuer); err != nil {
		return err
	}
	_, err := s.db.Collection(trustedKeysCollection).InsertOne(ctx, bson.D{
		{Key: "_id", Value: trustedKeyID(k.Issuer, k.KeyID)},
		{Key: "issuer", Value: k.Issuer},
		{Key: "key_id", Value: k.KeyID},
		{Key: "algorithm", Value: k.Algorithm},
		{Key: "public_key", Value: string(k.PublicKey)},
		{Key: "state", Value: store.StateActive},
		{Key: "created_at", Value: at},
		{Key: "created_by", Value: by},
	})
	return s.mapErr(err)
}

func (s *Store) RemoveTrustedKey(ctx context.Context, issuer, keyID string) error {
	if err := checkIssuerKey(issuer, keyID); err != nil {
		return err
	}
	res, err := s.db.Collection(trustedKeysCollection).UpdateOne(ctx,
		bson.D{{Key: "_id", Value: trustedKeyID(issuer, keyID)}, {Key: "state", Value: store.StateActive}},
		bson.D{{Key: "$set", Value: bson.D{{Key: "state", Value: store.StateDisabled}}}})
	if err != nil {
		return s.mapErr(err)
	}
	if res.MatchedCount == 0 {
		return apperr.ErrNotFound
	}
	return nil
}

func (s *Store) TrustedIssuers(ctx context.Context) ([]store.TrustedIssuer, error) {
	var issuers []trustedIssuerDoc
	if err := s.findAll(ctx, trustedIssuersCollection, bson.D{}, &issuers); err != nil {
		return nil, err
	}
	var tenants []tenantStateDoc
	if err := s.findAll(ctx, tenantsCollection, bson.D{}, &tenants, options.Find().SetProjection(bson.D{{Key: "state", Value: 1}})); err != nil {
		return nil, err
	}
	tenantStates := make(map[string]store.State, len(tenants))
	for _, t := range tenants {
		tenantStates[t.ID] = t.State
	}
	out := make([]store.TrustedIssuer, 0, len(issuers))
	for _, i := range issuers {
		tenantState, ok := tenantStates[i.TenantID]
		if !ok {
			continue
		}
		out = append(out, store.TrustedIssuer{Issuer: i.Issuer, TenantID: i.TenantID, IssuerState: i.State, TenantState: tenantState})
	}
	slices.SortFunc(out, func(a, b store.TrustedIssuer) int { return cmp.Compare(a.Issuer, b.Issuer) })
	return out, nil
}

func (s *Store) TrustedKeys(ctx context.Context) ([]store.TrustedKey, error) {
	var docs []trustedKeyDoc
	err := s.findAll(ctx, trustedKeysCollection, bson.D{{Key: "state", Value: store.StateActive}}, &docs,
		options.Find().SetSort(bson.D{{Key: "_id", Value: 1}}))
	if err != nil {
		return nil, err
	}
	return trustedKeys(docs), nil
}

func (s *Store) TrustedKeysOf(ctx context.Context, issuer string) ([]store.TrustedKey, error) {
	var docs []trustedKeyDoc
	keyRange := bson.D{{Key: "_id", Value: bson.D{
		{Key: "$gte", Value: issuer + keySeparator},
		{Key: "$lt", Value: issuer + keyRangeEnd},
	}}}
	if err := s.findAll(ctx, trustedKeysCollection, keyRange, &docs); err != nil {
		return nil, err
	}
	keys := trustedKeys(docs)
	slices.SortFunc(keys, func(a, b store.TrustedKey) int {
		return cmp.Or(a.CreatedAt.Compare(b.CreatedAt), cmp.Compare(a.KeyID, b.KeyID))
	})
	return keys, nil
}

func trustedKeys(docs []trustedKeyDoc) []store.TrustedKey {
	out := make([]store.TrustedKey, 0, len(docs))
	for _, d := range docs {
		out = append(out, store.TrustedKey{
			Issuer: d.Issuer, KeyID: d.KeyID, Algorithm: d.Algorithm, PublicKey: []byte(d.PublicKey),
			State: d.State, CreatedAt: d.CreatedAt, CreatedBy: d.CreatedBy,
		})
	}
	return out
}

func (s *Store) findAll(ctx context.Context, collection string, filter bson.D, out any, opts ...options.Lister[options.FindOptions]) error {
	cur, err := s.db.Collection(collection).Find(ctx, filter, opts...)
	if err != nil {
		return s.mapErr(err)
	}
	return s.mapErr(cur.All(ctx, out))
}

func (s *Store) mustExist(ctx context.Context, collection, id string) error {
	err := s.db.Collection(collection).FindOne(ctx, bson.D{{Key: "_id", Value: id}},
		options.FindOne().SetProjection(bson.D{{Key: "_id", Value: 1}})).Err()
	return s.mapErr(err)
}

func trustedKeyID(issuer, keyID string) string {
	return issuer + keySeparator + keyID
}

func checkIssuerKey(issuer, keyID string) error {
	if err := checkKeyPart("issuer", issuer); err != nil {
		return err
	}
	return checkKeyPart("kid", keyID)
}

func checkKeyPart(name, value string) error {
	if strings.Contains(value, keySeparator) {
		return fmt.Errorf("%w: %s must not contain %q", apperr.ErrInvalidArgument, name, keySeparator)
	}
	return nil
}
