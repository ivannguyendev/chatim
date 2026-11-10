package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/ivannguyendev/chatim/tools/internal/devtoken"
)

const (
	exchangeUser     = "ha"
	exchangeName     = "Hà"
	exchangeNewName  = "Hà Nguyễn"
	exchangeUserFile = "identity13-user"
	jitSeries        = "chatim_identity_jit_writes_total"
	linkCreated      = `chatim_identity_user_links_total{result="created"}`
	exchangeDenied   = `chatim_identity_exchanges_total{result="disabled"}`
)

type exchangeFlags struct {
	identity, admin, ws, gateway, tenant, issuer, otherIssuer, realmIssuer, audience, dir, stage *string
	timeout                                                                                      *time.Duration
}

type exchangeRun struct {
	f      exchangeFlags
	c      *identityClient
	k1, k2 devtoken.TenantKey
}

func parseExchangeFlags(args []string) (exchangeFlags, error) {
	fs := flag.NewFlagSet("e2e identity-exchange", flag.ContinueOnError)
	f := exchangeFlags{
		identity:    fs.String("identity", "http://chatim-identity-1:8080", "identity public url"),
		admin:       fs.String("admin", "http://chatim-identity-1:9090", "identity admin url"),
		ws:          fs.String("url", "ws://chatim-websocket-1:8080/ws", "websocket url"),
		gateway:     fs.String("gateway", defaultGatewayURL, "gateway url"),
		tenant:      fs.String("tenant", "", "tenant created for this run"),
		issuer:      fs.String("issuer", "", "trusted issuer of the tenant tokens"),
		otherIssuer: fs.String("other-issuer", "", "second issuer of the tenant with its own realm, key identity-other-k1.jwk"),
		realmIssuer: fs.String("realm-issuer", "", "third issuer of the tenant in the realm of -issuer, key identity-realm-k1.jwk"),
		audience:    fs.String("audience", "chatim", "aud of tenant tokens, equal to IDENTITY_EXCHANGE_AUDIENCE"),
		dir:         fs.String("state", "/state", "directory holding the keys k1.jwk and k2.jwk, the token list and the chat user id"),
		stage:       fs.String("stage", "", "ok, realms, disabled, enabled, key-removed or tenant-disabled"),
		timeout:     fs.Duration("timeout", 10*time.Second, "per-call timeout"),
	}
	if err := fs.Parse(args); err != nil {
		return f, err
	}
	if *f.tenant == "" || *f.issuer == "" {
		return f, errors.New("-tenant and -issuer are required")
	}
	return f, nil
}

func loadTenantKey(path string) (devtoken.TenantKey, error) {
	raw, err := os.ReadFile(filepath.Clean(path))
	if err != nil {
		return devtoken.TenantKey{}, err
	}
	return devtoken.ParseTenantKey(raw)
}

func e2eIdentityExchange(ctx context.Context, args []string) (err error) {
	f, err := parseExchangeFlags(args)
	if err != nil {
		return err
	}
	used := &usedSecrets{path: filepath.Join(*f.dir, "identity-tokens")}
	defer func() { err = errors.Join(err, used.save()) }()
	r := exchangeRun{f: f, c: newIdentityClient(*f.identity, *f.issuer, *f.audience, *f.tenant, *f.timeout, used)}
	if r.k1, err = loadTenantKey(filepath.Join(*f.dir, "identity-k1.jwk")); err != nil {
		return err
	}
	if r.k2, err = loadTenantKey(filepath.Join(*f.dir, "identity-k2.jwk")); err != nil {
		return err
	}
	switch *f.stage {
	case "ok":
		if err := r.exchangeOK(ctx); err != nil {
			return err
		}
		return r.forged(ctx)
	case "realms":
		return r.realms(ctx)
	case "disabled":
		return r.userDisabled(ctx)
	case "enabled":
		return r.userEnabled(ctx)
	case "key-removed":
		return r.keyRemoved(ctx)
	case "tenant-disabled":
		return r.tenantDisabled(ctx)
	default:
		return errors.New("-stage must be ok, realms, disabled, enabled, key-removed or tenant-disabled")
	}
}

