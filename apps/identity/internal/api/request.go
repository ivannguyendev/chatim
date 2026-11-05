package api

import (
	"errors"
	"net/http"
	"strings"

	"connectrpc.com/connect"
)

const bearerPrefix = "bearer "

type headered interface {
	Header() http.Header
}

func bearerToken(h http.Header) (string, bool) {
	values := h.Values("Authorization")
	if len(values) != 1 || len(values[0]) <= len(bearerPrefix) || !strings.EqualFold(values[0][:len(bearerPrefix)], bearerPrefix) {
		return "", false
	}
	return values[0][len(bearerPrefix):], true
}

func errUnauthenticated() *connect.Error {
	return connect.NewError(connect.CodeUnauthenticated, errors.New("unauthenticated"))
}
