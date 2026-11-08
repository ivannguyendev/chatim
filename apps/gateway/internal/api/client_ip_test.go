package api_test

import (
	"net/http"
	"net/netip"
	"testing"

	"github.com/ivannguyendev/chatim/apps/gateway/internal/api"
)

func TestClientIPTrustsTheHeaderOnlyFromKnownProxies(t *testing.T) {
	ip := api.ClientIP{Header: "X-Forwarded-For", Trusted: []netip.Prefix{netip.MustParsePrefix("10.0.0.0/8")}}
	cases := []struct {
		name, remote string
		forwarded    []string
		want         string
	}{
		{"direct client, header ignored", "203.0.113.7:5000", []string{"1.2.3.4"}, "203.0.113.7"},
		{"behind the load balancer", "10.0.0.5:5000", []string{"198.51.100.9"}, "198.51.100.9"},
		{"two trusted hops", "10.0.0.5:5000", []string{"198.51.100.9, 10.1.1.1"}, "198.51.100.9"},
		{"client cannot forge past the last untrusted hop", "10.0.0.5:5000", []string{"1.1.1.1, 198.51.100.9"}, "198.51.100.9"},
		{"repeated headers join", "10.0.0.5:5000", []string{"1.1.1.1", "198.51.100.9"}, "198.51.100.9"},
		{"no header from the proxy", "10.0.0.5:5000", nil, "10.0.0.5"},
		{"garbage in the header", "10.0.0.5:5000", []string{"not-an-ip"}, "10.0.0.5"},
		{"ipv6 client", "10.0.0.5:5000", []string{"2001:db8::1"}, "2001:db8::1"},
	}
	for _, c := range cases {
		r := &http.Request{RemoteAddr: c.remote, Header: http.Header{}}
		for _, v := range c.forwarded {
			r.Header.Add("X-Forwarded-For", v)
		}
		if got := ip.Of(r); got != c.want {
			t.Errorf("%s: got %q, want %q", c.name, got, c.want)
		}
	}
	if got := (api.ClientIP{}).Of(&http.Request{RemoteAddr: "203.0.113.7:1", Header: http.Header{"X-Forwarded-For": {"1.2.3.4"}}}); got != "203.0.113.7" {
		t.Errorf("no trusted proxies: %q", got)
	}
}
