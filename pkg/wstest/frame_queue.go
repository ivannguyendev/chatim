package wstest

import (
	"sync"

	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

type frameQueue struct {
	mu     sync.Mutex
	items  []*chatimv1.ServerFrame
	ended  bool
	signal chan struct{}
	out    chan *chatimv1.ServerFrame
}

func newFrameQueue() *frameQueue {
	return &frameQueue{signal: make(chan struct{}, 1), out: make(chan *chatimv1.ServerFrame)}
}

func (q *frameQueue) push(frame *chatimv1.ServerFrame) {
	q.mu.Lock()
	q.items = append(q.items, frame)
	q.mu.Unlock()
	q.wake()
}

func (q *frameQueue) end() {
	q.mu.Lock()
	q.ended = true
	q.mu.Unlock()
	q.wake()
}

func (q *frameQueue) wake() {
	select {
	case q.signal <- struct{}{}:
	default:
	}
}

func (q *frameQueue) next(stop <-chan struct{}) (*chatimv1.ServerFrame, bool) {
	for {
		q.mu.Lock()
		if len(q.items) > 0 {
			frame := q.items[0]
			q.items[0] = nil
			q.items = q.items[1:]
			q.mu.Unlock()
			return frame, true
		}
		ended := q.ended
		q.mu.Unlock()
		if ended {
			return nil, false
		}
		select {
		case <-q.signal:
		case <-stop:
			return nil, false
		}
	}
}

func (q *frameQueue) run(stop <-chan struct{}) {
	defer close(q.out)
	for {
		frame, ok := q.next(stop)
		if !ok {
			return
		}
		select {
		case q.out <- frame:
		case <-stop:
			return
		}
	}
}
