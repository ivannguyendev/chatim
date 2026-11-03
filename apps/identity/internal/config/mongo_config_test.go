package config_test

import (
	"strings"
	"testing"

	"github.com/ivannguyendev/chatim/apps/identity/internal/config"
)

func TestMongoAuthSourceFollowsTheDatabase(t *testing.T) {
	setRequired(t)
	t.Setenv("IDENTITY_MONGO_DB", "identity_eu")
	c, err := config.Load()
	if err != nil || c.MongoDB != "identity_eu" || c.MongoAuthSource != "identity_eu" {
		t.Fatalf("Load = %q %q, %v; want the auth source to follow IDENTITY_MONGO_DB", c.MongoDB, c.MongoAuthSource, err)
	}
	t.Setenv("IDENTITY_MONGO_AUTH_SOURCE", "admin")
	c, err = config.Load()
	if err != nil || c.MongoAuthSource != "admin" {
		t.Fatalf("Load = %q, %v; want the set auth source", c.MongoAuthSource, err)
	}
}

func TestMongoWithoutCredentialsIsAccepted(t *testing.T) {
	setRequired(t)
	t.Setenv("IDENTITY_MONGO_USER", "")
	t.Setenv("IDENTITY_MONGO_PASSWORD", "")
	c, err := config.Load()
	if err != nil || c.MongoUser != "" || c.MongoPassword != "" {
		t.Fatalf("Load = %+v, %v; want a config without Mongo credentials", c, err)
	}
}

func TestLoadRefusesMongoCredentialsOutsideTheirSettings(t *testing.T) {
	const inURI = "IDENTITY_MONGO_URI must not carry credentials; use IDENTITY_MONGO_USER and IDENTITY_MONGO_PASSWORD or IDENTITY_MONGO_PASSWORD_FILE"
	cases := map[string]struct {
		env  map[string]string
		want string
	}{
		"user and password in the uri": {map[string]string{"IDENTITY_MONGO_URI": "mongodb://identity:inline@chatim-mongodb:27017/?replicaSet=rs0"}, inURI},
		"user only in the uri":         {map[string]string{"IDENTITY_MONGO_URI": "mongodb://identity@m1,m2/chatim_identity"}, inURI},
		"srv uri with credentials":     {map[string]string{"IDENTITY_MONGO_URI": "mongodb+srv://identity:inline@cluster.example/"}, inURI},
		"password without a user":      {map[string]string{"IDENTITY_MONGO_USER": ""}, "IDENTITY_MONGO_PASSWORD needs IDENTITY_MONGO_USER"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			setRequired(t)
			for k, v := range tc.env {
				t.Setenv(k, v)
			}
			_, err := config.Load()
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("Load = %v, want an error containing %q", err, tc.want)
			}
			if strings.Contains(err.Error(), "inline") {
				t.Fatalf("Load error carries the password: %v", err)
			}
		})
	}
	setRequired(t)
	t.Setenv("IDENTITY_MONGO_URI", "mongodb://m1:27017,m2:27017/?replicaSet=rs0&authMechanism=SCRAM-SHA-256&tag=a@b")
	if _, err := config.Load(); err != nil {
		t.Fatalf("Load with an @ only in the options = %v", err)
	}
}

func TestLogValueListsMongoSettings(t *testing.T) {
	setRequired(t)
	t.Setenv("IDENTITY_MONGO_URI", "mongodb://chatim-mongodb:27017/?replicaSet=rs0")
	c, err := config.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	out := c.String()
	for _, key := range []string{"mongo_uri=mongodb://chatim-mongodb:27017/?replicaSet=rs0", "mongo_db=chatim_identity", "mongo_user=identity", "mongo_password_set=true", "mongo_auth_source=chatim_identity"} {
		if !strings.Contains(out, key) {
			t.Fatalf("config log lacks %s: %s", key, out)
		}
	}
	for _, key := range []string{"pg_", "feed_wait_max"} {
		if strings.Contains(out, key) {
			t.Fatalf("config log still lists %s: %s", key, out)
		}
	}
}
