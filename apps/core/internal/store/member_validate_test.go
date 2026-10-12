package store_test

import (
	"errors"
	"math"
	"strconv"
	"testing"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/pkg/apperr"
)

var joinAt = time.UnixMilli(1_700_000_000_000).UTC()

func goodJoin() domain.Join {
	return domain.Join{Room: 7, Tenant: "acme", RequestID: "req-1", By: "alice", At: joinAt, ReadSeq: 3}
}

func TestValidateJoin(t *testing.T) {
	if err := store.ValidateJoin(goodJoin(), []string{"bob", "carol"}); err != nil {
		t.Fatalf("ValidateJoin(good) = %v", err)
	}
	full := make([]string, domain.MaxMemberBatch)
	for i := range full {
		full[i] = "u" + strconv.Itoa(i)
	}
	owned := goodJoin()
	owned.Owner = "carol"
	if err := store.ValidateJoin(owned, []string{"bob", "carol"}); err != nil {
		t.Fatalf("ValidateJoin(owner carol) = %v", err)
	}
	if err := store.ValidateJoin(goodJoin(), full); err != nil {
		t.Fatalf("ValidateJoin(%d users) = %v", len(full), err)
	}
	edit := func(f func(*domain.Join)) domain.Join {
		j := goodJoin()
		f(&j)
		return j
	}
	cases := map[string]struct {
		j     domain.Join
		users []string
	}{
		"zero room":      {edit(func(j *domain.Join) { j.Room = 0 }), []string{"bob"}},
		"bad tenant":     {edit(func(j *domain.Join) { j.Tenant = "" }), []string{"bob"}},
		"bad request id": {edit(func(j *domain.Join) { j.RequestID = "a b" }), []string{"bob"}},
		"no request id":  {edit(func(j *domain.Join) { j.RequestID = "" }), []string{"bob"}},
		"bad caller":     {edit(func(j *domain.Join) { j.By = "" }), []string{"bob"}},
		"zero time":      {edit(func(j *domain.Join) { j.At = time.Time{} }), []string{"bob"}},
		"read seq":       {edit(func(j *domain.Join) { j.ReadSeq = math.MaxInt64 + 1 }), []string{"bob"}},
		"no users":       {goodJoin(), nil},
		"too many users": {goodJoin(), append(full, "extra")},
		"bad user":       {goodJoin(), []string{"bob", "b c"}},
		"duplicate user": {goodJoin(), []string{"bob", "carol", "bob"}},
		"owner outside":  {edit(func(j *domain.Join) { j.Owner = "dan" }), []string{"bob"}},
	}
	for name, c := range cases {
		if err := store.ValidateJoin(c.j, c.users); !errors.Is(err, apperr.ErrInvalidArgument) {
			t.Errorf("%s: ValidateJoin = %v, want ErrInvalidArgument", name, err)
		}
	}
}

func activeMember() domain.Member {
	return domain.Member{
		Room: 7, Tenant: "acme", User: "bob", Role: domain.RoleMember, JoinedAt: joinAt, State: domain.MemberActive, Ver: 3,
		RequestID: "req-1", UpdatedAt: joinAt, UpdatedBy: "alice", LastChangeAt: joinAt,
	}
}

func TestValidateMemberChangeAcceptsOneStep(t *testing.T) {
	cur := activeMember()
	at := joinAt.Add(time.Second)
	for name, next := range map[string]domain.Member{
		"role":     cur.Next(domain.RoleAdmin, domain.MemberActive, 0, "req-2", "alice", at),
		"leave":    cur.Next(domain.RoleMember, domain.MemberRemoved, 0, "req-2", "bob", at),
		"priority": cur.Next(domain.RoleMember, domain.MemberActive, -5, "req-2", "alice", at),
	} {
		if err := store.ValidateMemberChange(cur, next); err != nil {
			t.Errorf("%s: ValidateMemberChange = %v", name, err)
		}
	}
	top := cur
	top.Ver = math.MaxUint32
	wrapped := top.Next(domain.RoleAdmin, domain.MemberActive, 0, "req-2", "alice", at)
	step := cur.Next(domain.RoleAdmin, domain.MemberActive, 0, "req-2", "alice", at)
	edit := func(m domain.Member, f func(*domain.Member)) domain.Member {
		f(&m)
		return m
	}
	cases := map[string]struct{ cur, next domain.Member }{
		"other room":     {cur, edit(step, func(m *domain.Member) { m.Room = 8 })},
		"other user":     {cur, edit(step, func(m *domain.Member) { m.User = "carol" })},
		"no current ver": {edit(cur, func(m *domain.Member) { m.Ver = 0 }), edit(step, func(m *domain.Member) { m.Ver = 1 })},
		"same ver":       {cur, edit(step, func(m *domain.Member) { m.Ver = cur.Ver })},
		"skips a ver":    {cur, edit(step, func(m *domain.Member) { m.Ver = cur.Ver + 2 })},
		"ver overflow":   {top, wrapped},
		"state zero":     {cur, edit(step, func(m *domain.Member) { m.State = 0 })},
		"state three":    {cur, edit(step, func(m *domain.Member) { m.State = 3 })},
		"bad role":       {cur, edit(step, func(m *domain.Member) { m.Role = "guest" })},
		"bad request id": {cur, edit(step, func(m *domain.Member) { m.RequestID = "" })},
		"bad caller":     {cur, edit(step, func(m *domain.Member) { m.UpdatedBy = "a b" })},
	}
	for name, c := range cases {
		if err := store.ValidateMemberChange(c.cur, c.next); !errors.Is(err, apperr.ErrInvalidArgument) {
			t.Errorf("%s: ValidateMemberChange = %v, want ErrInvalidArgument", name, err)
		}
	}
}

