package envconfig

import (
	"fmt"
	"os"
	"strings"
)

func Secret(key string) (string, error) {
	fileKey := key + "_FILE"
	path := os.Getenv(fileKey)
	if path == "" {
		return os.Getenv(key), nil
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("%s: %w", fileKey, err)
	}
	return strings.TrimRight(string(b), "\r\n"), nil
}
