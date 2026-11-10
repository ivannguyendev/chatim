package mongostore

import (
	"context"
	"errors"
	"fmt"
	"time"

	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
	"go.mongodb.org/mongo-driver/v2/mongo/readpref"
	"go.mongodb.org/mongo-driver/v2/mongo/writeconcern"

	"github.com/ivannguyendev/chatim/pkg/core/redact"
)

const disconnectTimeout = 5 * time.Second

type Options struct {
	URI              string
	Database         string
	User             string
	Password         string
	AuthSource       string
	AppName          string
	RevocationKeep   time.Duration
	JITWriters       int
	JITWindow        time.Duration
	JITMaxBatch      int
	JITQueue         int
	JITInsertTimeout time.Duration
}

type Store struct {
	client     *mongo.Client
	db         *mongo.Database
	uri        string
	keep       time.Duration
	holder     string
	links      *jitBatcher
	users      *jitBatcher
	jitTimeout time.Duration
	readLink   func(ctx context.Context, id string) (userID string, found bool, err error)
}

func Open(opts Options) (*Store, error) {
	if err := opts.validate(); err != nil {
		return nil, err
	}
	client, err := mongo.Connect(clientOptions(opts))
	if err != nil {
		return nil, fmt.Errorf("mongo connect %s: %w", redact.URL(opts.URI), redact.Error(err, opts.URI))
	}
	db := client.Database(opts.Database, options.Database().
		SetWriteConcern(writeconcern.Majority()).
		SetReadPreference(readpref.Primary()))
	s := &Store{client: client, db: db, uri: opts.URI, keep: opts.RevocationKeep, holder: newHolder(), jitTimeout: opts.JITInsertTimeout}
	cfg := jitConfig{
		writers: opts.JITWriters, window: opts.JITWindow, maxBatch: opts.JITMaxBatch, queue: opts.JITQueue, timeout: opts.JITInsertTimeout,
	}
	s.links = newJITBatcher(cfg, s.insertMany(userLinksCollection), s.unavailable)
	s.users = newJITBatcher(cfg, s.insertMany(usersCollection), s.unavailable)
	s.readLink = s.readLinkMajority
	return s, nil
}

func (o Options) validate() error {
	switch {
	case o.RevocationKeep <= 0:
		return errors.New("mongostore: revocation keep must be positive")
	case o.JITWriters <= 0 || o.JITWindow <= 0 || o.JITMaxBatch <= 0 || o.JITQueue <= 0 || o.JITInsertTimeout <= 0:
		return errors.New("mongostore: just-in-time writers, window, max batch, queue and insert timeout must be positive")
	default:
		return nil
	}
}

func clientOptions(opts Options) *options.ClientOptions {
	client := options.Client().ApplyURI(opts.URI)
	if opts.AppName != "" {
		client.SetAppName(opts.AppName)
	}
	if opts.User != "" {
		client.SetAuth(options.Credential{Username: opts.User, Password: opts.Password, AuthSource: opts.AuthSource})
	}
	return client
}

func (s *Store) Close() {
	s.links.close()
	s.users.close()
	ctx, cancel := context.WithTimeout(context.Background(), disconnectTimeout)
	defer cancel()
	_ = s.client.Disconnect(ctx)
}

func (s *Store) Ping(ctx context.Context) error {
	return s.mapErr(s.client.Ping(ctx, readpref.Primary()))
}
