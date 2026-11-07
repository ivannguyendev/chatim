package domain_test

import (
	"errors"
	"strings"
	"testing"

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
	if _, err := domain.ValidateStart("clip.mp4", "video/mp4", 40*mib, l); err != nil {
		t.Fatal(err)
	}
	bad := map[string][3]any{
		"empty name":         {"", "video/mp4", 40 * mib},
		"long name":          {strings.Repeat("a", 256), "video/mp4", 40 * mib},
		"bidi override name": {"invoice\u202efdp.exe", "video/mp4", 40 * mib},
		"broken utf-8":       {"\xff.mp4", "video/mp4", 40 * mib},
		"control character":  {"a\nb.mp4", "video/mp4", 40 * mib},
		"no subtype":         {"clip.mp4", "video", 40 * mib},
		"empty type":         {"clip.mp4", "", 40 * mib},
		"zero size":          {"clip.mp4", "video/mp4", int64(0)},
		"over the limit":     {"clip.mp4", "video/mp4", l.MaxFileBytes + 1},
	}
	for name, in := range bad {
		if _, err := domain.ValidateStart(in[0].(string), in[1].(string), in[2].(int64), l); !errors.Is(err, domain.ErrInvalid) {
			t.Errorf("%s: err = %v", name, err)
		}
	}
	if _, err := domain.ValidateStart(strings.Repeat("ả", 85), "image/png; charset=binary", 1, l); err != nil {
		t.Fatalf("255-byte Vietnamese name: %v", err)
	}
	contentType, err := domain.ValidateStart("a.png", "Image/PNG", 1, l)
	if err != nil || contentType != "image/png" {
		t.Fatalf("content type = %q, %v", contentType, err)
	}
}

func TestValidateFileFacts(t *testing.T) {
	good := []struct {
		dim []int32
		dur int64
	}{{nil, 0}, {[]int32{}, 0}, {[]int32{1920, 1080}, 95400}, {[]int32{1, 1}, 0}}
	for _, g := range good {
		if err := domain.ValidateFileFacts(g.dim, g.dur); err != nil {
			t.Errorf("dim %v dur %d: %v", g.dim, g.dur, err)
		}
	}
	bad := map[string]struct {
		dim []int32
		dur int64
	}{
		"one value":       {[]int32{1920}, 0},
		"three values":    {[]int32{1, 2, 3}, 0},
		"zero width":      {[]int32{0, 1080}, 0},
		"negative height": {[]int32{1920, -1}, 0},
		"negative dur":    {nil, -1},
	}
	for name, b := range bad {
		if err := domain.ValidateFileFacts(b.dim, b.dur); !errors.Is(err, domain.ErrInvalid) {
			t.Errorf("%s: err = %v", name, err)
		}
	}
}

func TestValidateHash(t *testing.T) {
	hex64 := strings.Repeat("9f86d081", 8)
	for _, ok := range []string{"", "sha256:" + hex64} {
		if err := domain.ValidateHash(ok); err != nil {
			t.Errorf("%q: %v", ok, err)
		}
	}
	bad := []string{
		"sha256:" + strings.ToUpper(hex64),
		"sha256:" + hex64[:63],
		"sha256:" + hex64 + "0",
		"sha256:" + hex64[:63] + "g",
		"md5:" + hex64,
		hex64,
		"SHA256:" + hex64,
	}
	for _, h := range bad {
		if err := domain.ValidateHash(h); !errors.Is(err, domain.ErrInvalid) {
			t.Errorf("%q: err = %v", h, err)
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
