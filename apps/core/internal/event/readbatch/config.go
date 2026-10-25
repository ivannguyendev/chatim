package readbatch

import (
	"cmp"
	"fmt"
	"time"

	"github.com/ivannguyendev/chatim/pkg/apperr"
)

const (
	DefaultWindow = time.Second
	MinWindow     = 100 * time.Millisecond
	MaxWindow     = 3 * time.Second
	PageSize      = 1000
)

type Config struct {
	Window time.Duration
}

func (c Config) Validate() error {
	if c.Window < 0 {
		return fmt.Errorf("%w: read event window %v must not be negative", apperr.ErrInvalidArgument, c.Window)
	}
	return c.withDefaults().validate()
}

func (c Config) withDefaults() Config {
	c.Window = cmp.Or(c.Window, DefaultWindow)
	return c
}

func (c Config) validate() error {
	if c.Window < MinWindow || c.Window > MaxWindow {
		return fmt.Errorf("%w: read event window %v must be within %v..%v", apperr.ErrInvalidArgument, c.Window, MinWindow, MaxWindow)
	}
	return nil
}
