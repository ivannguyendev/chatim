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
	tests := []struct {
		name, raw, msg string
	}{
		{"whole uri", "mongodb://chatim:s3cr3t@m1:27017/?replicaSet=rs0", `parse "mongodb://chatim:s3cr3t@m1:27017/?replicaSet=rs0": bad`},
		{"bare password", "mongodb://chatim:s3cr3t@m1/", "auth failed for password s3cr3t"},
		{"escaped password", "mongodb://chatim:s3%40cr3t@m1/", "bad escape in s3%40cr3t, decoded s3@cr3t"},
		{"userinfo", "nats://core:s3cr3t@n1:4222", "authorization core:s3cr3t rejected"},
		{"token only", "nats://s3cr3t@n1:4222", "token s3cr3t rejected"},
		{"second url of a list", "nats://n1:4222,nats://core:s3cr3t@n2:4222", "dial core:s3cr3t@n2 failed"},
		{"secret query value", "mongodb://m1/?tlsCertificateKeyFilePassword=s3cr3t", "cannot decrypt key with s3cr3t"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := config.RedactError(errors.New(tt.msg), tt.raw).Error()
			for _, secret := range []string{"s3cr3t", "s3%40cr3t", "s3@cr3t"} {
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
