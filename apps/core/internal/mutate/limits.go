package mutate

import (
	"cmp"
	"fmt"

	"github.com/ivannguyendev/chatim/pkg/apperr"
)

const (
	DefaultMaxEmojis = 20
	MaxEmojisCap     = 100
	DefaultPinLimit  = 50
	MaxPinLimit      = 1000
	FastTouchTries   = 3
)

type Limits struct {
	MaxEmojis int
	PinLimit  int
}

func (l Limits) Validate() error { return l.withDefaults().validate() }

func (l Limits) withDefaults() Limits {
	l.MaxEmojis = cmp.Or(l.MaxEmojis, DefaultMaxEmojis)
	l.PinLimit = cmp.Or(l.PinLimit, DefaultPinLimit)
	return l
}

func (l Limits) validate() error {
	if l.MaxEmojis < 1 || l.MaxEmojis > MaxEmojisCap || l.PinLimit < 1 || l.PinLimit > MaxPinLimit {
		return fmt.Errorf("%w: limits %+v need 1 to %d emojis per message and 1 to %d pins per room", apperr.ErrInvalidArgument, l, MaxEmojisCap, MaxPinLimit)
	}
	return nil
}
