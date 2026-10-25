package typing

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"sync"
	"sync/atomic"

	"google.golang.org/protobuf/proto"

	"github.com/ivannguyendev/chatim/apps/websocket/internal/interest"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

const (
	subjectRoot    = "eph"
	subjectKind    = "typing"
	Subject        = subjectRoot + ".*.*." + subjectKind
	originHexBytes = 4
)

var (
	errNoViewers    = errors.New("typing: viewers are required")
	errNoOrigin     = errors.New("typing: origin is required")
	errNoQueue      = errors.New("typing: queue must be positive")
	errNoRelay      = errors.New("typing: relay is required")
	errStartedTwice = errors.New("typing: already started")
)

type Relay interface {
	Publish(subject string, data []byte) error
	Subscribe(subject string, handler func(subject string, data []byte)) (func() error, error)
}

type Viewers interface {
	Signal(tenant string, room uint64, frame []byte, skipUser string) (delivered, dropped int)
}

type Config struct {
	Origin string
	Queue  int
}

type inbound struct {
	subject string
	data    []byte
}

type Service struct {
	cfg     Config
	viewers Viewers
	stats   stats
	relay   atomic.Pointer[Relay]
	inbox   chan inbound

	mu      sync.Mutex
	started bool
	unsub   func() error
	quit    chan struct{}
	done    chan struct{}
}

func NewOrigin(node string) (string, error) {
	b := make([]byte, originHexBytes)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("typing origin: %w", err)
	}
	return node + ":" + hex.EncodeToString(b), nil
}

func New(cfg Config, viewers Viewers) (*Service, error) {
	switch {
	case viewers == nil:
		return nil, errNoViewers
	case cfg.Origin == "":
		return nil, errNoOrigin
	case cfg.Queue <= 0:
		return nil, errNoQueue
	}
	return &Service{cfg: cfg, viewers: viewers, inbox: make(chan inbound, cfg.Queue)}, nil
}

func (s *Service) Start(relay Relay) error {
	if relay == nil {
		return errNoRelay
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.started {
		return errStartedTwice
	}
	unsub, err := relay.Subscribe(Subject, s.FromNode)
	if err != nil {
		return fmt.Errorf("typing subscribe %s: %w", Subject, err)
	}
	s.started, s.unsub = true, unsub
	s.quit, s.done = make(chan struct{}), make(chan struct{})
	s.relay.Store(&relay)
	go s.work(s.quit, s.done)
	return nil
}

func (s *Service) Stop() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.started || s.quit == nil {
		return nil
	}
	s.relay.Store(nil)
	err := s.unsub()
	close(s.quit)
	<-s.done
	s.quit = nil
	if err != nil {
		return fmt.Errorf("typing unsubscribe: %w", err)
	}
	return nil
}

func (s *Service) RateLimited() {
	s.stats.received.Add(1)
	s.stats.drop(dropRate)
}

func (s *Service) FromClient(conn *interest.Conn, tenant, user string, f *chatimv1.TypingFrame) {
	s.stats.received.Add(1)
	if conn == nil {
		s.stats.drop(dropNotMember)
		return
	}
	room, ok := conn.SignalRoom(f.GetRoomId())
	if !ok {
		s.stats.drop(dropNotMember)
		return
	}
	s.deliver(tenant, room, user, f.GetStopped())
	s.relayOut(tenant, room, user, f.GetStopped())
}

func (s *Service) FromNode(subject string, data []byte) {
	s.stats.received.Add(1)
	select {
	case s.inbox <- inbound{subject: subject, data: data}:
	default:
		s.stats.drop(dropQueueFull)
	}
}

func (s *Service) work(quit, done chan struct{}) {
	defer close(done)
	for {
		select {
		case <-quit:
			return
		case in := <-s.inbox:
			s.handle(in)
		}
	}
}

func (s *Service) handle(in inbound) {
	sig, ok := decodeSignal(in)
	if !ok {
		s.stats.drop(dropMalformed)
		return
	}
	if sig.GetOrigin() == s.cfg.Origin {
		return
	}
	s.deliver(sig.GetTenant(), sig.GetRoomId(), sig.GetUserId(), sig.GetStopped())
}

func (s *Service) deliver(tenant string, room uint64, user string, stopped bool) {
	notice := &chatimv1.TypingNotice{RoomId: strconv.FormatUint(room, 10), UserId: user, Stopped: stopped}
	frame, err := proto.Marshal(&chatimv1.ServerFrame{Frame: &chatimv1.ServerFrame_Typing{Typing: notice}})
	if err != nil {
		s.stats.drop(dropMalformed)
		return
	}
	delivered, dropped := s.viewers.Signal(tenant, room, frame, user)
	addCount(&s.stats.delivered, delivered)
	s.stats.dropN(dropSendFull, dropped)
}

func (s *Service) relayOut(tenant string, room uint64, user string, stopped bool) {
	r := s.relay.Load()
	if r == nil {
		return
	}
	data, err := proto.Marshal(&chatimv1.TypingSignal{Origin: s.cfg.Origin, Tenant: tenant, RoomId: room, UserId: user, Stopped: stopped})
	if err == nil {
		err = (*r).Publish(subjectOf(tenant, room), data)
	}
	if err != nil {
		s.stats.relayFailures.Add(1)
	}
}

func subjectOf(tenant string, room uint64) string {
	return subjectRoot + "." + tenant + "." + strconv.FormatUint(room, 10) + "." + subjectKind
}
