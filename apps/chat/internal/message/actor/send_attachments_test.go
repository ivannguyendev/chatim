package actor_test

import (
	"context"
	"encoding/base32"
	"encoding/binary"
	"errors"
	"reflect"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/ivannguyendev/chatim/apps/chat/internal/message/actor"
	"github.com/ivannguyendev/chatim/apps/chat/internal/model/domain"
	"github.com/ivannguyendev/chatim/pkg/core/apperr"
)

var fileKeyCode = base32.NewEncoding("0123456789abcdefghijklmnopqrstuv").WithPadding(base32.NoPadding)

func fileKey(user string, at time.Time, seed byte) string {
	var raw [16]byte
	var ms [8]byte
	binary.BigEndian.PutUint64(ms[:], uint64(at.UnixMilli()))
	copy(raw[:6], ms[2:])
	for i := 6; i < 16; i++ {
		raw[i] = seed + byte(i)
	}
	return tenant + "/" + user + "/" + at.UTC().Format("20060102") + "/" + fileKeyCode.EncodeToString(raw[:])
}

func photoOf(key string) domain.Attachment {
	return domain.Attachment{Key: key, Name: "ảnh.png", Mime: "Image/PNG", Size: 2048, Dim: []int32{640, 480}}
}

func TestSendAcceptsAttachmentsWithoutText(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		rg := started(t, baseConfig)
		c := cmd(roomA, "alice", "c1")
		c.Text = ""
		c.Attachments = []domain.Attachment{photoOf(fileKey("alice", time.Now().Add(-time.Hour), 1))}
		mustSend(t, rg.Router, c)
		synctest.Wait()
		sent := rg.sub.sent()
		if len(sent) != 1 || len(sent[0]) != 1 {
			t.Fatalf("submitted %v, want one message", sent)
		}
		want := c.Attachments[0]
		want.Mime = "image/png"
		if got := sent[0][0].Attachments; !reflect.DeepEqual(got, []domain.Attachment{want}) || sent[0][0].Text != "" {
			t.Fatalf("stored attachments %+v text %q, want %+v and no text", got, sent[0][0].Text, want)
		}
		if c.Attachments[0].Mime != "Image/PNG" {
			t.Fatalf("caller attachments changed to %+v", c.Attachments)
		}
		evs := rg.events.events(roomA)
		if len(evs) != 1 || len(evs[0].GetMessageCreated().GetMessage().GetAttachments()) != 1 {
			t.Fatalf("events = %v, want one msg_created with the attachment", evs)
		}
	})
}

func TestSendRejectsBadAttachmentsBeforeRouting(t *testing.T) {
	rg := started(t, baseConfig)
	now := time.Now()
	tests := map[string]struct {
		mutate func(*actor.SendCmd)
		field  string
	}{
		"empty text without attachments": {func(c *actor.SendCmd) { c.Text = "" }, "text"},
		"another user's file": {func(c *actor.SendCmd) {
			c.Attachments = []domain.Attachment{photoOf(fileKey("bob", now, 1))}
		}, "attachments.key"},
		"file older than a day": {func(c *actor.SendCmd) {
			c.Attachments = []domain.Attachment{photoOf(fileKey("alice", now.Add(-25*time.Hour), 1))}
		}, "attachments.key"},
		"thumbnail key": {func(c *actor.SendCmd) {
			c.Attachments = []domain.Attachment{photoOf(fileKey("alice", now, 1) + ".thumb")}
		}, "attachments.key"},
	}
	for name, tt := range tests {
		c := cmd(roomA, "alice", "c1")
		tt.mutate(&c)
		_, err := rg.Send(context.Background(), c)
		if !errors.Is(err, apperr.ErrInvalidArgument) || !strings.HasSuffix(err.Error(), ": "+tt.field) {
			t.Errorf("%s: Send = %v, want ErrInvalidArgument naming %q", name, err, tt.field)
		}
	}
	if n := rg.ActorCount(); n != 0 {
		t.Errorf("%d actors started for invalid commands, want 0", n)
	}
}

func TestForwardKeepsAttachmentsOfAnotherUser(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		rg := started(t, baseConfig)
		c := cmd(roomA, "alice", "c1")
		c.Text = ""
		c.Forward = &domain.ForwardRef{Room: roomB, Seq: 9, Author: "lan"}
		c.Attachments = []domain.Attachment{photoOf(fileKey("lan", time.Now().Add(-30*24*time.Hour), 1))}
		mustSend(t, rg.Router, c)
		synctest.Wait()
		if got := rg.sub.sent()[0][0].Attachments; len(got) != 1 || got[0].Key != c.Attachments[0].Key {
			t.Fatalf("stored attachments %+v, want lan's file kept", got)
		}
	})
}
