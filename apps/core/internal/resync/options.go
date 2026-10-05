package resync

import (
	"flag"
	"fmt"
	"io"
	"time"

	"github.com/ivannguyendev/chatim/pkg/apperr"
	"github.com/ivannguyendev/chatim/pkg/ids"
)

const (
	DefaultRate = 500
	MaxRate     = 10000
)

var ErrUsage = fmt.Errorf("%w: resync usage", apperr.ErrInvalidArgument)

type Options struct {
	From, To time.Time
	Tenant   string
	Room     uint64
	Rate     int
	DryRun   bool
}

func ParseArgs(args []string, stderr io.Writer) (Options, error) {
	fs := flag.NewFlagSet("resync", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var o Options
	var from, to, room string
	fs.StringVar(&from, "from", "", "start of the lost range, RFC3339")
	fs.StringVar(&to, "to", "", "end of the lost range, RFC3339")
	fs.StringVar(&o.Tenant, "tenant", "", "only rooms of this tenant")
	fs.StringVar(&room, "room", "", "only this room id")
	fs.IntVar(&o.Rate, "rate", DefaultRate, "work records published per second")
	fs.BoolVar(&o.DryRun, "dry-run", false, "count the records without publishing them")
	if err := fs.Parse(args); err != nil {
		return Options{}, fmt.Errorf("%w: %w", ErrUsage, err)
	}
	if fs.NArg() > 0 {
		return Options{}, fmt.Errorf("%w: unexpected arguments %v", ErrUsage, fs.Args())
	}
	var err error
	if o.From, err = time.Parse(time.RFC3339, from); err != nil {
		return Options{}, fmt.Errorf("%w: -from: %w", ErrUsage, err)
	}
	if o.To, err = time.Parse(time.RFC3339, to); err != nil {
		return Options{}, fmt.Errorf("%w: -to: %w", ErrUsage, err)
	}
	if room != "" {
		if o.Room, err = ids.ParseRoomID(room); err != nil {
			return Options{}, fmt.Errorf("%w: -room: %w", ErrUsage, err)
		}
	}
	return o, o.Validate()
}

func (o Options) Validate() error {
	switch {
	case !o.From.Before(o.To):
		return fmt.Errorf("%w: -from must be before -to", ErrUsage)
	case o.Rate < 1 || o.Rate > MaxRate:
		return fmt.Errorf("%w: -rate must be in 1..%d", ErrUsage, MaxRate)
	default:
		return nil
	}
}
