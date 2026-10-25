package redact_test

import (
	"testing"

	"github.com/ivannguyendev/chatim/pkg/redact"
)

func TestRedactURL(t *testing.T) {
	tests := []struct{ raw, want string }{
		{"", ""},
		{"nats://n1:4222", "nats://n1:4222"},
		{
			"mongodb://chatim:s3cr3t@chatim-mongodb:27017/?replicaSet=rs0&authSource=admin",
			"mongodb://xxxxx@chatim-mongodb:27017/?replicaSet=rs0&authSource=admin",
		},
		{"mongodb+srv://u:s3cr3t@cluster.example.net/db", "mongodb+srv://xxxxx@cluster.example.net/db"},
		{"mongodb://u:p@ss/w:s3cr3t@m1,m2/?replicaSet=rs0", "mongodb://xxxxx@m1,m2/?replicaSet=rs0"},
		{
			"mongodb://m1:27017/?tlsCertificateKeyFilePassword=s3cr3t&replicaSet=rs0",
			"mongodb://m1:27017/?tlsCertificateKeyFilePassword=xxxxx&replicaSet=rs0",
		},
		{
			"mongodb://m1/?authMechanism=MONGODB-AWS&authMechanismProperties=AWS_SESSION_TOKEN:s3cr3t",
			"mongodb://m1/?authMechanism=MONGODB-AWS&authMechanismProperties=xxxxx",
		},
		{"nats://u:s3cr3t@n1:4222,nats://u:s3cr3t@n2:4222", "nats://xxxxx@n2:4222"},
		{"s3cr3t-without-a-scheme", "xxxxx"},
	}
	for _, tt := range tests {
		if got := redact.URL(tt.raw); got != tt.want {
			t.Errorf("URL(%q) = %q, want %q", tt.raw, got, tt.want)
		}
	}
}
