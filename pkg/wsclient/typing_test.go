package wsclient

import (
	"slices"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/lxzan/gws"

	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

func signalLines(server *fakeServer) []string {
	var got []string
	for _, f := range server.drain() {
		switch {
		case f.frame.GetTyping() != nil && f.frame.GetTyping().GetStopped():
			got = append(got, "stopped "+f.frame.GetTyping().GetRoomId())
		case f.frame.GetTyping() != nil:
			got = append(got, "typing "+f.frame.GetTyping().GetRoomId())
		case f.frame.GetWatchPresence() != nil:
			got = append(got, "watch "+strings.Join(f.frame.GetWatchPresence().GetUserIds(), ","))
		case f.frame.GetRequest().GetSendMessage() != nil:
			got = append(got, "send "+f.frame.GetRequest().GetSendMessage().GetRoomId())
		}
	}
	return got
}

func answeringServer(t *testing.T) *fakeServer {
	return newFakeServer(func(conn *gws.Conn, frame *chatimv1.ClientFrame) {
		if req := frame.GetRequest(); req != nil {
			respond(t, conn, &chatimv1.ServerResponse{Id: req.GetId()})
		}
	})
}

func expectLines(t *testing.T, got []string, want ...string) {
	t.Helper()
	if !slices.Equal(got, want) {
		t.Fatalf("lines = %v, want %v", got, want)
	}
}

func TestTypingSendsTheFirstAtOnceAndThenAtMostOncePerInterval(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		server := newFakeServer(nil)
		client := dialPipe(t, server, "token")
		for range 10 {
			if err := client.Typing("7"); err != nil {
				t.Fatalf("Typing: %v", err)
			}
			time.Sleep(290 * time.Millisecond)
		}
		synctest.Wait()
		expectLines(t, signalLines(server), "typing 7")
		time.Sleep(100 * time.Millisecond)
		_ = client.Typing("7")
		_ = client.Typing("8")
		synctest.Wait()
		expectLines(t, signalLines(server), "typing 7", "typing 8")
		_ = client.Close()
	})
}

func TestStopTypingSendsOneStoppedOnlyAfterATyping(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		server := newFakeServer(nil)
		client := dialPipe(t, server, "token")
		_ = client.StopTyping("7")
		synctest.Wait()
		expectLines(t, signalLines(server))
		_ = client.Typing("7")
		_ = client.StopTyping("7")
		_ = client.StopTyping("7")
		synctest.Wait()
		expectLines(t, signalLines(server), "typing 7", "stopped 7")
		_ = client.Typing("7")
		synctest.Wait()
		expectLines(t, signalLines(server), "typing 7")
		_ = client.Close()
	})
}

func TestSendMessageEndsTypingWithoutAStopped(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		server := answeringServer(t)
		client := dialPipe(t, server, "token")
		_ = client.Typing("7")
		if _, err := client.Call(t.Context(), sendMessageRequest("7")); err != nil {
			t.Fatalf("Call: %v", err)
		}
		_ = client.StopTyping("7")
		synctest.Wait()
		expectLines(t, signalLines(server), "typing 7", "send 7")
		_ = client.Typing("7")
		synctest.Wait()
		expectLines(t, signalLines(server), "typing 7")
		_ = client.Close()
	})
}

func TestWatchPresenceSendsTheWholeList(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		server := newFakeServer(nil)
		client := dialPipe(t, server, "token")
		_ = client.WatchPresence([]string{"bob", "carol"})
		_ = client.WatchPresence([]string{"dave"})
		synctest.Wait()
		expectLines(t, signalLines(server), "watch bob,carol", "watch dave")
		_ = client.Close()
	})
}

func dialWithServerConn(t *testing.T, opts ...Option) (*Client, *gws.Conn) {
	t.Helper()
	conns := make(chan *gws.Conn, 1)
	server := newFakeServer(func(conn *gws.Conn, frame *chatimv1.ClientFrame) {
		if frame.GetAuth() != nil {
			conns <- conn
		}
	})
	client := dialPipe(t, server, "token", opts...)
	return client, <-conns
}

func typingLog(log *handlerLog) Option {
	return OnTyping(func(room, user string, typing bool) {
		log.add("%s %s %t", room, user, typing)
	})
}

func notice(t *testing.T, conn *gws.Conn, room, user string, stopped bool) {
	t.Helper()
	writeServerFrame(t, conn, &chatimv1.ServerFrame{Frame: &chatimv1.ServerFrame_Typing{
		Typing: &chatimv1.TypingNotice{RoomId: room, UserId: user, Stopped: stopped},
	}})
	synctest.Wait()
}

