package wsclient

import chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"

const DefaultDispatchQueue = 1024

type Option func(*options)

type options struct {
	applier       *Applier
	dispatchQueue int
	onEvent       func(*chatimv1.Event)
	onGap         func(*Client, Gap)
	onNotify      func(*chatimv1.Notification)
	onReady       func()
	onResync      func(chatimv1.ResyncReason)
	onRoomClosed  func(string, chatimv1.RoomClosedReason)
}

func WithApplier(applier *Applier) Option {
	return func(o *options) { o.applier = applier }
}

func WithDispatchQueue(frames int) Option {
	return func(o *options) { o.dispatchQueue = frames }
}

func OnEvent(handle func(*chatimv1.Event)) Option {
	return func(o *options) { o.onEvent = handle }
}

func OnGap(fill func(*Client, Gap)) Option {
	return func(o *options) { o.onGap = fill }
}

func OnNotification(handle func(*chatimv1.Notification)) Option {
	return func(o *options) { o.onNotify = handle }
}

func OnReady(handle func()) Option {
	return func(o *options) { o.onReady = handle }
}

func OnResync(handle func(chatimv1.ResyncReason)) Option {
	return func(o *options) { o.onResync = handle }
}

func OnRoomClosed(handle func(roomID string, reason chatimv1.RoomClosedReason)) Option {
	return func(o *options) { o.onRoomClosed = handle }
}

func buildOptions(opts []Option) options {
	o := options{dispatchQueue: DefaultDispatchQueue}
	for _, opt := range opts {
		opt(&o)
	}
	if o.applier == nil {
		o.applier = NewApplier(DefaultRecentIDs)
	}
	if o.dispatchQueue <= 0 {
		o.dispatchQueue = DefaultDispatchQueue
	}
	return o
}
