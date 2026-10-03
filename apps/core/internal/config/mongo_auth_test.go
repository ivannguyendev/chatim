package config_test

import (
	"strings"
	"testing"

	"github.com/ivannguyendev/chatim/apps/core/internal/config"
)

func TestLoadRejectsConflictingMongoCredentials(t *testing.T) {
	tests := []struct {
		name string
		env  map[string]string
		want string
	}{
		{
			"user next to credentials in the uri",
			map[string]string{"MONGO_URI": "mongodb://chatim:s3cr3t@m1/?replicaSet=rs0", "MONGO_USER": "chatim"},
			"MONGO_USER must not be set when MONGO_URI carries credentials",
		},
		{
			"password without a user",
			map[string]string{"MONGO_PASSWORD": "s3cr3t"},
			"MONGO_PASSWORD needs MONGO_USER",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			setEnv(t, tt.env)
			_, err := config.Load()
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("Load() = %v, want an error containing %q", err, tt.want)
			}
			if strings.Contains(err.Error(), "s3cr3t") {
				t.Errorf("error leaks a password: %v", err)
			}
		})
	}
}

func TestLoadAcceptsEitherMongoCredentialSource(t *testing.T) {
	for _, env := range []map[string]string{
		{"MONGO_URI": "mongodb://chatim:pw@m1/?replicaSet=rs0"},
		{"MONGO_USER": "chatim", "MONGO_PASSWORD": "pw"},
		{"MONGO_USER": "chatim"},
	} {
		setEnv(t, env)
		if _, err := config.Load(); err != nil {
			t.Errorf("Load() with %v = %v", env, err)
		}
	}
}

func TestConfigLogValueReportsMongoAuth(t *testing.T) {
	tests := []struct {
		env  map[string]string
		want string
	}{
		{nil, "mongo_auth=false"},
		{map[string]string{"MONGO_USER": "chatim", "MONGO_PASSWORD": "s3cr3t"}, "mongo_auth=true"},
		{map[string]string{"MONGO_URI": "mongodb://chatim:s3cr3t@m1/?replicaSet=rs0"}, "mongo_auth=true"},
	}
	for _, tt := range tests {
		setEnv(t, tt.env)
		cfg, err := config.Load()
		if err != nil {
			t.Fatalf("Load: %v", err)
		}
		out := cfg.String()
		if !strings.Contains(out, tt.want) || strings.Contains(out, "s3cr3t") {
			t.Errorf("summary %s: want %s and no password", out, tt.want)
		}
	}
}
