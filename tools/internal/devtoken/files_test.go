package devtoken_test

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/ivannguyendev/chatim/pkg/jwtverify"
	"github.com/ivannguyendev/chatim/tools/internal/devtoken"
)

func mode(t *testing.T, path string) os.FileMode {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat %s: %v", path, err)
	}
	return info.Mode().Perm()
}

func TestWriteFilesKeepsThePrivateKeyOwnerOnly(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "ws-keys")
	key, err := devtoken.Generate("dev-1")
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if err := devtoken.WriteFiles(dir, key, false); err != nil {
		t.Fatalf("WriteFiles: %v", err)
	}
	private := filepath.Join(dir, devtoken.PrivateFile)
	public := filepath.Join(dir, devtoken.PublicFile)
	if got := mode(t, dir); got != 0o700 {
		t.Fatalf("dir mode = %o; want 700", got)
	}
	if got := mode(t, private); got != 0o600 {
		t.Fatalf("private mode = %o; want 600", got)
	}
	if got := mode(t, public); got != 0o644 {
		t.Fatalf("public mode = %o; want 644", got)
	}
	loaded, err := devtoken.LoadPrivateFile(private)
	if err != nil {
		t.Fatalf("LoadPrivateFile: %v", err)
	}
	token, err := loaded.Sign(testClaims())
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	v, err := jwtverify.LoadFile(public, jwtverify.Config{
		Issuer:   "chatim-dev",
		Audience: "chatim-websocket",
		Leeway:   30 * time.Second,
		Now:      func() time.Time { return testNow },
	})
	if err != nil {
		t.Fatalf("jwtverify.LoadFile: %v", err)
	}
	if _, err := v.Verify(token); err != nil {
		t.Fatalf("Verify with the written key set: %v", err)
	}
}

func TestWriteFilesRefusesToOverwriteWithoutForce(t *testing.T) {
	dir := t.TempDir()
	first, err := devtoken.Generate("dev-1")
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if err := devtoken.WriteFiles(dir, first, false); err != nil {
		t.Fatalf("WriteFiles: %v", err)
	}
	before, err := os.ReadFile(filepath.Join(dir, devtoken.PrivateFile))
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	second, err := devtoken.Generate("dev-2")
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if err := devtoken.WriteFiles(dir, second, false); !errors.Is(err, devtoken.ErrKeysExist) {
		t.Fatalf("WriteFiles over existing keys = %v; want ErrKeysExist", err)
	}
	after, err := os.ReadFile(filepath.Join(dir, devtoken.PrivateFile))
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if !bytes.Equal(before, after) {
		t.Fatal("refused write changed the private key")
	}
	if err := devtoken.WriteFiles(dir, second, true); err != nil {
		t.Fatalf("WriteFiles with force: %v", err)
	}
	loaded, err := devtoken.LoadPrivateFile(filepath.Join(dir, devtoken.PrivateFile))
	if err != nil {
		t.Fatalf("LoadPrivateFile: %v", err)
	}
	if loaded.KeyID != "dev-2" {
		t.Fatalf("kid after force = %q; want dev-2", loaded.KeyID)
	}
}

func TestWriteFilesRefusesWhenOnlyOneFileExists(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, devtoken.PublicFile), []byte(`{"keys":[]}`), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	key, err := devtoken.Generate("dev-1")
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if err := devtoken.WriteFiles(dir, key, false); !errors.Is(err, devtoken.ErrKeysExist) {
		t.Fatalf("WriteFiles = %v; want ErrKeysExist", err)
	}
}

func TestForceTightensALoosePrivateFile(t *testing.T) {
	dir := t.TempDir()
	private := filepath.Join(dir, devtoken.PrivateFile)
	if err := os.WriteFile(private, []byte("{}"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	key, err := devtoken.Generate("dev-1")
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if err := devtoken.WriteFiles(dir, key, true); err != nil {
		t.Fatalf("WriteFiles: %v", err)
	}
	if got := mode(t, private); got != 0o600 {
		t.Fatalf("private mode = %o; want 600", got)
	}
}

func TestLoadPrivateFileRefusesMissingAndInvalidFiles(t *testing.T) {
	dir := t.TempDir()
	if _, err := devtoken.LoadPrivateFile(filepath.Join(dir, "missing.jwk")); err == nil {
		t.Fatal("LoadPrivateFile of a missing file succeeded")
	}
	bad := filepath.Join(dir, "bad.jwk")
	if err := os.WriteFile(bad, []byte("{"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if _, err := devtoken.LoadPrivateFile(bad); !errors.Is(err, devtoken.ErrInvalidKey) {
		t.Fatalf("LoadPrivateFile of a bad file = %v; want ErrInvalidKey", err)
	}
}
