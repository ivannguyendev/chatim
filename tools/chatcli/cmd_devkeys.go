package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/ivannguyendev/chatim/tools/internal/devtoken"
)

const (
	devIssuer   = "chatim-dev"
	devAudience = "chatim-websocket"
	devTokenTTL = 15 * time.Minute
)

var (
	errOutRequired = errors.New("-out is required")
	errKeyRequired = errors.New("-key is required")
	errCallerEmpty = errors.New("-tenant and -user are required")
	errTTLPositive = errors.New("-ttl must be positive")
)

func devKeysCmd(_ context.Context, args []string) error {
	fs := flag.NewFlagSet("dev-keys", flag.ContinueOnError)
	out := fs.String("out", "", "directory for "+devtoken.PrivateFile+" (0600) and the public "+devtoken.PublicFile+" (0644)")
	kid := fs.String("kid", "dev-1", "key id written in both files and in every token header")
	force := fs.Bool("force", false, "replace keys already in -out")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *out == "" {
		return errOutRequired
	}
	key, err := devtoken.Generate(*kid)
	if err != nil {
		return err
	}
	if err := devtoken.WriteFiles(*out, key, *force); err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "dev keys %s written: %s (private) and %s (public)\n", *kid,
		filepath.Join(*out, devtoken.PrivateFile), filepath.Join(*out, devtoken.PublicFile))
	return nil
}

func tokenCmd(_ context.Context, args []string) error {
	fs := flag.NewFlagSet("token", flag.ContinueOnError)
	keyFile := fs.String("key", "", "private JWK written by dev-keys")
	tenant := fs.String("tenant", "e2e", "tenant claim tid")
	user := fs.String("user", "e2e-user", "user claim sub")
	ttl := fs.Duration("ttl", devTokenTTL, "token lifetime")
	issuer := fs.String("issuer", devIssuer, "issuer claim iss, JWT_ISSUER of websocket and gateway")
	audience := fs.String("audience", devAudience, "audience claim aud, JWT_AUDIENCE of websocket and gateway")
	if err := fs.Parse(args); err != nil {
		return err
	}
	switch {
	case *keyFile == "":
		return errKeyRequired
	case *tenant == "" || *user == "":
		return errCallerEmpty
	case *ttl <= 0:
		return errTTLPositive
	}
	key, err := devtoken.LoadPrivateFile(*keyFile)
	if err != nil {
		return err
	}
	token, err := key.Sign(devtoken.Claims{
		Issuer:   *issuer,
		Audience: *audience,
		Subject:  *user,
		Tenant:   *tenant,
		TTL:      *ttl,
	})
	if err != nil {
		return err
	}
	_, err = fmt.Fprintln(os.Stdout, token)
	return err
}

func devIssuerKeyCmd(_ context.Context, args []string) error {
	fs := flag.NewFlagSet("dev-issuer-key", flag.ContinueOnError)
	alg := fs.String("alg", devtoken.AlgorithmES256, "EdDSA, ES256 or RS256")
	kid := fs.String("kid", "", "key id written in the JWK and in every token header")
	out := fs.String("out", "", "new file for the private JWK (0600); the public JWK goes to stdout")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *out == "" {
		return errOutRequired
	}
	key, err := devtoken.GenerateTenantKey(*alg, *kid)
	if err != nil {
		return err
	}
	private, err := key.PrivateJWK()
	if err != nil {
		return err
	}
	public, err := key.PublicJWK()
	if err != nil {
		return err
	}
	f, err := os.OpenFile(filepath.Clean(*out), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	_, werr := f.Write(private)
	if err := errors.Join(werr, f.Close()); err != nil {
		return err
	}
	_, err = fmt.Fprintln(os.Stdout, string(public))
	return err
}
