package domain_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/ivannguyendev/chatim/modules/media/internal/domain"
)

func TestLimitsValidate(t *testing.T) {
	ok := limits()
	if err := ok.Validate(); err != nil {
		t.Fatal(err)
	}
	broken := map[string]func(*domain.Limits){
		"min part under the S3 floor": func(l *domain.Limits) { l.MinPartBytes = 4 * mib },
		"min above max":               func(l *domain.Limits) { l.MinPartBytes = 256 * mib },
		"no parts":                    func(l *domain.Limits) { l.MaxParts = 0 },
		"over the S3 part count":      func(l *domain.Limits) { l.MaxParts = 10001 },
		"no streams":                  func(l *domain.Limits) { l.MaxParallel = 0 },
		"more streams than parts":     func(l *domain.Limits) { l.MaxParts, l.MaxParallel = 2, 4 },
		"file over parts times size":  func(l *domain.Limits) { l.MaxFileBytes = 2000*128*mib + 1 },
		"no file size":                func(l *domain.Limits) { l.MaxFileBytes = 0 },
	}
	for name, change := range broken {
		l := limits()
		change(&l)
		if err := l.Validate(); err == nil {
			t.Errorf("%s: no error", name)
		}
	}
}

func TestValidateStart(t *testing.T) {
	l := limits()
	if err := domain.ValidateStart("clip.mp4", "video/mp4", 40*mib, l); err != nil {
		t.Fatal(err)
	}
	bad := map[string][3]any{
		"empty name":        {"", "video/mp4", 40 * mib},
		"long name":         {strings.Repeat("a", 256), "video/mp4", 40 * mib},
		"broken utf-8":      {"\xff.mp4", "video/mp4", 40 * mib},
		"control character": {"a\nb.mp4", "video/mp4", 40 * mib},
		"no subtype":        {"clip.mp4", "video", 40 * mib},
		"empty type":        {"clip.mp4", "", 40 * mib},
		"zero size":         {"clip.mp4", "video/mp4", int64(0)},
		"over the limit":    {"clip.mp4", "video/mp4", l.MaxFileBytes + 1},
	}
	for name, in := range bad {
		if err := domain.ValidateStart(in[0].(string), in[1].(string), in[2].(int64), l); !errors.Is(err, domain.ErrInvalid) {
			t.Errorf("%s: err = %v", name, err)
		}
	}
	if err := domain.ValidateStart(strings.Repeat("ả", 85), "image/png; charset=binary", 1, l); err != nil {
		t.Fatalf("255-byte Vietnamese name: %v", err)
	}
}

func TestObjectKeys(t *testing.T) {
	at := time.Date(2026, 11, 6, 23, 30, 0, 0, time.FixedZone("ICT", 7*3600))
	key := domain.NewObjectKey("acme", "an", at, [16]byte{1, 2, 3})
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

func TestCheckPartNumbers(t *testing.T) {
	if err := domain.CheckPartNumbers([]int32{4, 2, 3}, 10, 4); err != nil {
		t.Fatal(err)
	}
	bad := map[string][]int32{
		"none":         nil,
		"over N":       {1, 2, 3, 4, 5},
		"zero":         {0},
		"past the end": {11},
		"twice":        {2, 2},
	}
	for name, nums := range bad {
		if err := domain.CheckPartNumbers(nums, 10, 4); !errors.Is(err, domain.ErrInvalid) {
			t.Errorf("%s: err = %v", name, err)
		}
	}
}

func TestCheckUploadedParts(t *testing.T) {
	parts := []domain.UploadedPart{{Number: 3, ETag: `"c"`}, {Number: 1, ETag: `"a"`}, {Number: 2, ETag: `"b"`}}
	sorted, err := domain.CheckUploadedParts(parts, 3)
	if err != nil {
		t.Fatal(err)
	}
	if sorted[0].Number != 1 || sorted[2].Number != 3 || parts[0].Number != 3 {
		t.Fatalf("sorted = %v, input changed = %v", sorted, parts[0].Number != 3)
	}
	bad := map[string][]domain.UploadedPart{
		"missing part 2": {{Number: 1, ETag: "a"}, {Number: 3, ETag: "c"}},
		"extra part":     {{Number: 1, ETag: "a"}, {Number: 2, ETag: "b"}, {Number: 3, ETag: "c"}, {Number: 4, ETag: "d"}},
		"twice":          {{Number: 1, ETag: "a"}, {Number: 1, ETag: "a"}, {Number: 3, ETag: "c"}},
		"empty etag":     {{Number: 1, ETag: "a"}, {Number: 2, ETag: ""}, {Number: 3, ETag: "c"}},
		"long etag":      {{Number: 1, ETag: "a"}, {Number: 2, ETag: strings.Repeat("e", 129)}, {Number: 3, ETag: "c"}},
	}
	for name, in := range bad {
		if _, err := domain.CheckUploadedParts(in, 3); !errors.Is(err, domain.ErrInvalid) {
			t.Errorf("%s: err = %v", name, err)
		}
	}
}
