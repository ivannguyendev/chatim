package e2e

import (
	"time"

	"github.com/ivannguyendev/chatim/pkg/backoff"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

type wakeup struct{ timer *time.Timer }

func (w *wakeup) set(wait time.Duration) {
	if w.timer == nil {
		w.timer = time.NewTimer(wait)
		return
	}
	w.timer.Reset(wait)
}

func (w *wakeup) stop() {
	if w.timer != nil {
		w.timer.Stop()
	}
}

func (w *wakeup) due() <-chan time.Time {
	if w.timer == nil {
		return nil
	}
	return w.timer.C
}

func (f *WSFeed) Stats() EventStats { return f.events.Stats() }

func (f *WSFeed) Run(frames <-chan *chatimv1.ServerFrame, loadRooms func() error) {
	var typing, load wakeup
	defer typing.stop()
	defer load.stop()
	for {
		select {
		case frame, ok := <-frames:
			if !ok {
				return
			}
			f.apply(frame, &load)
		case <-typing.due():
			for _, who := range f.typing.Expire(time.Now()) {
				f.Typing(who.Room, who.User, false)
			}
		case <-load.due():
			_ = loadRooms()
		}
		if next, ok := f.typing.Next(); ok {
			typing.set(time.Until(next))
		} else {
			typing.stop()
		}
	}
}

func (f *WSFeed) apply(frame *chatimv1.ServerFrame, load *wakeup) {
	switch fr := frame.GetFrame().(type) {
	case *chatimv1.ServerFrame_Event:
		f.applyEvent(fr.Event)
	case *chatimv1.ServerFrame_Notification:
		f.Notification(fr.Notification)
	case *chatimv1.ServerFrame_Ready:
		load.stop()
		f.Ready()
	case *chatimv1.ServerFrame_Resync:
		load.stop()
		f.Resync(fr.Resync.GetReason())
	case *chatimv1.ServerFrame_RoomClosed:
		f.RoomClosed(fr.RoomClosed.GetRoomId(), fr.RoomClosed.GetReason())
	case *chatimv1.ServerFrame_LoadFailed:
		retry := time.Duration(fr.LoadFailed.GetRetryAfterMs()) * time.Millisecond
		load.set(retry + backoff.Jitter(retry/2))
	case *chatimv1.ServerFrame_Typing:
		f.applyTyping(fr.Typing)
	case *chatimv1.ServerFrame_Presence:
		f.Presence(fr.Presence.GetEntries())
	case *chatimv1.ServerFrame_AuthRequired:
		f.AuthRequired(fr.AuthRequired.GetReason())
	case *chatimv1.ServerFrame_FrameError:
		f.FrameError(fr.FrameError.GetReason())
	case *chatimv1.ServerFrame_RetryLater:
		f.RetryLater(time.Duration(fr.RetryLater.GetRetryAfterMs()) * time.Millisecond)
	}
}

func (f *WSFeed) applyEvent(ev *chatimv1.Event) {
	fresh, gap := f.events.Apply(ev)
	if !fresh {
		return
	}
	if !gap.Empty() {
		f.Gap(gap)
	}
	if ev.GetMessageCreated() != nil && f.typing.End(Typist{Room: ev.GetRoomId(), User: ev.GetActor()}) {
		f.Typing(ev.GetRoomId(), ev.GetActor(), false)
	}
	f.Event(ev)
}

func (f *WSFeed) applyTyping(n *chatimv1.TypingNotice) {
	who := Typist{Room: n.GetRoomId(), User: n.GetUserId()}
	if n.GetStopped() {
		if f.typing.End(who) {
			f.Typing(who.Room, who.User, false)
		}
		return
	}
	f.typing.Start(who, time.Now())
	f.Typing(who.Room, who.User, true)
}