func (r exchangeRun) exchangeOK(ctx context.Context) error {
	created, err := metricValue(ctx, *r.f.admin+"/metrics", linkCreated)
	if err != nil {
		return err
	}
	first, err := r.c.awaitExchange(ctx, r.k1, r.c.claims(exchangeUser, exchangeName), trustLimit, "exchange a k1 ES256 token once the new issuer is loaded")
	if err != nil {
		return err
	}
	fmt.Printf("identity: a tenant token signed by k1 (ES256) for sub %s became a chat token with only aud, exp, iat, iss, sid, sub and tid, a 300s lifetime, the tenant of the issuer and the identity-issued sub %s\n", exchangeUser, first.User)
	if err := websocketReady(ctx, *r.f.ws, first.Access, *r.f.timeout); err != nil {
		return err
	}
	status, err := gatewayStatus(ctx, *r.f.gateway, first.Access, *r.f.timeout)
	if err != nil || status != http.StatusOK {
		return fmt.Errorf("gateway with the chat token = %d, %w; want 200", status, err)
	}
	fmt.Println("identity: websocket-1 answered ready and the gateway 200 to the exchanged token")
	took, err := r.c.awaitUser(ctx, first.Access, first.User, directoryLimit, func(u directoryUser) bool { return u.DisplayName == exchangeName })
	if err != nil {
		return fmt.Errorf("GetUsers after the first exchange: %w", err)
	}
	after, err := metricValue(ctx, *r.f.admin+"/metrics", linkCreated)
	if err != nil {
		return err
	}
	if after <= created {
		return fmt.Errorf("%s stayed at %v after the first exchange", linkCreated, after)
	}
	fmt.Printf("identity: the first exchange linked %s to a new user named %q, seen by GetUsers of %s in %v\n", exchangeUser, exchangeName, first.User, took.Round(time.Millisecond))
	if err := r.sameNameWritesNothing(ctx, first.User); err != nil {
		return err
	}
	renamed, err := r.c.mustExchange(ctx, r.k1, r.c.claims(exchangeUser, exchangeNewName), "exchange with a new name")
	if err := sameUser(renamed, first.User, err, "exchange with a new name"); err != nil {
		return err
	}
	if took, err = r.c.awaitUser(ctx, first.Access, first.User, directoryLimit, func(u directoryUser) bool { return u.DisplayName == exchangeNewName }); err != nil {
		return fmt.Errorf("GetUsers after the new name: %w", err)
	}
	fmt.Printf("identity: an exchange carrying %q renamed the user, seen by GetUsers in %v\n", exchangeNewName, took.Round(time.Millisecond))
	byK2, err := r.c.awaitExchange(ctx, r.k2, r.c.claims(exchangeUser, exchangeNewName), trustLimit, "exchange a k2 RS256 token")
	if err := sameUser(byK2, first.User, err, "exchange a k2 RS256 token"); err != nil {
		return err
	}
	fmt.Println("identity: a tenant token signed by k2 (RS256) was exchanged too, for the same chat id")
	return r.saveUser(first.User)
}

func (r exchangeRun) sameNameWritesNothing(ctx context.Context, user string) error {
	before, err := metricSum(ctx, *r.f.admin+"/metrics", jitSeries)
	if err != nil {
		return err
	}
	again, err := r.c.mustExchange(ctx, r.k1, r.c.claims(exchangeUser, exchangeName), "second exchange with the same name")
	if err := sameUser(again, user, err, "second exchange with the same name"); err != nil {
		return err
	}
	after, err := metricSum(ctx, *r.f.admin+"/metrics", jitSeries)
	if err != nil {
		return err
	}
	if after != before {
		return fmt.Errorf("%s went from %v to %v on an exchange with an unchanged name", jitSeries, before, after)
	}
	fmt.Println("identity: a second exchange with the same name gave the same chat id and wrote nothing (jit_writes_total unchanged)")
	return nil
}

func (r exchangeRun) userDisabled(ctx context.Context) error {
	before, err := metricValue(ctx, *r.f.admin+"/metrics", exchangeDenied)
	if err != nil {
		return err
	}
	took, err := r.c.awaitRefused(ctx, r.k1, r.c.claims(exchangeUser, exchangeNewName), directoryLimit, pollPause)
	if err != nil {
		return fmt.Errorf("exchange after user-disable: %w", err)
	}
	after, err := metricValue(ctx, *r.f.admin+"/metrics", exchangeDenied)
	if err != nil {
		return err
	}
	if after <= before {
		return fmt.Errorf("%s stayed at %v after user-disable", exchangeDenied, after)
	}
	fmt.Printf("identity: user-disable refused the next exchange within %v, counted as result disabled\n", took.Round(time.Millisecond))
	return nil
}

func (r exchangeRun) keyRemoved(ctx context.Context) error {
	took, err := r.c.awaitRefused(ctx, r.k1, r.c.claims(exchangeUser, exchangeNewName), trustLimit, time.Second)
	if err != nil {
		return fmt.Errorf("k1 after issuer-key-remove: %w", err)
	}
	user, err := r.savedUser()
	if err != nil {
		return err
	}
	byK2, err := r.c.mustExchange(ctx, r.k2, r.c.claims(exchangeUser, exchangeNewName), "k2 after k1 was removed")
	if err := sameUser(byK2, user, err, "k2 after k1 was removed"); err != nil {
		return err
	}
	fmt.Printf("identity: issuer-key-remove k1 refused k1 within %v while k2 kept working\n", took.Round(time.Second))
	return nil
}

func (r exchangeRun) tenantDisabled(ctx context.Context) error {
	took, err := r.c.awaitRefused(ctx, r.k2, r.c.claims(exchangeUser, exchangeNewName), trustLimit, time.Second)
	if err != nil {
		return fmt.Errorf("k2 after tenant-disable: %w", err)
	}
	fmt.Printf("identity: tenant-disable refused k2 within %v\n", took.Round(time.Millisecond))
	return nil
}
