package mongostore

import (
	"context"
	"errors"
	"fmt"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/pkg/apperr"
	"github.com/ivannguyendev/chatim/pkg/keys"
)

func (s *Store) Insert(ctx context.Context, msgs []domain.Message) []store.Result {
	out := make([]store.Result, len(msgs))
	if err := ctx.Err(); err != nil {
		for i := range out {
			out[i] = store.Result{Outcome: store.Rejected, Err: fmt.Errorf("insert messages: %w", err)}
		}
		return out
	}
	docs := make([]any, 0, len(msgs))
	sent := make([]int, 0, len(msgs))
	for i, m := range msgs {
		doc, err := encodeMessage(m)
		if err != nil {
			out[i] = store.Result{Outcome: store.Rejected, Err: err}
			continue
		}
		docs = append(docs, doc)
		sent = append(sent, i)
	}
	if len(docs) == 0 {
		return out
	}
	_, err := s.messages.InsertMany(ctx, docs, options.InsertMany().SetOrdered(false))
	classifyInsert(out, sent, err)
	return out
}

func classifyInsert(out []store.Result, sent []int, err error) {
	if err == nil {
		fill(out, sent, store.Result{Outcome: store.Inserted})
		return
	}
	bwe, ok := errors.AsType[mongo.BulkWriteException](err)
	if !ok || len(bwe.WriteErrors) == 0 || !batchIndexesWithin(bwe.WriteErrors, len(sent)) {
		fill(out, sent, unknown(err))
		return
	}
	clean := store.Result{Outcome: store.Inserted}
	if bwe.WriteConcernError != nil {
		clean = unknown(err)
	}
	fill(out, sent, clean)
	for _, we := range bwe.WriteErrors {
		out[sent[we.Index]] = writeErrorResult(we)
	}
}

func writeErrorResult(we mongo.BulkWriteError) store.Result {
	if mongo.IsDuplicateKeyError(we.WriteError) {
		return store.Result{Outcome: store.Duplicate}
	}
	return store.Result{Outcome: store.Rejected, Err: fmt.Errorf("insert message: %w: %w", apperr.ErrInvalidArgument, we.WriteError)}
}

func unknown(err error) store.Result {
	return store.Result{Outcome: store.Unknown, Err: fmt.Errorf("insert messages: %w", err)}
}

func fill(out []store.Result, at []int, r store.Result) {
	for _, i := range at {
		out[i] = r
	}
}

func (s *Store) Last(ctx context.Context, room, thread uint64) (uint64, error) {
	q := pageRange(store.PageQuery{Room: room, Thread: thread, Anchor: store.Latest, Limit: 1})
	msgs, err := find(ctx, s.messages, q)
	if err != nil {
		return 0, fmt.Errorf("last message of %d/%d: %w", room, thread, err)
	}
	if len(msgs) == 0 {
		return 0, nil
	}
	return msgs[0].Seq, nil
}

func (s *Store) Find(ctx context.Context, room uint64, ks []store.MsgKey) ([]domain.Message, error) {
	if err := store.ValidateKeys(room, ks); err != nil {
		return nil, err
	}
	if len(ks) == 0 {
		return nil, ctx.Err()
	}
	ids := make(bson.A, len(ks))
	for i, k := range ks {
		ids[i] = keys.Msg(k.Room, k.Thread, k.Seq)
	}
	q := messageQuery{filter: bson.D{{Key: "_id", Value: bson.D{{Key: "$in", Value: ids}}}}}
	msgs, err := find(ctx, s.committed, q)
	if err != nil {
		return nil, fmt.Errorf("find messages in room %d: %w", room, err)
	}
	return msgs, nil
}
