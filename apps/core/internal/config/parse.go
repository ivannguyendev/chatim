package config

import (
	"fmt"
	"net"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/model/domain"
	"github.com/ivannguyendev/chatim/pkg/envconfig"
)

type parser struct {
	errs []error
}

func (p *parser) fail(err error) { p.errs = append(p.errs, err) }

func (p *parser) coreID() string {
	if id := os.Getenv("CORE_ID"); id != "" {
		return id
	}
	host, err := os.Hostname()
	if err != nil {
		p.fail(fmt.Errorf("CORE_ID unset and hostname unavailable: %w", err))
	}
	return host
}

func (p *parser) advertiseAddr(coreID, grpcAddr string) string {
	_, port, err := net.SplitHostPort(grpcAddr)
	if err != nil {
		p.fail(fmt.Errorf("CORE_GRPC_ADDR=%q: %w", grpcAddr, err))
	}
	if v := os.Getenv("CORE_ADVERTISE_ADDR"); v != "" {
		return v
	}
	if err != nil {
		return ""
	}
	return net.JoinHostPort(coreID, port)
}

func (p *parser) count(key string, def int) int {
	v, err := envconfig.Int(key, def)
	switch {
	case err != nil:
		p.fail(err)
	case v <= 0:
		p.fail(fmt.Errorf("%s=%d: must be positive", key, v))
	}
	return v
}

func (p *parser) index(key string, def int) int {
	v, err := envconfig.Int(key, def)
	switch {
	case err != nil:
		p.fail(err)
	case v < 0:
		p.fail(fmt.Errorf("%s=%d: must not be negative", key, v))
	}
	return v
}

func (p *parser) span(key string, def time.Duration) time.Duration {
	v, err := envconfig.Duration(key, def)
	switch {
	case err != nil:
		p.fail(err)
	case v <= 0 && os.Getenv(key) != "":
		p.fail(fmt.Errorf("%s=%v: must be positive", key, v))
	}
	return v
}

func (p *parser) flag(key string, def bool) bool {
	v, err := envconfig.Bool(key, def)
	if err != nil {
		p.fail(err)
	}
	return v
}

func (p *parser) list(key string) []string {
	var items []string
	for item := range strings.SplitSeq(os.Getenv(key), ",") {
		if item = strings.TrimSpace(item); item != "" {
			items = append(items, item)
		}
	}
	return items
}

func (p *parser) listOr(key string, def []string) []string {
	if items := p.list(key); len(items) > 0 {
		return items
	}
	return slices.Clone(def)
}

func (p *parser) kinds(key string) []domain.Kind {
	var kinds []domain.Kind
	for _, name := range p.list(key) {
		k, err := domain.ParseKind(name)
		if err != nil {
			p.fail(fmt.Errorf("%s: %w", key, err))
			continue
		}
		kinds = append(kinds, k)
	}
	return kinds
}

func (p *parser) secret(key string) string {
	fileKey := key + "_FILE"
	path := os.Getenv(fileKey)
	if path == "" {
		return os.Getenv(key)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		p.fail(fmt.Errorf("%s: %w", fileKey, err))
		return ""
	}
	return strings.TrimRight(string(b), "\r\n")
}