func sentBy(room, user string, seq uint64) *chatimv1.ServerFrame {
	ev := created(room, 0, seq)
	ev.Actor = user
	return eventFrame(ev)
}

func TestOnTypingReportsANoticeAndAStopped(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var log handlerLog
		client, conn := dialWithServerConn(t, typingLog(&log))
		notice(t, conn, "7", "bob", true)
		notice(t, conn, "7", "bob", false)
		notice(t, conn, "7", "bob", true)
		notice(t, conn, "7", "bob", true)
		expectLines(t, log.snapshot(), "7 bob true", "7 bob false")
		_ = client.Close()
	})
}

func TestOnTypingEndsAfterSilenceAndANoticeExtendsIt(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var log handlerLog
		client, conn := dialWithServerConn(t, typingLog(&log))
		notice(t, conn, "7", "bob", false)
		time.Sleep(TypingTimeout - time.Millisecond)
		synctest.Wait()
		expectLines(t, log.snapshot(), "7 bob true")
		time.Sleep(time.Millisecond)
		synctest.Wait()
		expectLines(t, log.snapshot(), "7 bob true", "7 bob false")
		notice(t, conn, "7", "bob", false)
		time.Sleep(5 * time.Second)
		notice(t, conn, "7", "bob", false)
		time.Sleep(6*time.Second - time.Millisecond)
		synctest.Wait()
		expectLines(t, log.snapshot(), "7 bob true", "7 bob false", "7 bob true", "7 bob true")
		time.Sleep(time.Millisecond)
		synctest.Wait()
		expectLines(t, log.snapshot(), "7 bob true", "7 bob false", "7 bob true", "7 bob true", "7 bob false")
		_ = client.Close()
	})
}

func TestOnTypingEndsWhenTheTypistSendsAMessageInThatRoom(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var log handlerLog
		client, conn := dialWithServerConn(t, typingLog(&log), OnEvent(func(ev *chatimv1.Event) {
			log.add("event %s", ev.GetId())
		}))
		notice(t, conn, "7", "bob", false)
		writeServerFrame(t, conn, sentBy("8", "bob", 1))
		writeServerFrame(t, conn, sentBy("7", "carol", 1))
		writeServerFrame(t, conn, sentBy("7", "bob", 2))
		synctest.Wait()
		time.Sleep(2 * TypingTimeout)
		synctest.Wait()
		expectLines(t, log.snapshot(), "7 bob true", "event 8-0-1", "event 7-0-1", "7 bob false", "event 7-0-2")
		_ = client.Close()
	})
}

func TestOnTypingTracksEachUserOnItsOwnAndIgnoresUsersNotShown(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var log handlerLog
		client, conn := dialWithServerConn(t, typingLog(&log))
		notice(t, conn, "7", "dave", true)
		writeServerFrame(t, conn, sentBy("7", "erin", 1))
		notice(t, conn, "7", "bob", false)
		time.Sleep(3 * time.Second)
		notice(t, conn, "7", "carol", false)
		time.Sleep(3 * time.Second)
		synctest.Wait()
		expectLines(t, log.snapshot(), "7 bob true", "7 carol true", "7 bob false")
		time.Sleep(3 * time.Second)
		synctest.Wait()
		expectLines(t, log.snapshot(), "7 bob true", "7 carol true", "7 bob false", "7 carol false")
		_ = client.Close()
	})
}

func TestOnPresenceReceivesTheEntries(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var log handlerLog
		client, conn := dialWithServerConn(t, OnPresence(func(entries []*chatimv1.PresenceEntry) {
			for _, e := range entries {
				log.add("%s %t", e.GetUserId(), e.GetOnline())
			}
		}))
		writeServerFrame(t, conn, &chatimv1.ServerFrame{Frame: &chatimv1.ServerFrame_Presence{Presence: &chatimv1.PresenceFrame{
			Entries: []*chatimv1.PresenceEntry{{UserId: "bob", Online: true}, {UserId: "carol"}},
		}}})
		synctest.Wait()
		expectLines(t, log.snapshot(), "bob true", "carol false")
		_ = client.Close()
	})
}

func TestCloseStopsTypingTimers(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var log handlerLog
		client, conn := dialWithServerConn(t, typingLog(&log))
		notice(t, conn, "7", "bob", false)
		if err := client.Close(); err != nil {
			t.Fatalf("Close: %v", err)
		}
		time.Sleep(2 * TypingTimeout)
		synctest.Wait()
		expectLines(t, log.snapshot(), "7 bob true")
	})
}
