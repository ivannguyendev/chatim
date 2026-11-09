package mongostore

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
	"go.mongodb.org/mongo-driver/v2/mongo/readconcern"

	"github.com/ivannguyendev/chatim/apps/identity/internal/store"
	"github.com/ivannguyendev/chatim/pkg/core/apperr"
	"github.com/ivannguyendev/chatim/pkg/ids"
)

var (
	errLinkPending   = fmt.Errorf("%w: user link not visible yet", apperr.ErrUnavailable)
	errLinkCorrupt   = fmt.Errorf("%w: user link holds an invalid user id", apperr.ErrUnavailable)
	errSubjectFields = fmt.Errorf("%w: tenant, realm and sub are required and must not hold %q", apperr.ErrInvalidArgument, keySeparator)
)

func (s *Store) LinkUser(ctx context.Context, u store.NewUser) (store.LinkedUser, error) {
	if err := validNewUser(u); err != nil {
		return store.LinkedUser{}, err
	}
	key := linkKey(u.TenantID, u.Realm, u.Sub)
	created, err := s.links.write(ctx, key, newLinkDoc(key, u))
	if err != nil {
		return store.LinkedUser{}, s.linkFailure(err)
	}
	id := u.CandidateID
	if !created {
		stored, err := s.storedLink(ctx, key)
		if errors.Is(err, errLinkPending) {
			return store.LinkedUser{Result: store.LinkPending}, err
		}
		if err != nil {
			return store.LinkedUser{}, err
		}
		id = stored
	}
	w := store.ProfileWrite{TenantID: u.TenantID, UserID: id, DisplayName: u.DisplayName, AvatarURL: u.AvatarURL, At: u.At, By: u.By}
	userKey := tenantKey(u.TenantID, id)
	doc := append(newUserDoc(userKey, w), bson.E{Key: "realm", Value: u.Realm}, bson.E{Key: "sub", Value: u.Sub})
	written, err := s.users.write(ctx, userKey, doc)
	if err != nil {
		return store.LinkedUser{}, s.linkFailure(err)
	}
	return store.LinkedUser{ID: id, Result: linkResult(created, written)}, nil
}

func (s *Store) storedLink(ctx context.Context, key string) (string, error) {
	rctx, cancel := context.WithTimeout(ctx, s.jitTimeout)
	defer cancel()
	id, found, err := s.readLink(rctx, key)
	switch {
	case err != nil:
		return "", s.linkFailure(err)
	case !found:
		return "", errLinkPending
	case ids.ValidUser(id) != nil:
		return "", errLinkCorrupt
	default:
		return id, nil
	}
}

func (s *Store) readLinkMajority(ctx context.Context, key string) (string, bool, error) {
	var doc struct {
		UserID string `bson:"user_id"`
	}
	coll := s.db.Collection(userLinksCollection, options.Collection().SetReadConcern(readconcern.Majority()))
	err := coll.FindOne(ctx, linkFilter(key), options.FindOne().SetProjection(linkProjection)).Decode(&doc)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return doc.UserID, true, nil
}

var linkProjection = bson.D{{Key: "_id", Value: 0}, {Key: "user_id", Value: 1}}

func linkFilter(key string) bson.D {
	return bson.D{{Key: "_id", Value: key}}
}

func (s *Store) linkFailure(err error) error {
	if errors.Is(err, apperr.ErrUnavailable) {
		return err
	}
	return s.unavailable(err)
}

func linkResult(created, written bool) store.LinkResult {
	switch {
	case created:
		return store.LinkCreated
	case written:
		return store.LinkHealed
	default:
		return store.LinkExisting
	}
}

func validNewUser(u store.NewUser) error {
	for _, part := range []string{u.TenantID, u.Realm, u.Sub} {
		if part == "" || strings.Contains(part, keySeparator) {
			return errSubjectFields
		}
	}
	return ids.ValidUser(u.CandidateID)
}

func linkKey(tenantID, realm, sub string) string {
	return tenantID + keySeparator + realm + keySeparator + sub
}

func newLinkDoc(key string, u store.NewUser) bson.D {
	return bson.D{
		{Key: "_id", Value: key},
		{Key: "tenant_id", Value: u.TenantID},
		{Key: "realm", Value: u.Realm},
		{Key: "sub", Value: u.Sub},
		{Key: "user_id", Value: u.CandidateID},
		{Key: "created_at", Value: u.At},
		{Key: "created_by", Value: u.By},
	}
}
