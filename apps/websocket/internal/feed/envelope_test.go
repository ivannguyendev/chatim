package feed

import "testing"

func TestParseEventReadsTheFourTokensAfterTheRoot(t *testing.T) {
	got, ok := parseEvent("evt", "evt.acme.message.42.msg_created")
	if !ok || got.Tenant != "acme" || got.DataKind != "message" || got.Room != "42" || got.Kind != "msg_created" {
		t.Fatalf("parseEvent = %+v, %v", got, ok)
	}
	if got, ok := parseEvent("evt.it", "evt.it.acme.member.7.member_added"); !ok || got.Room != "7" || got.Kind != "member_added" {
		t.Fatalf("parseEvent(dotted root) = %+v, %v", got, ok)
	}
}

func TestParseEventRejectsMalformedSubjects(t *testing.T) {
	for _, subject := range []string{
		"", "evt", "evt.", "evt.acme.message.42", "evt.acme.message.42.msg_created.extra",
		"evt..message.42.msg_created", "evt.acme.message.42.", "other.acme.message.42.msg_created", "evtx.acme.message.42.msg_created",
	} {
		if got, ok := parseEvent("evt", subject); ok {
			t.Errorf("parseEvent(%q) = %+v, want rejected", subject, got)
		}
	}
}

func TestParsePublishReadsTheTenant(t *testing.T) {
	got, ok := parsePublish("websocket.publish", "websocket.publish.acme")
	if !ok || got.Tenant != "acme" {
		t.Fatalf("parsePublish = %+v, %v", got, ok)
	}
	for _, subject := range []string{"websocket.publish", "websocket.publish.", "websocket.publish.acme.x", "notify.acme"} {
		if got, ok := parsePublish("websocket.publish", subject); ok {
			t.Errorf("parsePublish(%q) = %+v, want rejected", subject, got)
		}
	}
}

func TestSpreadIsStableAndInRange(t *testing.T) {
	seen := map[uint64]bool{}
	for _, key := range []string{"1", "2", "3", "42", "77", "9001", "123456789"} {
		i := spread(key, 4)
		if i >= 4 || i != spread(key, 4) {
			t.Fatalf("spread(%q) = %d", key, i)
		}
		seen[i] = true
	}
	if len(seen) < 2 {
		t.Fatal("spread put every key on one worker")
	}
}
