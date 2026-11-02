package mongostore

import (
	"context"
	"fmt"

	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
	"go.mongodb.org/mongo-driver/v2/mongo/readconcern"
	"go.mongodb.org/mongo-driver/v2/mongo/readpref"
	"go.mongodb.org/mongo-driver/v2/mongo/writeconcern"
)

var beforeCommit func()

func (s *Store) runOnce(ctx context.Context, work func(ctx context.Context) error) error {
	sess, err := s.client.StartSession()
	if err != nil {
		return s.mapErr(fmt.Errorf("start session: %w", err))
	}
	defer sess.EndSession(context.WithoutCancel(ctx))
	if err := sess.StartTransaction(transactionOptions()); err != nil {
		return s.mapErr(fmt.Errorf("start transaction: %w", err))
	}
	if err := work(mongo.NewSessionContext(ctx, sess)); err != nil {
		_ = sess.AbortTransaction(context.WithoutCancel(ctx))
		return s.mapErr(err)
	}
	if beforeCommit != nil {
		beforeCommit()
	}
	if err := sess.CommitTransaction(ctx); err != nil {
		_ = sess.AbortTransaction(context.WithoutCancel(ctx))
		return s.mapErr(fmt.Errorf("commit transaction: %w", err))
	}
	return nil
}

func transactionOptions() *options.TransactionOptionsBuilder {
	return options.Transaction().
		SetReadConcern(readconcern.Snapshot()).
		SetWriteConcern(writeconcern.Majority()).
		SetReadPreference(readpref.Primary())
}
