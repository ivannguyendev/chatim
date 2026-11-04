package mongostore

import (
	"testing"
	"time"
)

func testOptions(uri, database string) Options {
	return Options{
		URI: uri, Database: database, AppName: "chatim-identity-it", RevocationKeep: time.Hour,
		JITWriters: 4, JITWindow: 2 * time.Millisecond, JITMaxBatch: 256, JITQueue: 1024, JITInsertTimeout: time.Second,
	}
}

func TestOpenRejectsNonPositiveRevocationKeep(t *testing.T) {
	for _, keep := range []time.Duration{0, -time.Second} {
		opts := testOptions("mongodb://db:27017", "chatim_identity")
		opts.RevocationKeep = keep
		if _, err := Open(opts); err == nil {
			t.Fatalf("Open with RevocationKeep %v = nil; want an error", keep)
		}
	}
}

func TestOpenRejectsNonPositiveJITSettings(t *testing.T) {
	cases := map[string]func(*Options){
		"writers":        func(o *Options) { o.JITWriters = 0 },
		"window":         func(o *Options) { o.JITWindow = 0 },
		"max batch":      func(o *Options) { o.JITMaxBatch = -1 },
		"queue":          func(o *Options) { o.JITQueue = 0 },
		"insert timeout": func(o *Options) { o.JITInsertTimeout = -time.Second },
	}
	for name, change := range cases {
		t.Run(name, func(t *testing.T) {
			opts := testOptions("mongodb://db:27017", "chatim_identity")
			change(&opts)
			if _, err := Open(opts); err == nil {
				t.Fatalf("Open with a non-positive %s = nil; want an error", name)
			}
		})
	}
}
