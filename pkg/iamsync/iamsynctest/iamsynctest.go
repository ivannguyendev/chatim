package iamsynctest

import (
	"bytes"
	"io"
	"net/http"
	"strings"

	"github.com/ivannguyendev/chatim/pkg/iamsync"
)

const (
	InternalURL = "http://iam.test:8081"
	JWKSURL     = "http://iam.test:8080/.well-known/jwks.json"
	Secret      = "node-secret-value-0123456789abcdef"
)

func Config() iamsync.Config {
	c := iamsync.Defaults()
	c.InternalURL, c.JWKSURL, c.Secret, c.AllowHTTP = InternalURL, JWKSURL, Secret, true
	return c
}

type Accepting struct{}

func (Accepting) Do(req *http.Request) (*http.Response, error) {
	body := `{"keys":[]}`
	if strings.HasPrefix(req.URL.String(), InternalURL) {
		body = `{"sessions":[],"tenants":[]}`
	}
	return answer(http.StatusOK, body), nil
}

type Refusing struct{ Status int }

func (r Refusing) Do(*http.Request) (*http.Response, error) {
	return answer(r.Status, ""), nil
}

func answer(status int, body string) *http.Response {
	return &http.Response{StatusCode: status, Header: http.Header{}, Body: io.NopCloser(bytes.NewReader([]byte(body)))}
}
