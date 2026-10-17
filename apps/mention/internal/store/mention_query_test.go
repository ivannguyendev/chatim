package store_test

import (
	"errors"
	"slices"
	"testing"

	"github.com/ivannguyendev/chatim/apps/mention/internal/domain"
	"github.com/ivannguyendev/chatim/apps/mention/internal/store"
	"github.com/ivannguyendev/chatim/pkg/apperr"
)

func TestValidateTargetMentionQuery(t *testing.T) {
	good := store.TargetMentionQuery{Tenant: "acme", Target: "user:minh", Limit: 20}
	if err := store.ValidateTargetMentionQuery(good); err != nil {
		t.Fatalf("ValidateTargetMentionQuery(good) = %v", err)
	}
	for name, edit := range map[string]func(*store.TargetMentionQuery){
		"no tenant":       func(q *store.TargetMentionQuery) { q.Tenant = "" },
		"bad tenant":      func(q *store.TargetMentionQuery) { q.Tenant = "Acme Co" },
		"no target":       func(q *store.TargetMentionQuery) { q.Target = "" },
		"zero limit":      func(q *store.TargetMentionQuery) { q.Limit = 0 },
		"limit over page": func(q *store.TargetMentionQuery) { q.Limit = store.MaxPageLimit + 1 },
		"cursor no key":   func(q *store.TargetMentionQuery) { q.Before = store.MentionCursor{At: at(1)} },
	} {
		q := good
		edit(&q)
		if err := store.ValidateTargetMentionQuery(q); !errors.Is(err, apperr.ErrInvalidArgument) {
			t.Errorf("%s: ValidateTargetMentionQuery = %v, want ErrInvalidArgument", name, err)
		}
	}
}

func TestValidateSharedMentionQuery(t *testing.T) {
	good := store.SharedMentionQuery{Tenant: "acme", Rooms: []store.SharedRoom{{Room: 1}, {Room: 2, Since: at(1)}}, Groups: []string{"ops"}, Limit: 20}
	if err := store.ValidateSharedMentionQuery(good); err != nil {
		t.Fatalf("ValidateSharedMentionQuery(good) = %v", err)
	}
	full := make([]store.SharedRoom, store.SharedMentionBatch)
	for i := range full {
		full[i].Room = uint64(i + 1)
	}
	if err := store.ValidateSharedMentionQuery(store.SharedMentionQuery{Tenant: "acme", Rooms: full, Limit: 20}); err != nil {
		t.Fatalf("ValidateSharedMentionQuery(full batch) = %v", err)
	}
	for name, edit := range map[string]func(*store.SharedMentionQuery){
		"no tenant":      func(q *store.SharedMentionQuery) { q.Tenant = "" },
		"no rooms":       func(q *store.SharedMentionQuery) { q.Rooms = nil },
		"over a batch":   func(q *store.SharedMentionQuery) { q.Rooms = slices.Concat(full, []store.SharedRoom{{Room: 9999}}) },
		"room 0":         func(q *store.SharedMentionQuery) { q.Rooms = []store.SharedRoom{{}} },
		"duplicate room": func(q *store.SharedMentionQuery) { q.Rooms = []store.SharedRoom{{Room: 1}, {Room: 1}} },
		"bad group":      func(q *store.SharedMentionQuery) { q.Groups = []string{"Bad Id"} },
		"zero limit":     func(q *store.SharedMentionQuery) { q.Limit = 0 },
		"cursor no key":  func(q *store.SharedMentionQuery) { q.Before = store.MentionCursor{At: at(1)} },
	} {
		q := good
		edit(&q)
		if err := store.ValidateSharedMentionQuery(q); !errors.Is(err, apperr.ErrInvalidArgument) {
			t.Errorf("%s: ValidateSharedMentionQuery = %v, want ErrInvalidArgument", name, err)
		}
	}
}

func TestTargetMentionQueryWants(t *testing.T) {
	q := store.TargetMentionQuery{Tenant: "acme", Target: "user:minh", Before: store.CursorOf(hit(2, 9, 90, minh)), Limit: 5}
	foreign := hit(1, 1, 10, minh)
	foreign.Tenant = "globex"
	retired := hit(1, 2, 10, minh)
	retired.State = domain.StateRetired
	cases := map[string]struct {
		m    domain.Mention
		want bool
	}{
		"live target":    {hit(1, 3, 10, minh), true},
		"another tenant": {foreign, false},
		"retired":        {retired, false},
		"another target": {hit(1, 4, 10, ops), false},
		"at the cursor":  {hit(2, 9, 90, minh), false},
		"newer":          {hit(2, 10, 91, minh), false},
	}
	for name, c := range cases {
		if got := q.Wants(c.m); got != c.want {
			t.Errorf("%s: Wants = %v, want %v", name, got, c.want)
		}
	}
}
