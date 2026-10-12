package store_test

import (
	"errors"
	"math"
	"strings"
	"testing"

	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/pkg/apperr"
)

func TestEditErrorsWrapAppKinds(t *testing.T) {
	if !errors.Is(store.ErrEditExists, apperr.ErrAlreadyExists) || !errors.Is(store.ErrEditNotFound, apperr.ErrNotFound) {
		t.Fatalf("ErrEditExists = %v, ErrEditNotFound = %v; want already exists and not found", store.ErrEditExists, store.ErrEditNotFound)
	}
}

func TestEditKeyOf(t *testing.T) {
	e := domain.Edit{Room: 42, Thread: 7, Seq: 3, Version: 2, Text: "hi"}
	if got, want := store.EditKeyOf(e), (store.MsgKey{Room: 42, Thread: 7, Seq: 3}); got != want {
		t.Fatalf("EditKeyOf = %+v, want %+v", got, want)
	}
}

func TestValidateEdit(t *testing.T) {
	good := domain.Edit{Room: 1, Seq: 1, Version: 1, Kind: domain.EditText}
	tests := []struct {
		name   string
		mutate func(*domain.Edit)
		field  string
	}{
		{"text", func(*domain.Edit) {}, ""},
		{"delete", func(e *domain.Edit) { e.Kind = domain.EditDelete }, ""},
		{"max int32 version", func(e *domain.Edit) { e.Version = math.MaxInt32 }, ""},
		{"zero room", func(e *domain.Edit) { e.Room = 0 }, "room"},
		{"zero seq", func(e *domain.Edit) { e.Seq = 0 }, "seq"},
		{"version above max int32", func(e *domain.Edit) { e.Version = math.MaxInt32 + 1 }, "version"},
		{"zero kind", func(e *domain.Edit) { e.Kind = 0 }, "edit kind"},
		{"kind past original", func(e *domain.Edit) { e.Kind = domain.EditOriginal + 1 }, "edit kind"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := good
			tt.mutate(&e)
			err := store.ValidateEdit(e)
			switch {
			case tt.field == "" && err != nil:
				t.Fatalf("ValidateEdit = %v, want nil", err)
			case tt.field != "" && (!errors.Is(err, apperr.ErrInvalidArgument) || !strings.HasSuffix(err.Error(), ": "+tt.field)):
				t.Fatalf("ValidateEdit = %v, want ErrInvalidArgument naming %q", err, tt.field)
			}
		})
	}
}

func TestValidateEditTiesVerZeroToTheOriginalKind(t *testing.T) {
	cases := []struct {
		version uint32
		kind    domain.EditKind
		ok      bool
	}{
		{0, domain.EditOriginal, true},
		{0, domain.EditText, false},
		{0, domain.EditDelete, false},
		{1, domain.EditOriginal, false},
		{7, domain.EditOriginal, false},
		{1, domain.EditText, true},
		{2, domain.EditDelete, true},
	}
	for _, c := range cases {
		err := store.ValidateEdit(domain.Edit{Room: 1, Seq: 1, Version: c.version, Kind: c.kind, Text: "hi"})
		if c.ok != (err == nil) || (!c.ok && !errors.Is(err, apperr.ErrInvalidArgument)) {
			t.Errorf("ValidateEdit(v%d, kind %d) = %v, want ok=%v", c.version, c.kind, err, c.ok)
		}
	}
}

func TestValidateLimit(t *testing.T) {
	for limit, ok := range map[int]bool{-1: false, 0: false, 1: true, 100: true, 101: false} {
		err := store.ValidateLimit(limit, 100)
		if ok != (err == nil) || (!ok && !errors.Is(err, apperr.ErrInvalidArgument)) {
			t.Errorf("ValidateLimit(%d, 100) = %v, want ok=%v", limit, err, ok)
		}
	}
}
