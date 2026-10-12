package pbconv_test

import (
	"testing"

	"google.golang.org/protobuf/proto"

	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/model/pbconv"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
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

func TestBookmarkChangedCarriesTheMessageTheStateAndTheVer(t *testing.T) {
	for _, on := range []bool{true, false} {
		b := domain.Bookmark{Room: groupRoom().ID, Thread: 0, Seq: 9, Tenant: "acme", User: "bob", On: on, Ver: 3, At: changedAt}
		want := memberEnvelope("9007199254740993-bm-0-9-bob-v3", "bob")
		want.Seq = 9
		want.Payload = &chatimv1.Event_BookmarkChanged{BookmarkChanged: &chatimv1.BookmarkChanged{Seq: 9, User: "bob", On: on, Ver: 3}}
		if got := pbconv.BookmarkChanged(groupRoom(), b); !proto.Equal(got, want) {
			t.Fatalf("BookmarkChanged(on %v) = %v, want %v", on, got, want)
		}
	}
}
