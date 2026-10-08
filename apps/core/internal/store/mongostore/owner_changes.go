package mongostore

import (
	"context"
	"errors"
	"fmt"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
	"go.mongodb.org/mongo-driver/v2/mongo/readconcern"
	"go.mongodb.org/mongo-driver/v2/mongo/readpref"
	"go.mongodb.org/mongo-driver/v2/mongo/writeconcern"

	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
)

const (
	writeConflictCode         = 112
	transientTransactionLabel = "TransientTransactionError"
	unknownCommitResultLabel  = "UnknownTransactionCommitResult"
)

func (s *Store) ChangeOwners(ctx context.Context, room uint64, users []string, decide store.OwnerDecision) (store.OwnerResult, error) {
	if err := store.ValidateLimit(len(users), domain.MaxMemberBatch+1); err != nil {
		return store.OwnerResult{}, err
	}
	key, err := toInt64("room id", room)
	if err != nil {
		return store.OwnerResult{}, fmt.Errorf("change owners of room %d: %w", room, domain.ErrRoomNotFound)
	}
	sess, err := s.client.StartSession()
	if err != nil {
		return store.OwnerResult{}, fmt.Errorf("change owners of room %d: start session: %w", room, err)
	}
	defer sess.EndSession(context.WithoutCancel(ctx))
	if err := sess.StartTransaction(ownerTransactionOptions()); err != nil {
		return store.OwnerResult{}, fmt.Errorf("change owners of room %d: start transaction: %w", room, err)
	}
	res, err := s.changeOwners(mongo.NewSessionContext(ctx, sess), key, room, users, decide)
	if err != nil || len(res.Written) == 0 {
		_ = sess.AbortTransaction(context.WithoutCancel(ctx))
		return store.OwnerResult{}, ownerChangeError(room, err)
	}
	if err := sess.CommitTransaction(ctx); err != nil {
		return store.OwnerResult{}, ownerChangeError(room, fmt.Errorf("change owners of room %d: commit: %w", room, err))
	}
	return res, nil
}

func ownerTransactionOptions() *options.TransactionOptionsBuilder {
	return options.Transaction().
		SetReadConcern(readconcern.Snapshot()).
		SetWriteConcern(writeconcern.Majority()).
		SetReadPreference(readpref.Primary())
}

func (s *Store) changeOwners(ctx context.Context, key int64, room uint64, users []string, decide store.OwnerDecision) (store.OwnerResult, error) {
	view, err := s.ownerView(ctx, key, room, users)
	if err != nil {
		return store.OwnerResult{}, err
	}
	writes, err := decide(view)
	if err != nil || len(writes) == 0 {
		return store.OwnerResult{}, err
	}
	if err := store.ValidateOwnerWrites(room, writes); err != nil {
		return store.OwnerResult{}, err
	}
	res := store.OwnerResult{Written: make([]domain.Member, 0, len(writes))}
	for _, w := range writes {
		ok, err := applyMember(ctx, s.members, w.Cur, w.Next)
		if err != nil {
			return store.OwnerResult{}, err
		}
		if !ok {
			return store.OwnerResult{}, fmt.Errorf("change owners of room %d: member %q moved past ver %d: %w", room, w.Cur.User, w.Cur.Ver, domain.ErrRetryLater)
		}
		res.Written = append(res.Written, w.Next)
	}
	inc := bson.D{{Key: "owners_ver", Value: int64(1)}}
	delta := store.MemberCountDelta(writes)
	if delta != 0 {
		inc = append(inc, memberCountFields(delta)...)
	}
	count, err := updateMemberCount(ctx, s.rooms, bson.D{{Key: "_id", Value: key}}, bson.D{{Key: "$inc", Value: inc}})
	if err != nil {
		return store.OwnerResult{}, fmt.Errorf("change owners of room %d: bump owners ver: %w", room, err)
	}
	if delta != 0 {
		res.Count, res.CountChanged = count, true
	}
	return res, nil
}

func ownerChangeError(room uint64, err error) error {
	se, ok := errors.AsType[mongo.ServerError](err)
	if ok && (se.HasErrorLabel(transientTransactionLabel) || se.HasErrorLabel(unknownCommitResultLabel) || se.HasErrorCode(writeConflictCode)) {
		return fmt.Errorf("change owners of room %d: %w: %w", room, domain.ErrRetryLater, err)
	}
	return err
}
