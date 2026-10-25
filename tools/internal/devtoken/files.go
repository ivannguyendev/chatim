package devtoken

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

const (
	PrivateFile = "private.jwk"
	PublicFile  = "jwks.json"

	privateMode fs.FileMode = 0o600
	publicMode  fs.FileMode = 0o644
	dirMode     fs.FileMode = 0o700
)

var ErrKeysExist = errors.New("dev keys already exist")

func WriteFiles(dir string, key KeyPair, force bool) error {
	private, err := key.PrivateJWK()
	if err != nil {
		return fmt.Errorf("encode private jwk: %w", err)
	}
	public, err := key.PublicJWKS()
	if err != nil {
		return fmt.Errorf("encode public jwks: %w", err)
	}
	if err := os.MkdirAll(dir, dirMode); err != nil {
		return fmt.Errorf("create key dir: %w", err)
	}
	if !force {
		for _, name := range []string{PrivateFile, PublicFile} {
			if _, err := os.Lstat(filepath.Join(dir, name)); err == nil {
				return fmt.Errorf("%w in %s: pass -force to replace them", ErrKeysExist, dir)
			} else if !errors.Is(err, fs.ErrNotExist) {
				return fmt.Errorf("check %s: %w", name, err)
			}
		}
	}
	if err := writeAtomic(dir, PrivateFile, private, privateMode); err != nil {
		return err
	}
	return writeAtomic(dir, PublicFile, public, publicMode)
}

func LoadPrivateFile(path string) (KeyPair, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return KeyPair{}, fmt.Errorf("read private jwk: %w", err)
	}
	return ParsePrivateJWK(data)
}

func writeAtomic(dir, name string, data []byte, mode fs.FileMode) error {
	tmp, err := os.CreateTemp(dir, "."+name+".*")
	if err != nil {
		return fmt.Errorf("write %s: %w", name, err)
	}
	done := false
	defer func() {
		if !done {
			_ = os.Remove(tmp.Name())
		}
	}()
	_, werr := tmp.Write(data)
	cerr := tmp.Close()
	if err := errors.Join(werr, cerr); err != nil {
		return fmt.Errorf("write %s: %w", name, err)
	}
	if err := os.Chmod(tmp.Name(), mode); err != nil {
		return fmt.Errorf("chmod %s: %w", name, err)
	}
	if err := os.Rename(tmp.Name(), filepath.Join(dir, name)); err != nil {
		return fmt.Errorf("rename %s: %w", name, err)
	}
	done = true
	return nil
}
