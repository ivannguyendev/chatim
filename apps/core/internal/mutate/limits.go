package mutate

import (
	"cmp"
	"fmt"
	"slices"

	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
	"github.com/ivannguyendev/chatim/pkg/apperr"
)

const (
	MaxEmojiList    = 100
	DefaultPinLimit = 50
	MaxPinLimit     = 1000
	FastTouchTries  = 3
)

var DefaultEmojis = []string{"👍", "❤️", "😂", "😮", "😢", "🙏"}

type Limits struct {
	Emojis   []string
	PinLimit int
}

func (l Limits) Validate() error { return l.withDefaults().validate() }

func (l Limits) withDefaults() Limits {
	if l.Emojis == nil {
		l.Emojis = DefaultEmojis
	}
	l.Emojis = slices.Clone(l.Emojis)
	l.PinLimit = cmp.Or(l.PinLimit, DefaultPinLimit)
	return l
}

func (l Limits) validate() error {
	if n := len(l.Emojis); n < 1 || n > MaxEmojiList {
		return fmt.Errorf("%w: need 1 to %d reaction emojis, got %d", apperr.ErrInvalidArgument, MaxEmojiList, n)
	}
	for i, e := range l.Emojis {
		if err := domain.ValidateEmoji(e); err != nil {
			return fmt.Errorf("reaction emoji %q: %w", e, err)
		}
		if slices.Contains(l.Emojis[:i], e) {
			return fmt.Errorf("%w: reaction emoji %q listed twice", apperr.ErrInvalidArgument, e)
		}
	}
	if l.PinLimit < 1 || l.PinLimit > MaxPinLimit {
		return fmt.Errorf("%w: pin limit %d must be 1 to %d", apperr.ErrInvalidArgument, l.PinLimit, MaxPinLimit)
	}
	return nil
}
