package wspublish

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/ivannguyendev/chatim/pkg/apperr"
)

const (
	DefaultRoot   = "websocket.publish"
	DefaultStream = "CHATIM_WS"
	pageHashChars = 8
)

var (
	rootPattern   = regexp.MustCompile(`^[a-z0-9_]+(\.[a-z0-9_]+)*$`)
	streamPattern = regexp.MustCompile(`^[A-Z0-9_]+$`)
)

func ValidRoot(root string) error {
	if !rootPattern.MatchString(root) {
		return invalid("root %q must be dot-separated tokens of a-z, 0-9 and _", root)
	}
	return nil
}

func ValidStream(name string) error {
	if !streamPattern.MatchString(name) {
		return invalid("stream %q must match %s", name, streamPattern)
	}
	return nil
}

func ValidKind(kind string) error {
	if !token(kind) {
		return invalid("kind %q must be one subject token", kind)
	}
	return nil
}

func Subject(root, tenant string) (string, error) {
	if err := ValidRoot(root); err != nil {
		return "", err
	}
	if !token(tenant) {
		return "", invalid("tenant %q must be one subject token", tenant)
	}
	return root + "." + tenant, nil
}

func Subjects(root string) string { return root + ".>" }

func Pages(users []string, size int) [][]string {
	if len(users) == 0 {
		return nil
	}
	sorted := slices.Sorted(slices.Values(users))
	if size < 1 {
		size = len(sorted)
	}
	return slices.Collect(slices.Chunk(sorted, size))
}

func PageID(notificationID string, part int, users []string) string {
	if !slices.IsSorted(users) {
		users = slices.Sorted(slices.Values(users))
	}
	sum := sha256.Sum256([]byte(strings.Join(users, "\n")))
	return notificationID + "-p" + strconv.Itoa(part) + "-" + hex.EncodeToString(sum[:])[:pageHashChars]
}

func token(s string) bool { return s != "" && !strings.ContainsAny(s, ".*> \t\r\n") }

func invalid(format string, args ...any) error {
	return fmt.Errorf("%w: ws publish "+format, append([]any{apperr.ErrInvalidArgument}, args...)...)
}
