package envconfig_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ivannguyendev/chatim/pkg/envconfig"
)

func writeSecret(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "secret")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write secret file: %v", err)
	}
	return path
}

func TestSecretReadsTheFileFirst(t *testing.T) {
	tests := []struct{ name, content, plain, want string }{
		{"trailing newline trimmed", "file-pw\n", "", "file-pw"},
		{"crlf trimmed", "file-pw\r\n", "", "file-pw"},
		{"no newline", "file-pw", "", "file-pw"},
		{"file wins over the plain var", "file-pw\n", "plain-pw", "file-pw"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("CHATIM_TEST_SECRET_FILE", writeSecret(t, tt.content))
			t.Setenv("CHATIM_TEST_SECRET", tt.plain)
			got, err := envconfig.Secret("CHATIM_TEST_SECRET")
			if err != nil || got != tt.want {
				t.Fatalf("Secret = %q, %v; want %q, nil", got, err, tt.want)
			}
		})
	}
}

func TestSecretFallsBackToThePlainVar(t *testing.T) {
	t.Setenv("CHATIM_TEST_SECRET_FILE", "")
	t.Setenv("CHATIM_TEST_SECRET", "plain-pw")
	got, err := envconfig.Secret("CHATIM_TEST_SECRET")
	if err != nil || got != "plain-pw" {
		t.Fatalf("Secret = %q, %v; want plain-pw, nil", got, err)
	}
}

func TestSecretIsEmptyWhenNothingIsSet(t *testing.T) {
	t.Setenv("CHATIM_TEST_SECRET_FILE", "")
	t.Setenv("CHATIM_TEST_SECRET", "")
	got, err := envconfig.Secret("CHATIM_TEST_SECRET")
	if err != nil || got != "" {
		t.Fatalf("Secret = %q, %v; want empty, nil", got, err)
	}
}

func TestSecretRejectsAMissingFile(t *testing.T) {
	t.Setenv("CHATIM_TEST_SECRET_FILE", filepath.Join(t.TempDir(), "missing"))
	t.Setenv("CHATIM_TEST_SECRET", "plain-s3cr3t")
	got, err := envconfig.Secret("CHATIM_TEST_SECRET")
	if err == nil || !strings.Contains(err.Error(), "CHATIM_TEST_SECRET_FILE") {
		t.Fatalf("Secret error = %v, want one naming CHATIM_TEST_SECRET_FILE", err)
	}
	if got != "" || strings.Contains(err.Error(), "plain-s3cr3t") {
		t.Fatalf("Secret = %q, %v; want empty and no plain value in the error", got, err)
	}
}
