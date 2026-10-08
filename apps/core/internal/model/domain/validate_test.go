package domain_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/pkg/apperr"
)

func assertInvalid(t *testing.T, err error, field string) {
	t.Helper()
	if !errors.Is(err, apperr.ErrInvalidArgument) || !strings.HasSuffix(err.Error(), ": "+field) {
		t.Fatalf("err = %v, want ErrInvalidArgument naming %q", err, field)
	}
}

func TestValidateText(t *testing.T) {
	tests := []struct {
		name string
		text string
		ok   bool
	}{
		{"ascii", "hello", true},
		{"vietnamese", "xin chào các bạn", true},
		{"emoji", "ok 👍", true},
		{"padded", "  hi  ", true},
		{"exactly 16384 bytes", strings.Repeat("a", 16384), true},
		{"16384 bytes of multibyte runes", strings.Repeat("ạ", 16384/3) + "a", true},
		{"empty", "", false},
		{"spaces only", "   ", false},
		{"unicode whitespace only", "\t\n 　", false},
		{"16385 bytes", strings.Repeat("a", 16385), false},
		{"multibyte over limit", strings.Repeat("ạ", 16384/3+1), false},
		{"invalid utf-8", "hi \xff", false},
		{"truncated rune", "\xe1\xba", false},
		{"surrogate half", "\xed\xa0\x80", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := domain.ValidateText(tt.text)
			if tt.ok {
				if err != nil {
					t.Fatalf("ValidateText = %v, want nil", err)
				}
				return
			}
			assertInvalid(t, err, "text")
		})
	}
}

func TestValidateThread(t *testing.T) {
	if err := domain.ValidateThread(0); err != nil {
		t.Fatalf("ValidateThread(0) = %v, want nil", err)
	}
	for _, thread := range []uint64{1, 42, 1<<64 - 1} {
		assertInvalid(t, domain.ValidateThread(thread), "thread")
	}
}

func TestPageLimit(t *testing.T) {
	tests := []struct {
		in   int
		want int
		ok   bool
	}{
		{0, 50, true},
		{1, 1, true},
		{50, 50, true},
		{100, 100, true},
		{101, 0, false},
		{-1, 0, false},
		{1 << 30, 0, false},
	}
	for _, tt := range tests {
		got, err := domain.PageLimit(tt.in)
		if tt.ok {
			if err != nil || got != tt.want {
				t.Errorf("PageLimit(%d) = %d, %v; want %d, nil", tt.in, got, err, tt.want)
			}
			continue
		}
		assertInvalid(t, err, "limit")
	}
}
