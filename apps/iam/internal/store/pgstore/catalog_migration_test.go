package pgstore

import (
	"io/fs"
	"regexp"
	"slices"
	"testing"

	"github.com/ivannguyendev/chatim/apps/iam/internal/authz"
)

var quotedIAMPermission = regexp.MustCompile(`'(iam:[^']*)'`)

func TestMigrationCatalogMatchesTheCode(t *testing.T) {
	body, err := fs.ReadFile(migrationFiles, "migrations/0004_roles.sql")
	if err != nil {
		t.Fatalf("read migration: %v", err)
	}
	var inSQL []string
	for _, m := range quotedIAMPermission.FindAllStringSubmatch(string(body), -1) {
		inSQL = append(inSQL, m[1])
	}
	slices.Sort(inSQL)
	inCode := slices.Clone(authz.IAMPermissions)
	slices.Sort(inCode)
	if !slices.Equal(inSQL, inCode) {
		t.Fatalf("catalog in 0004_roles.sql = %v; want %v", inSQL, inCode)
	}
}
