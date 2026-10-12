package domain_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/pkg/apperr"
)

func TestIdentValidators(t *testing.T) {
	type validator struct {
		name  string
		fn    func(string) error
		field string
	}
	tenant := validator{"tenant", domain.ValidTenant, "tenant"}
	user := validator{"user", domain.ValidUser, "user"}
	cid := validator{"cid", domain.ValidCID, "cid"}

	tests := []struct {
		v     validator
		input string
		ok    bool
	}{
		{tenant, "acme", true},
		{tenant, "a", true},
		{tenant, "shop_01-vn", true},
		{tenant, strings.Repeat("a", 32), true},
		{tenant, "", false},
		{tenant, strings.Repeat("a", 33), false},
		{tenant, "Acme", false},
		{tenant, "acme.vn", false},
		{tenant, "acme*", false},
		{tenant, "acme>", false},
		{tenant, "ac me", false},
		{tenant, "acme\n", false},
		{tenant, "tên", false},
		{user, "u1", true},
		{user, "User_42-X", true},
		{user, strings.Repeat("Z", 64), true},
		{user, "", false},
		{user, strings.Repeat("Z", 65), false},
		{user, "user.name", false},
		{user, "user@x", false},
		{user, "u\x00", false},
		{user, "người", false},
		{cid, "c-1", true},
		{cid, "01J9ZQ4T8K5X_abc", true},
		{cid, strings.Repeat("9", 64), true},
		{cid, "", false},
		{cid, strings.Repeat("9", 65), false},
		{cid, "c.1", false},
		{cid, "c/1", false},
	}
	for _, tt := range tests {
		t.Run(tt.v.name+"/"+tt.input, func(t *testing.T) {
			err := tt.v.fn(tt.input)
			if tt.ok {
				if err != nil {
					t.Fatalf("%s(%q) = %v, want nil", tt.v.name, tt.input, err)
				}
				return
			}
			if !errors.Is(err, apperr.ErrInvalidArgument) {
				t.Fatalf("%s(%q) = %v, want ErrInvalidArgument", tt.v.name, tt.input, err)
			}
			if !strings.Contains(err.Error(), tt.v.field) {
				t.Errorf("%s(%q) error %q does not name field %q", tt.v.name, tt.input, err, tt.v.field)
			}
			if tt.input != "" && strings.Contains(err.Error(), tt.input) {
				t.Errorf("%s(%q) error %q echoes the raw input", tt.v.name, tt.input, err)
			}
		})
	}
}
