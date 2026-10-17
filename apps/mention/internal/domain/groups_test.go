package domain_test

import (
	"errors"
	"math"
	"slices"
	"testing"

	"github.com/ivannguyendev/chatim/apps/mention/internal/domain"
	"github.com/ivannguyendev/chatim/pkg/apperr"
)

func TestNormalizeGroups(t *testing.T) {
	in := []string{"team-sale", "manager", "team-sale", "a"}
	got := domain.NormalizeGroups(in)
	if want := []string{"a", "manager", "team-sale"}; !slices.Equal(got, want) {
		t.Fatalf("normalize = %v, want %v", got, want)
	}
	if !slices.Equal(in, []string{"team-sale", "manager", "team-sale", "a"}) {
		t.Fatalf("input changed: %v", in)
	}
	if got := domain.NormalizeGroups(nil); got == nil || len(got) != 0 {
		t.Fatalf("normalize nil = %#v, want empty non-nil", got)
	}
}

func TestUserGroupsValidate(t *testing.T) {
	good := []domain.UserGroups{
		{Tenant: "t1", User: "Ha", Groups: []string{"manager", "team:sale"}, Ver: 1003},
		{Tenant: "t1", User: "Ha", Groups: nil, Ver: 1},
	}
	for _, u := range good {
		if err := u.Validate(); err != nil {
			t.Fatalf("validate %+v: %v", u, err)
		}
	}
	bad := map[string]domain.UserGroups{
		"tenant":  {Tenant: "T1", User: "Ha", Ver: 1},
		"user":    {Tenant: "t1", User: "H a", Ver: 1},
		"ver":     {Tenant: "t1", User: "Ha", Ver: 0},
		"big ver": {Tenant: "t1", User: "Ha", Ver: math.MaxInt64 + 1},
		"group":   {Tenant: "t1", User: "Ha", Groups: []string{"ok", "not ok"}, Ver: 1},
		"empty":   {Tenant: "t1", User: "Ha", Groups: []string{""}, Ver: 1},
		"no user": {Tenant: "t1", Ver: 1},
	}
	for name, u := range bad {
		if err := u.Validate(); !errors.Is(err, apperr.ErrInvalidArgument) {
			t.Fatalf("%s: validate = %v, want invalid argument", name, err)
		}
	}
}

func TestGroupValidate(t *testing.T) {
	if err := (domain.Group{Tenant: "t1", ID: "team-sale", Name: "Phòng Kinh doanh", Active: true, Ver: 50}).Validate(); err != nil {
		t.Fatalf("validate group: %v", err)
	}
	bad := map[string]domain.Group{
		"tenant": {Tenant: "", ID: "g", Name: "G", Ver: 1},
		"id":     {Tenant: "t1", ID: "g.h", Name: "G", Ver: 1},
		"name":   {Tenant: "t1", ID: "g", Name: "  ", Ver: 1},
		"ver":    {Tenant: "t1", ID: "g", Name: "G", Ver: 0},
		"big":    {Tenant: "t1", ID: "g", Name: "G", Ver: math.MaxInt64 + 1},
	}
	for name, g := range bad {
		if err := g.Validate(); !errors.Is(err, apperr.ErrInvalidArgument) {
			t.Fatalf("%s: validate = %v, want invalid argument", name, err)
		}
	}
}
