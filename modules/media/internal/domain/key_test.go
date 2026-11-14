package domain_test

import (
	"strings"
	"testing"
	"time"

	"github.com/ivannguyendev/chatim/modules/media/internal/domain"
)

func TestObjectKeys(t *testing.T) {
	at := time.Date(2026, 11, 6, 23, 30, 0, 0, time.FixedZone("ICT", 7*3600))
	key := domain.NewObjectKey("acme", "an", false, at, [10]byte{1, 2, 3})
	if !strings.HasPrefix(key, "acme/an/20261106/") || len(key) != len("acme/an/20261106/")+26 {
		t.Fatalf("key = %q", key)
	}
	if !domain.OwnsKey(key, "acme", "an") {
		t.Fatal("owner does not own its key")
	}
	for _, other := range [][2]string{{"acme", "an-b"}, {"acme", "a"}, {"other", "an"}, {"", ""}} {
		if domain.OwnsKey(key, other[0], other[1]) {
			t.Fatalf("%v owns %q", other, key)
		}
	}
	for _, forged := range []string{"acme/an/", "acme/an/20261106", "acme/an/20261106/x/y", "acme/an//x", "acme/an/2026110/x", strings.Repeat("a", 600)} {
		if domain.OwnsKey(forged, "acme", "an") {
			t.Fatalf("forged key %q accepted", forged)
		}
	}
}

func TestObjectKeysSortByTime(t *testing.T) {
	at := time.UnixMilli(1_794_000_000_000)
	first := domain.NewObjectKey("acme", "an", false, at, [10]byte{0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff})
	sameMilli := domain.NewObjectKey("acme", "an", false, at, [10]byte{0xfe})
	later := domain.NewObjectKey("acme", "an", false, at.Add(time.Millisecond), [10]byte{})
	if first == sameMilli {
		t.Fatalf("same millisecond gave the same key %q", first)
	}
	if first >= later || sameMilli >= later {
		t.Fatalf("later key %q does not sort after %q and %q", later, first, sameMilli)
	}
	stamp, ok := strings.CutPrefix(first, "acme/an/"+at.UTC().Format("20060102")+"/")
	if !ok || len(stamp) != 26 || strings.Trim(stamp, "0123456789abcdefghijklmnopqrstuv") != "" {
		t.Fatalf("key %q is not 26 lowercase base32hex characters", first)
	}
}

func TestOwnsKeyRejectsCharactersOutsideBase32Hex(t *testing.T) {
	key := domain.NewObjectKey("acme", "an", false, time.UnixMilli(1_794_000_000_000), [10]byte{7})
	if !domain.OwnsKey(key, "acme", "an") {
		t.Fatalf("own key %q refused", key)
	}
	prefix, random := key[:len(key)-26], key[len(key)-26:]
	for _, forged := range []string{
		prefix + "w" + random[1:],
		prefix + "z" + random[1:],
		prefix + strings.ToUpper(random),
		prefix + "-" + random[1:],
		domain.ThumbnailKey(key, ".thumb"),
		key + "x",
	} {
		if domain.OwnsKey(forged, "acme", "an") {
			t.Fatalf("forged key %q accepted", forged)
		}
	}
}

func TestThumbnailKeyAppendsTheSuffix(t *testing.T) {
	if got := domain.ThumbnailKey("acme/an/20261112/0123456789abcdefghijklmnop", ".thumb"); got != "acme/an/20261112/0123456789abcdefghijklmnop.thumb" {
		t.Fatalf("thumbnail key = %q", got)
	}
}

func TestPublicObjectKeys(t *testing.T) {
	at := time.Date(2026, 11, 13, 9, 0, 0, 0, time.UTC)
	key := domain.NewObjectKey("acme", "an", true, at, [10]byte{1})
	if !strings.HasPrefix(key, "acme/an/pub/20261113/") || len(key) != len("acme/an/pub/20261113/")+26 {
		t.Fatalf("key = %q", key)
	}
	if !domain.OwnsKey(key, "acme", "an") {
		t.Fatal("owner does not own its public key")
	}
	for _, other := range [][2]string{{"acme", "an-b"}, {"acme", "pub"}, {"other", "an"}} {
		if domain.OwnsKey(key, other[0], other[1]) {
			t.Fatalf("%v owns %q", other, key)
		}
	}
	random := key[len(key)-26:]
	for _, forged := range []string{
		"acme/an/pux/20261113/" + random,
		"acme/an/pub/pub/20261113/" + random,
		"acme/an/20261113/pub/" + random,
		"acme/an/pub/" + random,
	} {
		if domain.OwnsKey(forged, "acme", "an") {
			t.Fatalf("forged key %q accepted", forged)
		}
	}
	long := domain.NewObjectKey(strings.Repeat("t", 32), strings.Repeat("u", 64), true, at, [10]byte{})
	if !domain.OwnsKey(long, strings.Repeat("t", 32), strings.Repeat("u", 64)) {
		t.Fatalf("longest public key %q refused", long)
	}
}

func TestParseKeyReadsTheOwnerAndTheKind(t *testing.T) {
	at := time.Date(2026, 11, 13, 9, 0, 0, 0, time.UTC)
	private := domain.NewObjectKey("acme", "an", false, at, [10]byte{2})
	public := domain.NewObjectKey("acme", "pub", true, at, [10]byte{2})
	for key, want := range map[string]domain.KeyInfo{
		private: {Tenant: "acme", User: "an"},
		public:  {Tenant: "acme", User: "pub", Public: true},
	} {
		if got, ok := domain.ParseKey(key); !ok || got != want {
			t.Fatalf("ParseKey(%q) = %+v, %v", key, got, ok)
		}
	}
	random := private[len(private)-26:]
	for _, bad := range []string{
		"",
		"/an/20261113/" + random,
		"acme//20261113/" + random,
		"acme/an/20261113/" + random + "/x",
		"pub/acme/an/20261113/" + random,
		"acme/an/pub/pub/20261113/" + random,
		"acme/an/20261313/" + random,
		"acme/an/20261113/" + random[1:],
	} {
		if got, ok := domain.ParseKey(bad); ok {
			t.Fatalf("ParseKey(%q) = %+v", bad, got)
		}
	}
}

func TestPublicKeyPrefixSplitsPrivateFromPublicKeys(t *testing.T) {
	bound := domain.PublicKeyPrefix("acme", "an")
	if bound != "acme/an/pub/" {
		t.Fatalf("bound = %q", bound)
	}
	days := []time.Time{time.Date(1, 1, 1, 0, 0, 0, 0, time.UTC), time.Date(9999, 12, 31, 23, 59, 59, 0, time.UTC)}
	randoms := [][10]byte{{}, {0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff}}
	for _, day := range days {
		for _, random := range randoms {
			if key := domain.NewObjectKey("acme", "an", false, day, random); key >= bound {
				t.Fatalf("private key %q does not sort before %q", key, bound)
			}
			if key := domain.NewObjectKey("acme", "an", true, day, random); !strings.HasPrefix(key, bound) {
				t.Fatalf("public key %q does not start with %q", key, bound)
			}
			if key := domain.NewObjectKey("acme", "an2", true, day, random); strings.HasPrefix(key, bound) {
				t.Fatalf("key %q of another user starts with %q", key, bound)
			}
		}
	}
}
