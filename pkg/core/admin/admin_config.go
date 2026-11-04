package admin

import (
	"cmp"
	"net/http"
	"time"
)

const minWriteTimeout = 65 * time.Second

type Config struct {
	Addr              string
	ReadHeaderTimeout time.Duration
	ReadTimeout       time.Duration
	WriteTimeout      time.Duration
	IdleTimeout       time.Duration
	MaxHeaderBytes    int
	ShutdownTimeout   time.Duration
	Metrics           http.Handler
}

func (c Config) withDefaults() Config {
	c.Addr = cmp.Or(c.Addr, ":9090")
	c.ReadHeaderTimeout = orDefault(c.ReadHeaderTimeout, 5*time.Second)
	c.ReadTimeout = orDefault(c.ReadTimeout, 10*time.Second)
	c.WriteTimeout = max(c.WriteTimeout, minWriteTimeout)
	c.IdleTimeout = orDefault(c.IdleTimeout, 90*time.Second)
	c.MaxHeaderBytes = orDefault(c.MaxHeaderBytes, 64<<10)
	c.ShutdownTimeout = orDefault(c.ShutdownTimeout, 10*time.Second)
	return c
}

func orDefault[T int | time.Duration](v, def T) T {
	if v <= 0 {
		return def
	}
	return v
}
