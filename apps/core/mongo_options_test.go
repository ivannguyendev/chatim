package main

import (
	"testing"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/config"
)

func TestMongoOptionsAuthenticateOnlyWithAUser(t *testing.T) {
	base := config.Config{MongoURI: "mongodb://m1:27017/?replicaSet=rs0", MongoAuthSource: "admin", ConnectTimeout: time.Second}
	if opts := mongoOptions(base); opts.Auth != nil {
		t.Errorf("Auth = %+v without MONGO_USER, want nil", opts.Auth)
	}
	withUser := base
	withUser.MongoUser, withUser.MongoPassword, withUser.MongoAuthSource = "chatim", "mongo-pw", "chatim_auth"
	opts := mongoOptions(withUser)
	if opts.Auth == nil {
		t.Fatal("Auth = nil with MONGO_USER")
	}
	if got := *opts.Auth; got.Username != "chatim" || got.Password != "mongo-pw" || got.AuthSource != "chatim_auth" {
		t.Errorf("Auth = user %q source %q, want chatim/chatim_auth with the password", got.Username, got.AuthSource)
	}
	if opts.ReplicaSet == nil || *opts.ReplicaSet != "rs0" {
		t.Errorf("ReplicaSet = %v, want rs0 from MONGO_URI", opts.ReplicaSet)
	}
}

func TestMongoOptionsKeepCredentialsOfTheURI(t *testing.T) {
	cfg := config.Config{MongoURI: "mongodb://chatim:uri-pw@m1:27017/?authSource=admin", MongoAuthSource: "admin", ConnectTimeout: time.Second}
	opts := mongoOptions(cfg)
	if opts.Auth == nil || opts.Auth.Username != "chatim" || opts.Auth.Password != "uri-pw" {
		t.Errorf("Auth = %v, want the credentials of MONGO_URI", opts.Auth)
	}
}
