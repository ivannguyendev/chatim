package store_test

import (
	"errors"
	"slices"
	"testing"

	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/pkg/apperr"
)

func TestValidateTargetMentionQuery(t *testing.T) {
	good := store.TargetMentionQuery{Tenant: "acme", Target: "user:minh", Limit: 20}
	if err := store.ValidateTargetMentionQuery(good); err != nil {
		t.Fatalf("ValidateTargetMentionQuery(good) = %v", err)
	}
	for name, edit := range map[string]func(*store.TargetMentionQuery){
		"no tenant":       func(q *store.TargetMentionQuery) { q.Tenant = "" },
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
	good := store.SharedMentionQuery{Rooms: []store.SharedRoom{{Room: 1}, {Room: 2, Since: at(1)}}, Groups: []string{"ops"}, Limit: 20}
	if err := store.ValidateSharedMentionQuery(good); err != nil {
		t.Fatalf("ValidateSharedMentionQuery(good) = %v", err)
	}
	full := make([]store.SharedRoom, store.SharedMentionBatch)
	for i := range full {
		full[i].Room = uint64(i + 1)
	}
	if err := store.ValidateSharedMentionQuery(store.SharedMentionQuery{Rooms: full, Limit: 20}); err != nil {
		t.Fatalf("ValidateSharedMentionQuery(full batch) = %v", err)
	}
	for name, edit := range map[string]func(*store.SharedMentionQuery){
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

func TestValidateSentTimeKeys(t *testing.T) {
	if err := store.ValidateSentTimeKeys([]store.MsgKey{{Room: 1, Seq: 1}, {Room: 2, Seq: 5}}); err != nil {
		t.Fatalf("ValidateSentTimeKeys(good) = %v", err)
	}
	many := make([]store.MsgKey, store.SharedMentionBatch+1)
	for i := range many {
		many[i] = store.MsgKey{Room: uint64(i + 1), Seq: 1}
	}
	for name, ks := range map[string][]store.MsgKey{"too many": many, "seq 0": {{Room: 1}}, "room 0": {{Seq: 1}}} {
		if err := store.ValidateSentTimeKeys(ks); !errors.Is(err, apperr.ErrInvalidArgument) {
			t.Errorf("%s: ValidateSentTimeKeys = %v, want ErrInvalidArgument", name, err)
		}
	}
}
