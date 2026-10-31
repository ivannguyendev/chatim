package directory_test

import (
	"encoding/base64"
	"errors"
	"math"
	"strings"
	"testing"

	"github.com/ivannguyendev/chatim/apps/iam/internal/directory"
	"github.com/ivannguyendev/chatim/apps/iam/internal/store"
	"github.com/ivannguyendev/chatim/pkg/apperr"
)

func TestACursorOfExactly1024CharactersDecodes(t *testing.T) {
	p := store.Position{Xid: 1, Kind: 1, Tenant: "t", ID: strings.Repeat("x", 757)}
	cursor := directory.EncodePosition(p)
	if len(cursor) != 1024 {
		t.Fatalf("cursor length = %d; want 1024", len(cursor))
	}
	if got, err := directory.DecodePosition(cursor); err != nil || got != p {
		t.Fatalf("DecodePosition = %+v, %v; want %+v", got, err, p)
	}
}

func TestPositionsRoundTrip(t *testing.T) {
	positions := []store.Position{
		{Xid: 1, Kind: store.ChangeUser, Tenant: "shop", ID: "a2aaaaaaaaaaaaaaaaaaaaaaaa"},
		{Xid: math.MaxUint64, Kind: store.ChangeGroup, Tenant: "t", ID: "sales:north"},
		{Xid: 742, Kind: 0, Tenant: "", ID: ""},
		{Xid: 9, Kind: store.ChangeUser, Tenant: "", ID: "only-id"},
		{Xid: 9, Kind: store.ChangeUser, Tenant: "only-tenant", ID: ""},
		{Xid: 9, Kind: store.ChangeUser, Tenant: "nguyễn", ID: strings.Repeat("x", 300)},
	}
	for _, p := range positions {
		cursor := directory.EncodePosition(p)
		got, err := directory.DecodePosition(cursor)
		if err != nil {
			t.Fatalf("DecodePosition(%q) for %+v: %v", cursor, p, err)
		}
		if got != p {
			t.Fatalf("round trip = %+v; want %+v", got, p)
		}
		if strings.ContainsAny(cursor, "=+/") {
			t.Fatalf("cursor %q is not unpadded base64url", cursor)
		}
	}
}

func TestEmptyCursorIsTheStart(t *testing.T) {
	got, err := directory.DecodePosition("")
	if err != nil {
		t.Fatalf("DecodePosition(\"\"): %v", err)
	}
	if got != (store.Position{}) {
		t.Fatalf("empty cursor = %+v; want the zero position", got)
	}
	if c := directory.EncodePosition(store.Position{}); c != "" {
		t.Fatalf("EncodePosition(zero) = %q; want empty", c)
	}
}

func TestSeparatorBytesNeverMakeTwoPositionsShareACursor(t *testing.T) {
	a := store.Position{Xid: 5, Kind: store.ChangeUser, Tenant: "ab", ID: "c"}
	b := store.Position{Xid: 5, Kind: store.ChangeUser, Tenant: "a", ID: "bc"}
	c := store.Position{Xid: 5, Kind: store.ChangeUser, Tenant: "a\x01b", ID: "c"}
	d := store.Position{Xid: 5, Kind: store.ChangeUser, Tenant: "a", ID: "\x01bc"}
	seen := map[string]store.Position{}
	for _, p := range []store.Position{a, b, c, d} {
		cursor := directory.EncodePosition(p)
		if other, dup := seen[cursor]; dup {
			t.Fatalf("%+v and %+v share cursor %q", p, other, cursor)
		}
		seen[cursor] = p
		got, err := directory.DecodePosition(cursor)
		if err != nil || got != p {
			t.Fatalf("DecodePosition(%q) = %+v, %v; want %+v", cursor, got, err, p)
		}
	}
}

func TestMalformedCursorsAreInvalidArguments(t *testing.T) {
	valid := directory.EncodePosition(store.Position{Xid: 77, Kind: store.ChangeGroup, Tenant: "shop", ID: "sales"})
	raw, err := base64.RawURLEncoding.DecodeString(valid)
	if err != nil {
		t.Fatalf("decode valid cursor: %v", err)
	}
	enc := base64.RawURLEncoding.EncodeToString
	cases := map[string]string{
		"not base64":            "!!!not-base64!!!",
		"padded":                valid + "=",
		"newline inside":        valid[:4] + "\n" + valid[4:],
		"std alphabet":          strings.NewReplacer("-", "+", "_", "/").Replace(enc([]byte{0xfb, 0xff, 0xfe, 0xfb, 0xff})),
		"too short":             enc([]byte{0, 0, 0}),
		"truncated header":      enc(raw[:7]),
		"kind missing":          enc(raw[:8]),
		"tenant length missing": enc(raw[:9]),
		"truncated tenant":      enc(raw[:len(raw)-len("sales")-2]),
		"tenant length beyond":  enc(append(append([]byte{}, raw[:9]...), 0xff, 0x01)),
		"invalid utf8":          directory.EncodePosition(store.Position{Xid: 1, Kind: 1, Tenant: "\xff", ID: "x"}),
		"nul byte":              directory.EncodePosition(store.Position{Xid: 1, Kind: 1, Tenant: "t", ID: "a\x00b"}),
		"negative kind":         directory.EncodePosition(store.Position{Xid: 1, Kind: -1, Tenant: "t", ID: "x"}),
		"longer than 1024":      directory.EncodePosition(store.Position{Xid: 1, Kind: 1, Tenant: "t", ID: strings.Repeat("x", 800)}),
	}
	for name, cursor := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := directory.DecodePosition(cursor)
			if !errors.Is(err, apperr.ErrInvalidArgument) {
				t.Fatalf("DecodePosition(%q) err = %v; want ErrInvalidArgument", cursor, err)
			}
		})
	}
}
