package grpcclient

import (
	"errors"
	"fmt"
	"slices"

	"google.golang.org/grpc"
	"google.golang.org/grpc/resolver"
	"google.golang.org/grpc/resolver/manual"
)

const staticScheme = "chatim-static"

func NewStatic(addrs []string, opts Options) (*grpc.ClientConn, error) {
	if len(addrs) == 0 {
		return nil, errors.New("grpcclient: at least one address is required")
	}
	state := resolver.State{Addresses: make([]resolver.Address, 0, len(addrs))}
	for i, addr := range addrs {
		if addr == "" || slices.Contains(addrs[:i], addr) {
			return nil, fmt.Errorf("grpcclient: address %q is empty or repeated", addr)
		}
		state.Addresses = append(state.Addresses, resolver.Address{Addr: addr})
	}
	r := manual.NewBuilderWithScheme(staticScheme)
	r.InitialState(state)
	opts.DialOptions = append(slices.Clone(opts.DialOptions), grpc.WithResolvers(r))
	return New(staticScheme+":///cores", opts)
}
