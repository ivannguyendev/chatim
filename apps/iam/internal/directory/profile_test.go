package directory_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/ivannguyendev/chatim/apps/iam/internal/directory"
	"github.com/ivannguyendev/chatim/pkg/apperr"
)

type nameCase struct {
	name  string
	input string
	want  string
	ok    bool
}

func nameCases() []nameCase {
	return []nameCase{
		{"plain", "Lan Nguyen", "Lan Nguyen", true},
		{"vietnamese", "Nguyễn Thị Lan", "Nguyễn Thị Lan", true},
		{"trims spaces", "  Lan  ", "Lan", true},
		{"trims unicode spaces", "\u00a0Lan\u3000", "Lan", true},
		{"drops c0 controls", "La\x00n\x1f", "Lan", true},
		{"drops tab and newline", "Lan\t\nNguyen", "LanNguyen", true},
		{"drops del", "Lan\x7f", "Lan", true},
		{"drops c1 controls", "Lan\u0085\u009f", "Lan", true},
		{"drops bidi embeddings", "\u202aLan\u202e", "Lan", true},
		{"drops bidi isolates", "\u2066Lan\u2069", "Lan", true},
		{"drops direction marks", "\u200eLan\u200f", "Lan", true},
		{"trims after dropping", "\u202e  Lan  \u202c", "Lan", true},
		{"one rune", "L", "L", true},
		{"128 runes", strings.Repeat("ễ", 128), strings.Repeat("ễ", 128), true},
		{"129 runes", strings.Repeat("ễ", 129), "", false},
		{"empty", "", "", false},
		{"only spaces", "   ", "", false},
		{"only controls", "\u202e\x00\u200f", "", false},
		{"invalid utf8", "Lan\xff", "", false},
	}
}

func checkNames(t *testing.T, normalize func(string) (string, error)) {
	t.Helper()
	for _, tc := range nameCases() {
		t.Run(tc.name, func(t *testing.T) {
			got, err := normalize(tc.input)
			if !tc.ok {
				if !errors.Is(err, apperr.ErrInvalidArgument) {
					t.Fatalf("normalize(%q) = %q, %v; want ErrInvalidArgument", tc.input, got, err)
				}
				return
			}
			if err != nil || got != tc.want {
				t.Fatalf("normalize(%q) = %q, %v; want %q", tc.input, got, err, tc.want)
			}
		})
	}
}

func TestNormalizeDisplayName(t *testing.T) {
	checkNames(t, directory.NormalizeDisplayName)
}

func TestNormalizeGroupName(t *testing.T) {
	checkNames(t, directory.NormalizeGroupName)
}

func TestValidateAvatarURL(t *testing.T) {
	cases := []struct {
		name  string
		input string
		ok    bool
	}{
		{"empty means no avatar", "", true},
		{"https", "https://cdn.example/lan.png", true},
		{"https with port and query", "https://cdn.example:8443/a/lan.png?v=2", true},
		{"https scheme in capitals", "HTTPS://cdn.example/lan.png", true},
		{"2048 bytes", "https://cdn.example/" + strings.Repeat("a", 2048-len("https://cdn.example/")), true},
		{"2049 bytes", "https://cdn.example/" + strings.Repeat("a", 2049-len("https://cdn.example/")), false},
		{"http", "http://cdn.example/lan.png", false},
		{"javascript", "javascript:alert(1)", false},
		{"data", "data:image/png;base64,AAAA", false},
		{"relative", "/lan.png", false},
		{"no host", "https:///lan.png", false},
		{"port only", "https://:443/lan.png", false},
		{"opaque", "https:cdn.example/lan.png", false},
		{"userinfo", "https://lan:secret@cdn.example/lan.png", false},
		{"user only", "https://lan@cdn.example/lan.png", false},
		{"control character", "https://cdn.example/\x00.png", false},
		{"leading space", " https://cdn.example/lan.png", false},
		{"bad percent escape", "https://cdn.example/%zz", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := directory.ValidateAvatarURL(tc.input)
			if tc.ok && err != nil {
				t.Fatalf("ValidateAvatarURL(%q) = %v; want nil", tc.input, err)
			}
			if !tc.ok && !errors.Is(err, apperr.ErrInvalidArgument) {
				t.Fatalf("ValidateAvatarURL(%q) = %v; want ErrInvalidArgument", tc.input, err)
			}
		})
	}
}

func TestValidateGroupID(t *testing.T) {
	cases := []struct {
		name  string
		input string
		ok    bool
	}{
		{"letters", "sales", true},
		{"all allowed bytes", "Sales_HN-2:team", true},
		{"64 bytes", strings.Repeat("a", 64), true},
		{"65 bytes", strings.Repeat("a", 65), false},
		{"empty", "", false},
		{"space", "sales hn", false},
		{"dot", "sales.hn", false},
		{"slash", "sales/hn", false},
		{"unicode", "bán-hàng", false},
		{"separator", "sales│hn", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := directory.ValidateGroupID(tc.input)
			if tc.ok && err != nil {
				t.Fatalf("ValidateGroupID(%q) = %v; want nil", tc.input, err)
			}
			if !tc.ok && !errors.Is(err, apperr.ErrInvalidArgument) {
				t.Fatalf("ValidateGroupID(%q) = %v; want ErrInvalidArgument", tc.input, err)
			}
		})
	}
}
