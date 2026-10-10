package mongostore

import (
	"errors"
	"testing"

	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/pkg/keys"
)

func TestDecodeMentionReadsTheTargetFromTheID(t *testing.T) {
	id := keys.Mention(keys.Msg(7, 0, 3), keys.MentionUserKind, "minh")
	got, err := decodeMention(mentionLinkDoc{ID: id, Tenant: "acme", Sender: "alice", State: mentionRetired, Ver: 4, CreatedAt: codecTime, UpdatedAt: codecTime})
	want := domain.Mention{
		Key: domain.MsgKey{Room: 7, Seq: 3}, Tenant: "acme", Target: domain.MentionTarget{Kind: domain.MentionUser, ID: "minh"},
		Sender: "alice", Ver: 4, CreatedAt: codecTime, UpdatedAt: codecTime,
	}
	if err != nil || got != want {
		t.Fatalf("decodeMention = %+v, %v; want %+v", got, err, want)
	}
}

func TestDecodeMentionRejectsCorruptDocs(t *testing.T) {
	good := keys.Mention(keys.Msg(7, 0, 3), keys.MentionAllKind, "")
	for name, d := range map[string]mentionLinkDoc{
		"short id":     {ID: keys.Msg(7, 0, 3), State: mentionLive},
		"bad state":    {ID: good, State: 3},
		"negative ver": {ID: good, State: mentionLive, Ver: -1},
	} {
		if _, err := decodeMention(d); !errors.Is(err, errCorrupt) {
			t.Errorf("%s: decodeMention = %v, want errCorrupt", name, err)
		}
	}
}
