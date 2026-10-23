package wsclient

import (
	"context"
	"errors"
	"slices"
	"strconv"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestMarkReadSendsTheFirstAtOnceAndTheLargestPendingSeqOncePerSecond(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		s := newReadServer(t)
		client := dialPipe(t, s.fakeServer, "token")
		for _, seq := range []uint64{1, 4, 9, 3, 7, 10, 2, 8, 6, 5} {
			if err := client.MarkRead(t.Context(), "7", seq); err != nil {
				t.Fatalf("MarkRead(%d): %v", seq, err)
			}
			time.Sleep(90 * time.Millisecond)
		}
		synctest.Wait()
		if got := s.lines(); !slices.Equal(got, []string{"read 7 1"}) {
			t.Fatalf("frames within the first second = %v, want only the first read", got)
		}
		time.Sleep(time.Second)
		synctest.Wait()
		if got := s.lines(); !slices.Equal(got, []string{"read 7 10"}) {
			t.Fatalf("frames at the tick = %v, want the largest pending seq once", got)
		}
		time.Sleep(3 * time.Second)
		synctest.Wait()
		if got := s.lines(); len(got) != 0 {
			t.Fatalf("frames in a quiet room = %v, want none", got)
		}
		if err := client.MarkRead(t.Context(), "7", 11); err != nil {
			t.Fatalf("MarkRead after the quiet period: %v", err)
		}
		synctest.Wait()
		if got := s.lines(); !slices.Equal(got, []string{"read 7 11"}) {
			t.Fatalf("frames after the quiet period = %v, want the read at once", got)
		}
		_ = client.Close()
	})
}

func TestMarkReadThrottlesEachRoomOnItsOwn(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		s := newReadServer(t)
		client := dialPipe(t, s.fakeServer, "token")
		for _, room := range []string{"7", "8", "7", "8"} {
			_ = client.MarkRead(t.Context(), room, 3)
		}
		synctest.Wait()
		if got := s.lines(); !slices.Equal(got, []string{"read 7 3", "read 8 3"}) {
			t.Fatalf("frames = %v, want the first read of each room", got)
		}
		_ = client.Close()
	})
}

func TestReadSeqZeroMeansEverythingAndWinsOverAnyPendingSeq(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		s := newReadServer(t)
		client := dialPipe(t, s.fakeServer, "token")
		_ = client.MarkRead(t.Context(), "7", 2)
		_ = client.MarkRead(t.Context(), "7", 0)
		_ = client.MarkRead(t.Context(), "7", 9)
		time.Sleep(time.Second)
		synctest.Wait()
		if got := s.lines(); !slices.Equal(got, []string{"read 7 2", "read 7 0"}) {
			t.Fatalf("frames = %v, want the pending read to be everything", got)
		}
		_ = client.Close()
	})
}

func TestCloseRoomSendsThePendingReadFirst(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		s := newReadServer(t)
		client := dialPipe(t, s.fakeServer, "token")
		_ = client.MarkRead(t.Context(), "7", 1)
		_ = client.MarkRead(t.Context(), "7", 5)
		if err := client.CloseRoom(t.Context(), "7"); err != nil {
			t.Fatalf("CloseRoom: %v", err)
		}
		synctest.Wait()
		if got := s.lines(); !slices.Equal(got, []string{"read 7 1", "read 7 5", "close 7"}) {
			t.Fatalf("frames = %v, want the pending read before close_room", got)
		}
		time.Sleep(2 * time.Second)
		synctest.Wait()
		if got := s.lines(); len(got) != 0 {
			t.Fatalf("frames after close_room = %v, want none", got)
		}
		_ = client.Close()
	})
}

func TestMarkUnreadDropsThePendingReadOfItsRoom(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		s := newReadServer(t)
		client := dialPipe(t, s.fakeServer, "token")
		_ = client.MarkRead(t.Context(), "7", 5)
		_ = client.MarkRead(t.Context(), "7", 9)
		_ = client.MarkRead(t.Context(), "8", 4)
		_ = client.MarkRead(t.Context(), "8", 6)
		if _, err := client.MarkUnread(t.Context(), "7", 3); err != nil {
			t.Fatalf("MarkUnread: %v", err)
		}
		time.Sleep(2 * time.Second)
		synctest.Wait()
		if got := s.lines(); !slices.Equal(got, []string{"read 7 5", "read 8 4", "unread 7 3", "read 8 6"}) {
			t.Fatalf("frames = %v, want room 7's pending read dropped and room 8's sent", got)
		}
		_ = client.Close()
	})
}

func TestATickAtTheSameMomentNeverSendsAReadAfterTheUnread(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		s := newReadServer(t)
		client := dialPipe(t, s.fakeServer, "token")
		for i := range 30 {
			room := strconv.Itoa(100 + i)
			_ = client.MarkRead(t.Context(), room, 5)
			_ = client.MarkRead(t.Context(), room, 9)
			time.Sleep(ReadInterval)
			if _, err := client.MarkUnread(t.Context(), room, 2); err != nil {
				t.Fatalf("MarkUnread: %v", err)
			}
			time.Sleep(2 * ReadInterval)
			synctest.Wait()
			got := s.lines()
			unread := slices.Index(got, "unread "+room+" 2")
			if unread < 0 || slices.Contains(got[unread+1:], "read "+room+" 9") {
				t.Fatalf("frames = %v, want no read after the unread", got)
			}
		}
		_ = client.Close()
	})
}

