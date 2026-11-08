package domain_test

import (
	"strings"
	"testing"
	"time"

	"github.com/ivannguyendev/chatim/modules/media/internal/domain"
)

func TestObjectKeys(t *testing.T) {
	at := time.Date(2026, 11, 6, 23, 30, 0, 0, time.FixedZone("ICT", 7*3600))
	key := domain.NewObjectKey("acme", "an", at, [10]byte{1, 2, 3})
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
	first := domain.NewObjectKey("acme", "an", at, [10]byte{0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff})
	sameMilli := domain.NewObjectKey("acme", "an", at, [10]byte{0xfe})
	later := domain.NewObjectKey("acme", "an", at.Add(time.Millisecond), [10]byte{})
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
	key := domain.NewObjectKey("acme", "an", time.UnixMilli(1_794_000_000_000), [10]byte{7})
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