func TestValidateOwnerWrites(t *testing.T) {
	bob, carol := activeMember(), activeMember()
	carol.User = "carol"
	at := joinAt.Add(time.Second)
	up := store.MemberWrite{Cur: carol, Next: carol.Next(domain.RoleOwner, domain.MemberActive, 0, "req-2", "bob", at)}
	out := store.MemberWrite{Cur: bob, Next: bob.Next(domain.RoleMember, domain.MemberRemoved, 0, "req-2", "bob", at)}
	if err := store.ValidateOwnerWrites(7, []store.MemberWrite{up, out}); err != nil {
		t.Fatalf("ValidateOwnerWrites(two) = %v", err)
	}
	stale := out
	stale.Next.Ver = bob.Ver
	for name, ws := range map[string][]store.MemberWrite{
		"none":         nil,
		"three":        {up, out, out},
		"same user":    {out, out},
		"other room":   {up},
		"invalid step": {stale},
	} {
		room := uint64(7)
		if name == "other room" {
			room = 8
		}
		if err := store.ValidateOwnerWrites(room, ws); !errors.Is(err, apperr.ErrInvalidArgument) {
			t.Errorf("%s: ValidateOwnerWrites = %v, want ErrInvalidArgument", name, err)
		}
	}
}

func TestMemberCountDeltaCountsStateMoves(t *testing.T) {
	bob := activeMember()
	at := joinAt.Add(time.Second)
	left := bob.Next(domain.RoleMember, domain.MemberRemoved, 0, "req-2", "bob", at)
	writes := []store.MemberWrite{
		{Cur: bob, Next: left},
		{Cur: bob, Next: bob.Next(domain.RoleOwner, domain.MemberActive, 0, "req-2", "bob", at)},
	}
	if got := store.MemberCountDelta(writes); got != -1 {
		t.Fatalf("MemberCountDelta = %d, want -1", got)
	}
	back := []store.MemberWrite{{Cur: left, Next: left.Next(domain.RoleMember, domain.MemberActive, 0, "req-3", "bob", at)}}
	if got := store.MemberCountDelta(back); got != 1 {
		t.Fatalf("MemberCountDelta(re-add) = %d, want 1", got)
	}
}

func TestValidateMemberDeltaAndCount(t *testing.T) {
	if store.ValidateMemberDelta(-1) != nil || store.ValidateMemberDelta(3) != nil || store.ValidateMemberCount(0) != nil {
		t.Fatal("valid delta or count rejected")
	}
	if !errors.Is(store.ValidateMemberDelta(0), apperr.ErrInvalidArgument) || !errors.Is(store.ValidateMemberCount(-1), apperr.ErrInvalidArgument) {
		t.Fatal("zero delta or negative count accepted")
	}
}

func TestValidateReadSeq(t *testing.T) {
	for _, seq := range []uint64{0, 1, math.MaxInt64} {
		if err := store.ValidateReadSeq(seq); err != nil {
			t.Errorf("ValidateReadSeq(%d) = %v", seq, err)
		}
	}
	if err := store.ValidateReadSeq(math.MaxInt64 + 1); !errors.Is(err, apperr.ErrInvalidArgument) {
		t.Fatalf("ValidateReadSeq(MaxInt64+1) = %v, want ErrInvalidArgument", err)
	}
}

func TestCreationMemberIsActiveAtVerOne(t *testing.T) {
	room := domain.Room{ID: 7, Tenant: "acme", CreatedBy: "alice", CreatedAt: joinAt}
	bare := domain.Member{Room: 7, Tenant: "acme", User: "bob", Role: domain.RoleMember, JoinedAt: joinAt}
	want := bare
	want.State, want.Ver, want.RequestID = domain.MemberActive, 1, "7-created"
	want.UpdatedAt, want.UpdatedBy, want.LastChangeAt = joinAt, "alice", joinAt
	if got := store.CreationMember(room, bare); got != want {
		t.Fatalf("CreationMember(bare) = %+v, want %+v", got, want)
	}
	later := joinAt.Add(time.Minute)
	full := bare
	full.State, full.Ver, full.RequestID, full.Priority = domain.MemberRemoved, 9, "other", 4
	full.UpdatedAt, full.UpdatedBy, full.LastChangeAt = later, "carol", later.Add(time.Second)
	want = full
	want.State, want.Ver, want.RequestID = domain.MemberActive, 1, "7-created"
	if got := store.CreationMember(room, full); got != want {
		t.Fatalf("CreationMember(full) = %+v, want %+v", got, want)
	}
}

func TestMemberChangeKindsKeepTheirWireNumbers(t *testing.T) {
	got := []store.ChangeKind{store.MemberChanged, store.ReadChanged, store.MessageHidden, store.HistoryCleared, store.MemberCountCheck}
	for i, k := range got {
		if want := store.ChangeKind(6 + i); k != want {
			t.Errorf("kind %d = %d, want %d", i, k, want)
		}
	}
}