func TestFlushReadsSendsPendingReadsAndWaitsForTheirAnswers(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		s := newReadServer(t)
		client := dialPipe(t, s.fakeServer, "token")
		s.holding(true)
		_ = client.MarkRead(t.Context(), "7", 5)
		_ = client.MarkRead(t.Context(), "7", 9)
		_ = client.MarkRead(t.Context(), "8", 2)
		flushed := make(chan error, 1)
		go func() { flushed <- client.FlushReads(t.Context()) }()
		synctest.Wait()
		got := s.lines()
		slices.Sort(got)
		if !slices.Equal(got, []string{"read 7 5", "read 7 9", "read 8 2"}) {
			t.Fatalf("frames = %v, want every pending read sent", got)
		}
		select {
		case err := <-flushed:
			t.Fatalf("FlushReads returned %v before the read answers", err)
		default:
		}
		s.release()
		if err := <-flushed; err != nil {
			t.Fatalf("FlushReads = %v", err)
		}
		_ = client.Close()
	})
}

func TestFlushReadsGivesUpWhenItsContextEnds(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		s := newReadServer(t)
		client := dialPipe(t, s.fakeServer, "token")
		s.holding(true)
		_ = client.MarkRead(t.Context(), "7", 4)
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		start := time.Now()
		if err := client.FlushReads(ctx); !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("FlushReads = %v, want DeadlineExceeded", err)
		}
		if waited := time.Since(start); waited < 2*time.Second {
			t.Fatalf("waited %v, want the context's 2s", waited)
		}
		s.release()
		_ = client.Close()
	})
}

func TestFlushReadsWithNothingPendingReturnsAtOnce(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		s := newReadServer(t)
		client := dialPipe(t, s.fakeServer, "token")
		if err := client.FlushReads(t.Context()); err != nil {
			t.Fatalf("FlushReads = %v", err)
		}
		if got := s.lines(); len(got) != 0 {
			t.Fatalf("frames = %v, want none", got)
		}
		_ = client.Close()
	})
}

func TestCallNoLongerFlushesReads(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		s := newReadServer(t)
		client := dialPipe(t, s.fakeServer, "token")
		_ = client.MarkRead(t.Context(), "7", 5)
		_ = client.MarkRead(t.Context(), "7", 9)
		synctest.Wait()
		_ = s.lines()
		if _, err := client.Call(t.Context(), listRooms()); err != nil {
			t.Fatalf("Call: %v", err)
		}
		if got := s.lines(); !slices.Equal(got, []string{"list"}) {
			t.Fatalf("frames = %v, want only the call; pending reads wait for their tick or FlushReads", got)
		}
		_ = client.Close()
	})
}

func TestCloseSendsPendingReadsAndWaitsForTheirAnswers(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		s := newReadServer(t)
		client := dialPipe(t, s.fakeServer, "token")
		s.holding(true)
		_ = client.MarkRead(t.Context(), "7", 4)
		_ = client.MarkRead(t.Context(), "7", 6)
		closed := make(chan error, 1)
		go func() { closed <- client.CloseContext(context.Background()) }()
		synctest.Wait()
		select {
		case <-client.Done():
			t.Fatal("closed before the read answers")
		default:
		}
		if got := s.lines(); !slices.Equal(got, []string{"read 7 4", "read 7 6"}) {
			t.Fatalf("frames = %v, want the pending read sent before closing", got)
		}
		s.release()
		if err := <-closed; err != nil {
			t.Fatalf("CloseContext = %v", err)
		}
	})
}

func TestCloseGivesUpWaitingWhenItsContextEnds(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		s := newReadServer(t)
		client := dialPipe(t, s.fakeServer, "token")
		s.holding(true)
		_ = client.MarkRead(t.Context(), "7", 4)
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		start := time.Now()
		if err := client.CloseContext(ctx); !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("CloseContext = %v, want DeadlineExceeded", err)
		}
		if waited := time.Since(start); waited < 2*time.Second {
			t.Fatalf("waited %v, want the context's 2s", waited)
		}
	})
}

func TestFailedThrottledReadsGoToTheErrorHandlerAndClosedClientsRefuseReads(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var mu sync.Mutex
		var failed []string
		s := newReadServer(t)
		s.fail(9, int32(codes.PermissionDenied))
		client := dialPipe(t, s.fakeServer, "token", OnReadError(func(room string, err error) {
			mu.Lock()
			defer mu.Unlock()
			failed = append(failed, room+" "+status.Code(err).String())
		}))
		_ = client.MarkRead(t.Context(), "7", 1)
		_ = client.MarkRead(t.Context(), "7", 9)
		time.Sleep(time.Second)
		synctest.Wait()
		mu.Lock()
		got := slices.Clone(failed)
		mu.Unlock()
		if !slices.Equal(got, []string{"7 PermissionDenied"}) {
			t.Fatalf("read errors = %v, want the throttled read of room 7", got)
		}
		_ = client.Close()
		if err := client.MarkRead(t.Context(), "8", 1); err == nil {
			t.Fatal("MarkRead on a closed client = nil, want an error")
		}
	})
}
