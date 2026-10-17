package domain_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/ivannguyendev/chatim/apps/mention/internal/domain"
	"github.com/ivannguyendev/chatim/pkg/apperr"
)

func TestTargetName(t *testing.T) {
	cases := map[string]domain.Target{
		"user:minh":      {Kind: domain.TargetUser, ID: "minh"},
		"group:sale-1":   {Kind: domain.TargetGroup, ID: "sale-1"},
		"all:777":        {Kind: domain.TargetAll},
		"":               {Kind: 9, ID: "x"},
		"user:org:a_b-c": {Kind: domain.TargetUser, ID: "org:a_b-c"},
	}
	for want, target := range cases {
		if got := target.Name(777); got != want {
			t.Fatalf("name of %+v = %q, want %q", target, got, want)
		}
	}
}

func TestTargetValidate(t *testing.T) {
	good := []domain.Target{
		{Kind: domain.TargetUser, ID: "Minh"},
		{Kind: domain.TargetGroup, ID: "team:sale_1"},
		{Kind: domain.TargetAll},
	}
	for _, target := range good {
		if err := target.Validate(); err != nil {
			t.Fatalf("validate %+v: %v", target, err)
		}
	}
	bad := []domain.Target{
		{Kind: domain.TargetUser},
		{Kind: domain.TargetUser, ID: "a b"},
		{Kind: domain.TargetGroup, ID: strings.Repeat("g", 65)},
		{Kind: domain.TargetGroup, ID: "sale.team"},
		{Kind: domain.TargetAll, ID: "x"},
		{Kind: 0, ID: "x"},
		{Kind: 4, ID: "x"},
	}
	for _, target := range bad {
		if err := target.Validate(); !errors.Is(err, apperr.ErrInvalidArgument) {
			t.Fatalf("validate %+v = %v, want invalid argument", target, err)
		}
	}
}

func TestTargetIdentityDropsTime(t *testing.T) {
	target := domain.Target{Kind: domain.TargetUser, ID: "minh", At: time.UnixMilli(1)}
	if got := target.Identity(); got != (domain.Target{Kind: domain.TargetUser, ID: "minh"}) {
		t.Fatalf("identity = %+v", got)
	}
}

func TestMessageKeyValidate(t *testing.T) {
	if err := (domain.MessageKey{Room: 777, Thread: 0, Seq: 1}).Validate(); err != nil {
		t.Fatalf("validate key: %v", err)
	}
	bad := []domain.MessageKey{
		{Room: 0, Seq: 1},
		{Room: 1 << 63, Seq: 1},
		{Room: 777, Seq: 0},
	}
	for _, key := range bad {
		if err := key.Validate(); !errors.Is(err, apperr.ErrInvalidArgument) {
			t.Fatalf("validate %+v = %v, want invalid argument", key, err)
		}
	}
}

func TestMemberCursor(t *testing.T) {
	joined := time.UnixMilli(5)
	m := domain.Member{User: "lan", Role: "admin", Priority: 3, JoinedAt: joined, Active: true}
	want := domain.MemberCursor{Role: "admin", Priority: 3, JoinedAt: joined, User: "lan"}
	if got := m.Cursor(); got != want {
		t.Fatalf("cursor = %+v, want %+v", got, want)
	}
}
