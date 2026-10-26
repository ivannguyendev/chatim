package dpop

import (
	"crypto"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"strings"
	"time"

	"github.com/go-jose/go-jose/v4"
)

const (
	MaxProofLength = 8192
	MaxJTILength   = 64
	proofType      = "dpop+jwt"
)

var (
	ErrInvalidProof   = errors.New("invalid dpop proof")
	allowedAlgorithms = []jose.SignatureAlgorithm{jose.ES256, jose.EdDSA}
)

type Proof struct {
	Thumbprint string
	JTI        string
}

type Verifier struct {
	scheme string
	host   string
	prefix string
	window time.Duration
	now    func() time.Time
}

type proofClaims struct {
	JTI      string          `json:"jti"`
	Method   string          `json:"htm"`
	Target   string          `json:"htu"`
	IssuedAt json.RawMessage `json:"iat"`
}

func NewVerifier(publicURL string, window time.Duration, now func() time.Time) (*Verifier, error) {
	u, err := url.Parse(publicURL)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" || u.RawQuery != "" || u.Fragment != "" {
		return nil, fmt.Errorf("dpop: public url %q must be an absolute http(s) url without query", publicURL)
	}
	if window <= 0 {
		return nil, errors.New("dpop: window must be positive")
	}
	scheme, host := normalizeOrigin(u)
	return &Verifier{scheme: scheme, host: host, prefix: strings.TrimSuffix(u.Path, "/"), window: window, now: now}, nil
}

func (v *Verifier) Verify(proof, method, path string) (Proof, error) {
	if proof == "" || len(proof) > MaxProofLength {
		return Proof{}, ErrInvalidProof
	}
	jws, err := jose.ParseSignedCompact(proof, allowedAlgorithms)
	if err != nil || len(jws.Signatures) != 1 {
		return Proof{}, ErrInvalidProof
	}
	header := jws.Signatures[0].Protected
	jwk := header.JSONWebKey
	if typ, _ := header.ExtraHeaders[jose.HeaderType].(string); typ != proofType || jwk == nil || !jwk.IsPublic() || !jwk.Valid() {
		return Proof{}, ErrInvalidProof
	}
	payload, err := jws.Verify(jwk)
	if err != nil {
		return Proof{}, ErrInvalidProof
	}
	var claims proofClaims
	if err := json.Unmarshal(payload, &claims); err != nil {
		return Proof{}, ErrInvalidProof
	}
	if !v.claimsValid(claims, method, path) {
		return Proof{}, ErrInvalidProof
	}
	thumbprint, err := Thumbprint(jwk)
	if err != nil {
		return Proof{}, ErrInvalidProof
	}
	return Proof{Thumbprint: thumbprint, JTI: claims.JTI}, nil
}

func Thumbprint(jwk *jose.JSONWebKey) (string, error) {
	sum, err := jwk.Thumbprint(crypto.SHA256)
	if err != nil {
		return "", fmt.Errorf("thumbprint: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(sum), nil
}

func (v *Verifier) claimsValid(c proofClaims, method, path string) bool {
	if c.JTI == "" || len(c.JTI) > MaxJTILength || c.Method != method {
		return false
	}
	var iat int64
	if err := json.Unmarshal(c.IssuedAt, &iat); err != nil {
		return false
	}
	issued := time.Unix(iat, 0)
	now := v.now()
	if issued.Before(now.Add(-v.window)) || issued.After(now.Add(v.window)) {
		return false
	}
	return v.targetMatches(c.Target, path)
}

func (v *Verifier) targetMatches(target, path string) bool {
	u, err := url.Parse(target)
	if err != nil || u.Host == "" {
		return false
	}
	scheme, host := normalizeOrigin(u)
	return scheme == v.scheme && host == v.host && u.Path == v.prefix+path
}

func normalizeOrigin(u *url.URL) (string, string) {
	scheme := strings.ToLower(u.Scheme)
	host := strings.ToLower(u.Hostname())
	port := u.Port()
	if port == "" || (scheme == "https" && port == "443") || (scheme == "http" && port == "80") {
		return scheme, host
	}
	return scheme, net.JoinHostPort(host, port)
}
