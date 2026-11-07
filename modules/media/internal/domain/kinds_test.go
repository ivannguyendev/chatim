package domain_test

import (
	"testing"

	"github.com/ivannguyendev/chatim/modules/media/internal/domain"
)

func TestKindsMatchByMimeFamily(t *testing.T) {
	mimes := []string{"image/png", "video/mp4", "application/pdf", "text/plain"}
	cases := []struct {
		kinds domain.Kinds
		all   bool
		want  []bool
	}{
		{domain.Kinds{}, true, []bool{true, true, true, true}},
		{domain.Kinds{Image: true, Video: true, Other: true}, true, []bool{true, true, true, true}},
		{domain.Kinds{Image: true}, false, []bool{true, false, false, false}},
		{domain.Kinds{Video: true}, false, []bool{false, true, false, false}},
		{domain.Kinds{Other: true}, false, []bool{false, false, true, true}},
		{domain.Kinds{Image: true, Video: true}, false, []bool{true, true, false, false}},
		{domain.Kinds{Image: true, Other: true}, false, []bool{true, false, true, true}},
		{domain.Kinds{Video: true, Other: true}, false, []bool{false, true, true, true}},
	}
	for _, c := range cases {
		if c.kinds.All() != c.all {
			t.Fatalf("%+v All = %v", c.kinds, c.kinds.All())
		}
		for i, mime := range mimes {
			if got := c.kinds.Match(mime); got != c.want[i] {
				t.Fatalf("%+v Match(%s) = %v", c.kinds, mime, got)
			}
		}
	}
}

func TestKindsSplitTheMediaFamilies(t *testing.T) {
	in, out := domain.Kinds{Image: true, Other: true}.Families()
	if len(in) != 1 || in[0] != "image" || len(out) != 1 || out[0] != "video" {
		t.Fatalf("families in %v out %v", in, out)
	}
}
