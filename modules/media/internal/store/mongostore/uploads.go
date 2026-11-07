package mongostore

import (
	"context"
	"errors"
	"fmt"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	"github.com/ivannguyendev/chatim/modules/media/internal/domain"
)

type uploadDoc struct {
	Key         string     `bson:"_id"`
	Tenant      string     `bson:"tenant_id"`
	User        string     `bson:"user_id"`
	FileName    string     `bson:"file_name"`
	ContentType string     `bson:"content_type"`
	Size        int64      `bson:"size"`
	Dim         []int32    `bson:"dim,omitempty"`
	Dur         int64      `bson:"dur,omitempty"`
	Hash        string     `bson:"hash,omitempty"`
	PartSize    int64      `bson:"part_size"`
	PartCount   int64      `bson:"part_count"`
	UploadID    string     `bson:"upload_id"`
	State       int32      `bson:"state"`
	Ver         int64      `bson:"ver"`
	CreatedAt   time.Time  `bson:"created_at"`
	CompletedAt *time.Time `bson:"completed_at,omitempty"`
	UpdatedAt   time.Time  `bson:"updated_at"`
}

type Uploads struct {
	coll *mongo.Collection
}

func NewUploads(db *mongo.Database) *Uploads {
	return &Uploads{coll: db.Collection(uploadsCollection)}
}

func (s *Uploads) Insert(ctx context.Context, u domain.Upload) error {
	_, err := s.coll.InsertOne(ctx, toDoc(u))
	switch {
	case hasCode(err, duplicateKeyCode):
		return domain.ErrRetryLater
	case err != nil:
		return fmt.Errorf("insert upload: %w", err)
	}
	return nil
}

func (s *Uploads) Get(ctx context.Context, key string) (domain.Upload, error) {
	var d uploadDoc
	err := s.coll.FindOne(ctx, bson.D{{Key: "_id", Value: key}}).Decode(&d)
	switch {
	case errors.Is(err, mongo.ErrNoDocuments):
		return domain.Upload{}, domain.ErrUploadNotFound
	case err != nil:
		return domain.Upload{}, fmt.Errorf("get upload: %w", err)
	}
	return fromDoc(d), nil
}

func (s *Uploads) Transition(ctx context.Context, key string, from domain.State, fromVer int64, to domain.State, hash string) (domain.Upload, error) {
	set := bson.D{
		{Key: "state", Value: bson.D{{Key: "$literal", Value: int32(to)}}},
		{Key: "ver", Value: bson.D{{Key: "$add", Value: bson.A{"$ver", int64(1)}}}},
		{Key: "updated_at", Value: "$$NOW"},
	}
	if to == domain.StateUploaded {
		set = append(set, bson.E{Key: "completed_at", Value: "$$NOW"})
		if hash != "" {
			set = append(set, bson.E{Key: "hash", Value: bson.D{{Key: "$literal", Value: hash}}})
		}
	}
	filter := bson.D{{Key: "_id", Value: key}, {Key: "state", Value: int32(from)}, {Key: "ver", Value: fromVer}}
	var d uploadDoc
	err := s.coll.FindOneAndUpdate(ctx, filter, mongo.Pipeline{{{Key: "$set", Value: set}}},
		options.FindOneAndUpdate().SetReturnDocument(options.After)).Decode(&d)
	switch {
	case errors.Is(err, mongo.ErrNoDocuments):
		return domain.Upload{}, domain.ErrRetryLater
	case err != nil:
		return domain.Upload{}, fmt.Errorf("transition upload: %w", err)
	}
	return fromDoc(d), nil
}

func toDoc(u domain.Upload) uploadDoc {
	d := uploadDoc{
		Key: u.Key, Tenant: u.Tenant, User: u.User, FileName: u.FileName, ContentType: u.ContentType,
		Size: u.Size, Dim: u.Dim, Dur: u.Dur, Hash: u.Hash, PartSize: u.PartSize, PartCount: int64(u.PartCount), UploadID: u.UploadID,
		State: int32(u.State), Ver: u.Ver, CreatedAt: u.CreatedAt, UpdatedAt: u.UpdatedAt,
	}
	if !u.CompletedAt.IsZero() {
		at := u.CompletedAt
		d.CompletedAt = &at
	}
	return d
}

func fromDoc(d uploadDoc) domain.Upload {
	u := domain.Upload{
		Key: d.Key, Tenant: d.Tenant, User: d.User, FileName: d.FileName, ContentType: d.ContentType,
		Size: d.Size, Dim: d.Dim, Dur: d.Dur, Hash: d.Hash, PartSize: d.PartSize, PartCount: int(d.PartCount), UploadID: d.UploadID,
		State: domain.State(d.State), Ver: d.Ver, CreatedAt: d.CreatedAt.UTC(), UpdatedAt: d.UpdatedAt.UTC(),
	}
	if d.CompletedAt != nil {
		u.CompletedAt = d.CompletedAt.UTC()
	}
	return u
}
