package api

import (
	"net"
	"net/http"
	"net/netip"
	"slices"
	"strings"
)

type ClientIP struct {
	Header  string
	Trusted []netip.Prefix
}

func (c ClientIP) Of(r *http.Request) string {
	remote := remoteAddr(r.RemoteAddr)
	if !c.trusted(remote) || c.Header == "" {
		return remote.String()
	}
	hops := r.Header.Values(c.Header)
	var chain []string
	for _, h := range hops {
		for part := range strings.SplitSeq(h, ",") {
			chain = append(chain, strings.TrimSpace(part))
		}
	}
	for _, hop := range slices.Backward(chain) {
		addr, err := netip.ParseAddr(hop)
		if err != nil {
			return remote.String()
		}
		if !c.trusted(addr.Unmap()) {
			return addr.Unmap().String()
		}
	}
	return remote.String()
}

func (c ClientIP) trusted(addr netip.Addr) bool {
	return slices.ContainsFunc(c.Trusted, func(p netip.Prefix) bool { return p.Contains(addr) })
}

func remoteAddr(hostport string) netip.Addr {
	host, _, err := net.SplitHostPort(hostport)
	if err != nil {
		host = hostport
	}
	addr, err := netip.ParseAddr(host)
	if err != nil {
		return netip.Addr{}
	}
	return addr.Unmap()
}
