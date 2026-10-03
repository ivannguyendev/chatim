package admin

import (
	"log/slog"
	"net/http"
	"testing"
	"time"
)

func TestConfigDefaults(t *testing.T) {
	hardened := Config{
		Addr:              ":9090",
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      65 * time.Second,
		IdleTimeout:       90 * time.Second,
		MaxHeaderBytes:    64 << 10,
		ShutdownTimeout:   10 * time.Second,
	}
	explicit := Config{
		Addr:              "127.0.0.1:9999",
		ReadHeaderTimeout: time.Second,
		ReadTimeout:       2 * time.Second,
		WriteTimeout:      90 * time.Second,
		IdleTimeout:       30 * time.Second,
		MaxHeaderBytes:    1 << 10,
		ShutdownTimeout:   2 * time.Second,
	}
	negative := Config{
		ReadHeaderTimeout: -1,
		ReadTimeout:       -1,
		WriteTimeout:      -1,
		IdleTimeout:       -1,
		MaxHeaderBytes:    -1,
		ShutdownTimeout:   -1,
	}
	shortWrite := hardened
	shortWrite.WriteTimeout = 10 * time.Second

	tests := []struct {
		name string
		in   Config
		want Config
	}{
		{"zero value gets hardened defaults", Config{}, hardened},
		{"negative values fall back to defaults", negative, hardened},
		{"write timeout never cuts a pprof profile short", shortWrite, hardened},
		{"explicit values are kept", explicit, explicit},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.in.withDefaults(); got != tt.want {
				t.Errorf("withDefaults() = %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestNewHardensHTTPServer(t *testing.T) {
	s := New(Config{}, slog.New(slog.DiscardHandler))
	cfg := Config{}.withDefaults()

	if s.srv.Handler == nil || s.srv.Handler == http.DefaultServeMux {
		t.Fatal("admin server must use its own ServeMux, never http.DefaultServeMux")
	}
	if s.srv.ErrorLog == nil {
		t.Error("ErrorLog is nil; net/http errors would bypass the structured log")
	}
	got := Config{
		Addr:              s.srv.Addr,
		ReadHeaderTimeout: s.srv.ReadHeaderTimeout,
		ReadTimeout:       s.srv.ReadTimeout,
		WriteTimeout:      s.srv.WriteTimeout,
		IdleTimeout:       s.srv.IdleTimeout,
		MaxHeaderBytes:    s.srv.MaxHeaderBytes,
		ShutdownTimeout:   s.cfg.ShutdownTimeout,
	}
	if got != cfg {
		t.Errorf("http.Server limits = %+v, want %+v", got, cfg)
	}
}
