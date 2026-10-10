package store_test

import (
	"errors"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/pkg/apperr"
	"github.com/ivannguyendev/chatim/pkg/keys"
)

func TestReactionAndPinErrorsWrapAppKinds(t *testing.T) {
	for _, c := range []struct{ err, kind error }{
		{store.ErrStaleRead, apperr.ErrUnavailable},
		{store.ErrPinExists, apperr.ErrAlreadyExists},
		{store.ErrPinNotFound, apperr.ErrNotFound},
	} {
		if !errors.Is(c.err, c.kind) {
			t.Errorf("%v does not wrap %v", c.err, c.kind)
		}
	}
}

func TestChangeKindsOnlyGrowAtTheEnd(t *testing.T) {
	kinds := []store.ChangeKind{store.MessageInserted, store.RoomInserted, store.EditInserted, store.ReactionChanged, store.PinInserted}
	for i, k := range kinds {
		if int(k) != i+1 {
			t.Fatalf("change kind %d = %d, want %d: kinds are stored in work records", i, k, i+1)
		}
	}
}

func TestReactionKeyOf(t *testing.T) {
	r := domain.Reaction{Room: 42, Thread: 7, Seq: 3, User: "bob", Emoji: "👍"}
	if got, want := store.ReactionKeyOf(r), (store.MsgKey{Room: 42, Thread: 7, Seq: 3}); got != want {
		t.Fatalf("ReactionKeyOf = %+v, want %+v", got, want)
	}
}

func assertField(t *testing.T, op string, err error, field string) {
	t.Helper()
	switch {
	case field == "" && err != nil:
		t.Fatalf("%s = %v, want nil", op, err)
	case field != "" && (!errors.Is(err, apperr.ErrInvalidArgument) || !strings.HasSuffix(err.Error(), ": "+field)):
		t.Fatalf("%s = %v, want ErrInvalidArgument naming %q", op, err, field)
	}
}

func TestValidateReaction(t *testing.T) {
	good := domain.Reaction{Room: 1, Seq: 1, Tenant: "acme", User: "bob", Emoji: "👍"}
	for name, c := range map[string]struct {
		mutate func(*domain.Reaction)
		field  string
	}{
		"valid":                           {func(*domain.Reaction) {}, ""},
		"empty emoji is not its business": {func(r *domain.Reaction) { r.Emoji = "" }, ""},
		"zero room":                       {func(r *domain.Reaction) { r.Room = 0 }, "room"},
		"zero seq":                        {func(r *domain.Reaction) { r.Seq = 0 }, "seq"},
		"empty user":                      {func(r *domain.Reaction) { r.User = "" }, "user"},
		"user with a dot":                 {func(r *domain.Reaction) { r.User = "b.b" }, "user"},
		"empty tenant":                    {func(r *domain.Reaction) { r.Tenant = "" }, "tenant"},
	} {
		r := good
		c.mutate(&r)
		assertField(t, "ValidateReaction("+name+")", store.ValidateReaction(r), c.field)
	}
	assertField(t, "ValidateReactionTarget(ok)", store.ValidateReactionTarget(store.MsgKey{Room: 1, Seq: 1}, "bob"), "")
	assertField(t, "ValidateReactionTarget(zero seq)", store.ValidateReactionTarget(store.MsgKey{Room: 1}, "bob"), "seq")
	assertField(t, "ValidateReactionTarget(bad user)", store.ValidateReactionTarget(store.MsgKey{Room: 1, Seq: 1}, "b b"), "user")
}

