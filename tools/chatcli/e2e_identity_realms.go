package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	otherIssuerKey = "identity-other-k1.jwk"
	realmIssuerKey = "identity-realm-k1.jwk"
)

func sameUser(got chatToken, want string, err error, what string) error {
	if err != nil {
		return err
	}
	if got.User != want {
		return fmt.Errorf("%s: chat id %s, want %s as before", what, got.User, want)
	}
	return nil
}

func (r exchangeRun) saveUser(user string) error {
	return os.WriteFile(filepath.Join(*r.f.dir, exchangeUserFile), []byte(user+"\n"), 0o600)
}

func (r exchangeRun) savedUser() (string, error) {
	raw, err := os.ReadFile(filepath.Join(*r.f.dir, exchangeUserFile))
	if err != nil {
		return "", fmt.Errorf("read the chat id saved by -stage ok: %w", err)
	}
	user := strings.TrimSpace(string(raw))
	if !chatUserID.MatchString(user) {
		return "", fmt.Errorf("%s holds %q, not a chat id", exchangeUserFile, user)
	}
	return user, nil
}

func (r exchangeRun) exchangeAs(ctx context.Context, issuer, keyFile, what string) (chatToken, error) {
	key, err := loadTenantKey(filepath.Join(*r.f.dir, keyFile))
	if err != nil {
		return chatToken{}, err
	}
	claims := r.c.claims(exchangeUser, exchangeNewName)
	claims.Issuer = issuer
	return r.c.awaitExchange(ctx, key, claims, trustLimit, what)
}

func (r exchangeRun) realms(ctx context.Context) error {
	if *r.f.otherIssuer == "" || *r.f.realmIssuer == "" {
		return errors.New("-stage realms needs -other-issuer and -realm-issuer")
	}
	user, err := r.savedUser()
	if err != nil {
		return err
	}
	created, err := metricValue(ctx, *r.f.admin+"/metrics", linkCreated)
	if err != nil {
		return err
	}
	other, err := r.exchangeAs(ctx, *r.f.otherIssuer, otherIssuerKey, "exchange through the issuer with its own realm")
	if err != nil {
		return err
	}
	if other.User == user {
		return fmt.Errorf("issuer %s with its own realm mapped sub %s to %s, the user of %s", *r.f.otherIssuer, exchangeUser, user, *r.f.issuer)
	}
	after, err := metricValue(ctx, *r.f.admin+"/metrics", linkCreated)
	if err != nil {
		return err
	}
	if after <= created {
		return fmt.Errorf("%s stayed at %v after the first exchange through %s", linkCreated, after, *r.f.otherIssuer)
	}
	shared, err := r.exchangeAs(ctx, *r.f.realmIssuer, realmIssuerKey, "exchange through the issuer sharing the realm")
	if err := sameUser(shared, user, err, "exchange through "+*r.f.realmIssuer); err != nil {
		return err
	}
	fmt.Printf("identity: sub %s through %s (own realm) became the new user %s; through %s (realm %s) the same user %s\n",
		exchangeUser, *r.f.otherIssuer, other.User, *r.f.realmIssuer, *r.f.issuer, user)
	return nil
}

func (r exchangeRun) userEnabled(ctx context.Context) error {
	user, err := r.savedUser()
	if err != nil {
		return err
	}
	started := time.Now()
	got, err := r.c.awaitExchange(ctx, r.k1, r.c.claims(exchangeUser, exchangeNewName), directoryLimit, "exchange after user-enable")
	if err := sameUser(got, user, err, "exchange after user-enable"); err != nil {
		return err
	}
	fmt.Printf("identity: user-enable let the next exchange through within %v, for the same chat id\n", time.Since(started).Round(time.Millisecond))
	return nil
}
