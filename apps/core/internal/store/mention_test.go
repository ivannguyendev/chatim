package store_test

import (
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/pkg/apperr"
)

var (
	minh  = domain.MentionTarget{Kind: domain.MentionUser, ID: "minh"}
	lan   = domain.MentionTarget{Kind: domain.MentionUser, ID: "lan"}
	ops   = domain.MentionTarget{Kind: domain.MentionGroup, ID: "ops"}
	every = domain.MentionTarget{Kind: domain.MentionAll}
)

func mentionSet(ver uint32, targets ...domain.MentionTarget) store.MentionSet {
	at := time.UnixMilli(1_700_000_000_000).UTC()
	return store.MentionSet{Key: store.MsgKey{Room: 7, Seq: 3}, Tenant: "acme", Sender: "alice", Ver: ver, Targets: targets, CreatedAt: at, At: at}
}

func stored(t domain.MentionTarget, live bool, ver uint32) domain.Mention {
	return domain.Mention{Key: domain.MsgKey{Room: 7, Seq: 3}, Target: t, Live: live, Ver: ver}
}

func TestValidateMentionSet(t *testing.T) {
	for name, s := range map[string]store.MentionSet{"targets": mentionSet(0, minh, ops, every), "none": mentionSet(4)} {
		if err := store.ValidateMentionSet(s); err != nil {
			t.Errorf("%s: ValidateMentionSet = %v, want nil", name, err)
		}
	}
	bad := map[string]func(*store.MentionSet){
		"no key":       func(s *store.MentionSet) { s.Key = store.MsgKey{} },
		"no tenant":    func(s *store.MentionSet) { s.Tenant = "" },
		"bad sender":   func(s *store.MentionSet) { s.Sender = "" },
		"no time":      func(s *store.MentionSet) { s.At = time.Time{} },
		"no sent time": func(s *store.MentionSet) { s.CreatedAt = time.Time{} },
		"duplicate":    func(s *store.MentionSet) { s.Targets = append(s.Targets, minh) },
		"all with id": func(s *store.MentionSet) {
			s.Targets = append(s.Targets, domain.MentionTarget{Kind: domain.MentionAll, ID: "x"})
		},
		"long id": func(s *store.MentionSet) { s.Targets[0].ID = strings.Repeat("a", 65) },
		"kind 0":  func(s *store.MentionSet) { s.Targets[0].Kind = 0 },
	}
	for name, edit := range bad {
		s := mentionSet(1, minh)
		edit(&s)
		if err := store.ValidateMentionSet(s); !errors.Is(err, apperr.ErrInvalidArgument) {
			t.Errorf("%s: ValidateMentionSet = %v, want ErrInvalidArgument", name, err)
		}
	}
}

func TestPlanMentions(t *testing.T) {
	cases := map[string]struct {
		stored     []domain.Mention
		set        store.MentionSet
		live, gone []domain.MentionTarget
	}{
		"new message":          {nil, mentionSet(0, minh, every), []domain.MentionTarget{minh, every}, nil},
		"same version again":   {[]domain.Mention{stored(minh, true, 0)}, mentionSet(0, minh), nil, nil},
		"edit swaps a target":  {[]domain.Mention{stored(minh, true, 0), stored(lan, true, 0)}, mentionSet(2, minh, ops), []domain.MentionTarget{minh, ops}, []domain.MentionTarget{lan}},
		"delete retires all":   {[]domain.Mention{stored(minh, true, 1), stored(lan, false, 1)}, mentionSet(3), nil, []domain.MentionTarget{minh, lan}},
		"retired stays put":    {[]domain.Mention{stored(lan, false, 3)}, mentionSet(3), nil, nil},
		"a target comes back":  {[]domain.Mention{stored(lan, false, 1)}, mentionSet(2, lan), []domain.MentionTarget{lan}, nil},
		"older version skips":  {[]domain.Mention{stored(minh, true, 2), stored(lan, false, 2)}, mentionSet(1, lan), nil, nil},
		"one newer doc blocks": {[]domain.Mention{stored(minh, true, 0), stored(lan, false, 5)}, mentionSet(4, ops), nil, nil},
	}
	for name, c := range cases {
		p := store.PlanMentions(c.stored, c.set)
		if !slices.Equal(p.Live, c.live) || !slices.Equal(p.Retire, c.gone) {
			t.Errorf("%s: plan live %v retire %v, want live %v retire %v", name, p.Live, p.Retire, c.live, c.gone)
		}
	}
}
