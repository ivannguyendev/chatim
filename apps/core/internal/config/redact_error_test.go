package config_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/ivannguyendev/chatim/apps/core/internal/config"
)

func TestRedactErrorScrubsCredentials(t *testing.T) {
	const multiHost = "mongodb://chatim:pw1secret@m1:27017,m2:27017,m3:27017/?replicaSet=rs0&authMechanismProperties=AWS_SESSION_TOKEN:t0k3nsecret"
	tests := []struct {
		name, raw, msg string
		secrets        []string
	}{
		{"whole uri", "mongodb://chatim:s3cr3t@m1:27017/?replicaSet=rs0", `parse "mongodb://chatim:s3cr3t@m1:27017/?replicaSet=rs0": bad`, []string{"s3cr3t"}},
		{"bare password", "mongodb://chatim:s3cr3t@m1/", "auth failed for password s3cr3t", []string{"s3cr3t"}},
		{"escaped password", "mongodb://chatim:s3%40cr3t@m1/", "bad escape in s3%40cr3t, decoded s3@cr3t", []string{"s3%40cr3t", "s3@cr3t"}},
		{"userinfo", "nats://core:s3cr3t@n1:4222", "authorization core:s3cr3t rejected", []string{"s3cr3t"}},
		{"token only", "nats://s3cr3t@n1:4222", "token s3cr3t rejected", []string{"s3cr3t"}},
		{"second url of a list", "nats://n1:4222,nats://core:s3cr3t@n2:4222", "dial core:s3cr3t@n2 failed", []string{"s3cr3t"}},
		{"every url of a list", "nats://a:pw1secret@n1:4222,nats://b:pw2secret@n2:4222", "n1 said pw1secret, n2 said pw2secret", []string{"pw1secret", "pw2secret"}},
		{"secret query value", "mongodb://m1/?tlsCertificateKeyFilePassword=s3cr3t", "cannot decrypt key with s3cr3t", []string{"s3cr3t"}},
		{"multi-host uri", multiHost, `parse "` + multiHost + `" failed`, []string{"pw1secret", "t0k3nsecret"}},
		{"multi-host query secret alone", multiHost, "session token t0k3nsecret expired", []string{"t0k3nsecret"}},
		{"multi-host password alone", multiHost, "password pw1secret rejected", []string{"pw1secret"}},
		{"escaped query secret", "mongodb://m1,m2/?authMechanismProperties=AWS_SESSION_TOKEN%3At0k3nsecret", "token t0k3nsecret expired", []string{"t0k3nsecret"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := config.RedactError(errors.New(tt.msg), tt.raw).Error()
			for _, secret := range tt.secrets {
				if strings.Contains(got, secret) {
					t.Fatalf("RedactError = %q, still holds %q", got, secret)
				}
			}
			if !strings.Contains(got, "xxxxx") {
				t.Fatalf("RedactError = %q, want the secret replaced by xxxxx", got)
			}
		})
	}
}

func TestRedactTextCoversEveryURL(t *testing.T) {
	mongoURI, natsURL := "mongodb://chatim:pw1secret@m1/", "nats://core:pw2secret@n1:4222"
	got := config.RedactText("mongo pw1secret and nats pw2secret", mongoURI, "", natsURL)
	if strings.Contains(got, "pw1secret") || strings.Contains(got, "pw2secret") {
		t.Fatalf("RedactText = %q, want both secrets removed", got)
	}
}

func TestRedactErrorKeepsTheChain(t *testing.T) {
	raw := "mongodb://chatim:s3cr3t@m1/"
	err := config.RedactError(fmt.Errorf("ping %s: %w", raw, context.DeadlineExceeded), raw)
	wrapped := fmt.Errorf("mongo ping: %w", err)
	if !errors.Is(wrapped, context.DeadlineExceeded) {
		t.Fatalf("errors.Is(%v, DeadlineExceeded) = false, want the chain kept", wrapped)
	}
	if strings.Contains(wrapped.Error(), "s3cr3t") {
		t.Fatalf("wrapped error %q leaks the password", wrapped)
	}
}

func TestRedactErrorLeavesCleanErrorsAlone(t *testing.T) {
	clean := errors.New("connection refused")
	for _, raw := range []string{"mongodb://chatim:s3cr3t@m1/", ""} {
		if got := config.RedactError(clean, raw); !errors.Is(got, clean) || got.Error() != clean.Error() {
			t.Fatalf("RedactError(%q) = %v, want the error unchanged", raw, got)
		}
	}
	if got := config.RedactError(nil, "mongodb://chatim:s3cr3t@m1/"); got != nil {
		t.Fatalf("RedactError(nil) = %v, want nil", got)
	}
}