func TestValidateBookmarkAndReply(t *testing.T) {
	at := time.UnixMilli(1)
	b := domain.Bookmark{Room: 1, Seq: 1, Tenant: "acme", User: "bob", On: true, At: at}
	assertField(t, "ValidateBookmark(ok)", store.ValidateBookmark(b), "")
	b.At = time.Time{}
	assertField(t, "ValidateBookmark(zero time)", store.ValidateBookmark(b), "time")
	r := domain.Reply{Parent: domain.MsgKey{Room: 1, Seq: 1}, Room: 1, Seq: 2, Tenant: "acme", From: "bob", At: at}
	for name, c := range map[string]struct {
		mutate func(*domain.Reply)
		field  string
	}{
		"valid":           {func(*domain.Reply) {}, ""},
		"zero parent":     {func(r *domain.Reply) { r.Parent.Seq = 0 }, "parent"},
		"zero reply seq":  {func(r *domain.Reply) { r.Seq = 0 }, "reply"},
		"another room":    {func(r *domain.Reply) { r.Room = 2 }, "reply room"},
		"itself":          {func(r *domain.Reply) { r.Seq = 1 }, "reply"},
		"empty tenant":    {func(r *domain.Reply) { r.Tenant = "" }, "tenant"},
		"user with a dot": {func(r *domain.Reply) { r.From = "b.b" }, "user"},
		"zero time":       {func(r *domain.Reply) { r.At = time.Time{} }, "time"},
	} {
		x := r
		c.mutate(&x)
		assertField(t, "ValidateReply("+name+")", store.ValidateReply(x), c.field)
	}
	assertField(t, "ValidateReplyTarget(ok)", store.ValidateReplyTarget(store.MsgKey{Room: 1, Seq: 1}, store.MsgKey{Room: 1, Seq: 2}), "")
	assertField(t, "ValidateReplyTarget(another room)", store.ValidateReplyTarget(store.MsgKey{Room: 1, Seq: 1}, store.MsgKey{Room: 2, Seq: 2}), "reply")
	assertField(t, "ValidateBookmarkQuery(ok)", store.ValidateBookmarkQuery("acme", "bob", store.MaxPageLimit), "")
	assertField(t, "ValidateBookmarkQuery(no tenant)", store.ValidateBookmarkQuery("", "bob", 1), "tenant")
	assertField(t, "ValidateBookmarkQuery(limit)", store.ValidateBookmarkQuery("acme", "bob", 0), "limit")
	for kind, field := range map[keys.InteractionKind]string{keys.ReactionKind: "", keys.BookmarkKind: "", keys.ReplyKind: "", 0: "interaction kind", 4: "interaction kind"} {
		assertField(t, "ValidateInteractionKind", store.ValidateInteractionKind(kind), field)
	}
}

func TestValidatePinAction(t *testing.T) {
	good := domain.PinAction{Room: 1, PV: 1, Op: domain.PinOpPin, Seq: 1, By: "bob"}
	for name, c := range map[string]struct {
		mutate func(*domain.PinAction)
		field  string
	}{
		"pin":                     {func(*domain.PinAction) {}, ""},
		"unpin":                   {func(a *domain.PinAction) { a.Op = domain.PinOpUnpin }, ""},
		"max int64 version":       {func(a *domain.PinAction) { a.PV = math.MaxInt64 }, ""},
		"zero room":               {func(a *domain.PinAction) { a.Room = 0 }, "room"},
		"zero version":            {func(a *domain.PinAction) { a.PV = 0 }, "pin version"},
		"version above max int64": {func(a *domain.PinAction) { a.PV = math.MaxInt64 + 1 }, "pin version"},
		"zero op":                 {func(a *domain.PinAction) { a.Op = 0 }, "pin op"},
		"op past unpin":           {func(a *domain.PinAction) { a.Op = domain.PinOpUnpin + 1 }, "pin op"},
		"zero seq":                {func(a *domain.PinAction) { a.Seq = 0 }, "seq"},
		"bad user":                {func(a *domain.PinAction) { a.By = "b.b" }, "user"},
	} {
		a := good
		c.mutate(&a)
		assertField(t, "ValidatePinAction("+name+")", store.ValidatePinAction(a), c.field)
	}
}

func TestValidateVersionBump(t *testing.T) {
	for _, c := range []struct {
		base, next uint64
		ok         bool
	}{
		{0, 1, true}, {1, 2, true}, {5, math.MaxInt64, true},
		{0, 0, false}, {2, 2, false}, {3, 2, false}, {0, math.MaxInt64 + 1, false},
	} {
		field := "version"
		if c.ok {
			field = ""
		}
		assertField(t, "ValidateVersionBump", store.ValidateVersionBump(c.base, c.next), field)
	}
}
