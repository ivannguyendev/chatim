package pbconv_test

import (
	"testing"

	"github.com/ivannguyendev/chatim/apps/core/internal/model/pbconv"
)

func TestBookmarkEventIDNamesTheMessageTheUserAndTheVersion(t *testing.T) {
	cases := []struct{ got, want string }{
		{pbconv.BookmarkEventID(42, 0, 7, "bob", 1), "42-bm-0-7-bob-v1"},
		{pbconv.BookmarkEventID(42, 3, 9, "a-v1", 12), "42-bm-3-9-a-v1-v12"},
	}
	for _, c := range cases {
		if c.got != c.want {
			t.Errorf("BookmarkEventID = %q, want %q", c.got, c.want)
		}
	}
}
