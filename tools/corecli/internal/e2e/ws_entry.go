package e2e

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/lxzan/gws"
	"google.golang.org/protobuf/proto"

	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

const (
	authTimeoutEarly = 500 * time.Millisecond
	authTimeoutLate  = 5 * time.Second
	tokensMode       = 0o600
)

var errNoTokens = errors.New("no token to write")

type WSMessage struct {
	Seq        uint64
	Sender     string
	Text       string
	Ver        uint32
	Emoji      string
	Bookmarked bool
}

func CheckWSMessage(m *chatimv1.Message, want WSMessage) error {
	if m == nil {
		return fmt.Errorf("seq %d not found", want.Seq)
	}
	switch {
	case m.GetSeq() != want.Seq:
		return fmt.Errorf("seq %d, want %d", m.GetSeq(), want.Seq)
	case m.GetSender() != want.Sender:
		return fmt.Errorf("seq %d sender %q, want %q", want.Seq, m.GetSender(), want.Sender)
	case m.GetText() != want.Text:
		return fmt.Errorf("seq %d text %q, want %q", want.Seq, m.GetText(), want.Text)
	case m.GetVer() != want.Ver:
		return fmt.Errorf("seq %d version %d, want %d", want.Seq, m.GetVer(), want.Ver)
	case m.GetMyReaction() != want.Emoji:
		return fmt.Errorf("seq %d my reaction %q, want %q", want.Seq, m.GetMyReaction(), want.Emoji)
	case m.GetBookmarked() != want.Bookmarked:
		return fmt.Errorf("seq %d bookmarked %v, want %v", want.Seq, m.GetBookmarked(), want.Bookmarked)
	}
	return checkOneReaction(m, want)
}

func checkOneReaction(m *chatimv1.Message, want WSMessage) error {
	counts := m.GetReactions().GetCounts()
	if want.Emoji == "" {
		if len(counts) != 0 {
			return fmt.Errorf("seq %d reactions %v, want none", want.Seq, counts)
		}
		return nil
	}
	if len(counts) != 1 || counts[0].GetEmoji() != want.Emoji || counts[0].GetCount() != 1 {
		return fmt.Errorf("seq %d reactions %v, want %s once", want.Seq, counts, want.Emoji)
	}
	return nil
}

func MessageAt(msgs []*chatimv1.Message, seq uint64) *chatimv1.Message {
	for _, m := range msgs {
		if m.GetSeq() == seq {
			return m
		}
	}
	return nil
}

func SameReply(what string, viaWS, viaGRPC proto.Message) error {
	if !proto.Equal(viaWS, viaGRPC) {
		return fmt.Errorf("%s through websocket differs from gRPC:\nwebsocket %v\ngrpc      %v", what, viaWS, viaGRPC)
	}
	return nil
}

func Bookmarked(items []*chatimv1.BookmarkItem, room string, seq uint64) bool {
	for _, it := range items {
		if m := it.GetMessage(); it.GetAvailable() && m.GetRoomId() == room && m.GetSeq() == seq {
			return true
		}
	}
	return false
}

func CloseCode(err error) (uint16, bool) {
	if closeErr, ok := errors.AsType[*gws.CloseError](err); ok {
		return closeErr.Code, true
	}
	return 0, false
}

func CheckAuthTimeout(code uint16, waited, timeout time.Duration) error {
	switch {
	case code != 4002:
		return fmt.Errorf("silent connection closed with code %d, want 4002", code)
	case waited < timeout-authTimeoutEarly:
		return fmt.Errorf("silent connection closed after %v, before the auth timeout %v", waited, timeout)
	case waited > timeout+authTimeoutLate:
		return fmt.Errorf("silent connection closed after %v, long after the auth timeout %v", waited, timeout)
	}
	return nil
}

func WriteTokens(path string, tokens ...string) error {
	if len(tokens) == 0 {
		return errNoTokens
	}
	data := strings.Join(tokens, "\n") + "\n"
	if err := os.WriteFile(filepath.Clean(path), []byte(data), tokensMode); err != nil {
		return fmt.Errorf("write tokens: %w", err)
	}
	return os.Chmod(filepath.Clean(path), tokensMode)
}
