package main

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"
)

const (
	wakeLimit          = time.Second
	watchPause         = 50 * time.Millisecond
	restartReadyLimit  = 120 * time.Second
	cliDoneWait        = 60 * time.Second
	directoryStateFile = "identity15.json"
	pollOpenFile       = "identity15-poll-open"
	cliTookFile        = "identity15-cli-ms"
	directoryLan       = "lan"
	directoryLanName   = "Lan"
	directoryBinh      = "binh"
	binhNewName        = "Bình Trần"
)

type directoryFlags struct {
	identity, internal, admin, tenant, issuer, audience, key, dir, stage, secretFile *string
	timeout                                                                          *time.Duration
}

type directoryState struct {
	Access string `json:"access"`
}

type directoryRun struct {
	f      directoryFlags
	c      *identityClient
	secret string
}

func parseDirectoryFlags(args []string) (directoryFlags, error) {
	fs := flag.NewFlagSet("e2e identity-directory", flag.ContinueOnError)
	f := directoryFlags{
		identity:   fs.String("identity", "http://chatim-identity-1:8080", "identity public url"),
		internal:   fs.String("internal", "http://chatim-identity-1:8081", "identity internal url"),
		admin:      fs.String("admin", "http://chatim-identity-1:9090", "identity admin url"),
		tenant:     fs.String("tenant", "", "tenant created for this run"),
		issuer:     fs.String("issuer", "", "trusted issuer of the tenant tokens"),
		audience:   fs.String("audience", "chatim", "aud of tenant tokens, equal to IDENTITY_EXCHANGE_AUDIENCE"),
		key:        fs.String("key", "/state/identity15-k1.jwk", "private JWK of the issuer, written by dev-issuer-key"),
		dir:        fs.String("state", "/state", "directory holding the scenario state"),
		stage:      fs.String("stage", "", "seed, profile, disabled or restarted"),
		secretFile: fs.String("secret-file", "/state/identity15-node.secret", "file holding IDENTITY_NODE_SECRET"),
		timeout:    fs.Duration("timeout", 10*time.Second, "per-call timeout"),
	}
	if err := fs.Parse(args); err != nil {
		return f, err
	}
	if *f.tenant == "" || *f.issuer == "" {
		return f, errors.New("-tenant and -issuer are required")
	}
	return f, nil
}

func e2eIdentityDirectory(ctx context.Context, args []string) (err error) {
	f, err := parseDirectoryFlags(args)
	if err != nil {
		return err
	}
	secret, err := readSecret(*f.secretFile)
	if err != nil || secret == "" {
		return fmt.Errorf("read the node secret: %w", err)
	}
	used := &usedSecrets{path: filepath.Join(*f.dir, "identity15-tokens")}
	defer func() { err = errors.Join(err, used.save()) }()
	r := directoryRun{f: f, c: newIdentityClient(*f.identity, *f.issuer, *f.audience, *f.tenant, *f.timeout, used), secret: secret}
	switch *f.stage {
	case "seed":
		return r.seed(ctx)
	case "profile":
		return r.profile(ctx)
	case "disabled":
		return r.disabled(ctx)
	case "restarted":
		return r.restarted(ctx)
	default:
		return errors.New("-stage must be seed, profile, disabled or restarted")
	}
}

func (r directoryRun) exchange(ctx context.Context, user, name string) (string, error) {
	key, err := loadTenantKey(*r.f.key)
	if err != nil {
		return "", err
	}
	return r.c.awaitExchange(ctx, key, r.c.claims(user, name), trustLimit, "exchange for "+user)
}

func (r directoryRun) seed(ctx context.Context) error {
	access, err := r.exchange(ctx, directoryLan, directoryLanName)
	if err != nil {
		return err
	}
	if _, err := r.exchange(ctx, directoryBinh, ""); err != nil {
		return err
	}
	fmt.Println("identity: exchanges for lan (name Lan) and binh (no name) created both users")
	return writeJSON(filepath.Join(*r.f.dir, directoryStateFile), directoryState{Access: access})
}

func (r directoryRun) profile(ctx context.Context) error {
	var st directoryState
	if err := readJSON(filepath.Join(*r.f.dir, directoryStateFile), &st); err != nil {
		return err
	}
	if err := r.checkFirstUsers(ctx, st.Access); err != nil {
		return err
	}
	opened, arrived, err := r.watchNewName(ctx, st.Access, filepath.Join(*r.f.dir, pollOpenFile))
	if err != nil {
		return err
	}
	took, err := awaitCLITook(ctx, *r.f.dir)
	if err != nil {
		return err
	}
	lag := arrived.Sub(opened) - took
	if lag >= wakeLimit {
		return fmt.Errorf("GetUsers showed the new name %v after user-set finished, want under %v", lag.Round(time.Millisecond), wakeLimit)
	}
	fmt.Printf("identity: GetUsers polled every %v showed the new name at most %v after user-set finished (watch open %v, the command took %v)\n",
		watchPause, max(lag, 0).Round(time.Millisecond), arrived.Sub(opened).Round(time.Millisecond), took.Round(time.Millisecond))
	return nil
}

func (r directoryRun) watchNewName(ctx context.Context, access, openPath string) (time.Time, time.Time, error) {
	var opened time.Time
	for {
		users, _, err := r.c.getUsers(ctx, access, []string{directoryBinh})
		if err != nil {
			return opened, time.Time{}, fmt.Errorf("GetUsers while waiting for the new name: %w", err)
		}
		now := time.Now()
		if slices.ContainsFunc(users, func(u directoryUser) bool { return u.ID == directoryBinh && u.DisplayName == binhNewName }) {
			if opened.IsZero() {
				return opened, now, errors.New("GetUsers showed the new name before user-set ran")
			}
			return opened, now, nil
		}
		if opened.IsZero() {
			opened = now
			if err := os.WriteFile(openPath, nil, 0o600); err != nil {
				return opened, time.Time{}, err
			}
		}
		if now.Sub(opened) > cliDoneWait {
			return opened, time.Time{}, fmt.Errorf("GetUsers(%s) = %+v, the new name never came within %v", directoryBinh, users, cliDoneWait)
		}
		select {
		case <-ctx.Done():
			return opened, time.Time{}, ctx.Err()
		case <-time.After(watchPause):
		}
	}
}

func (r directoryRun) checkFirstUsers(ctx context.Context, access string) error {
	ids := []string{directoryLan, directoryBinh, "e2e-unknown-" + randomHex(4)}
	started := time.Now()
	for {
		users, raw, err := r.c.getUsers(ctx, access, ids)
		if err != nil {
			return err
		}
		lower := bytes.ToLower(raw)
		if bytes.Contains(lower, []byte("email")) || bytes.Contains(lower, []byte("username")) {
			return fmt.Errorf("GetUsers leaked email or username: %s", raw)
		}
		got := usersByID(users)
		if len(users) == 2 && got[directoryLan].DisplayName == directoryLanName && got[directoryBinh].DisplayName == directoryBinh {
			fmt.Println("identity: GetUsers of lan, binh and an unknown id returned the two known users, with no email or username")
			return nil
		}
		if time.Since(started) > directoryLimit {
			return fmt.Errorf("GetUsers(lan, binh, unknown) = %+v, want Lan and binh only", users)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(50 * time.Millisecond):
		}
	}
}

func (r directoryRun) disabled(ctx context.Context) error {
	var st directoryState
	if err := readJSON(filepath.Join(*r.f.dir, directoryStateFile), &st); err != nil {
		return err
	}
	took, err := r.c.awaitUser(ctx, st.Access, directoryBinh, directoryLimit, func(u directoryUser) bool { return u.State == "USER_STATE_DISABLED" })
	if err != nil {
		return fmt.Errorf("GetUsers after user-disable: %w", err)
	}
	if err := expectInternalStatus(ctx, r.c.http, *r.f.internal, revocationsPath, r.secret, http.StatusOK); err != nil {
		return err
	}
	if err := expectInternalStatus(ctx, r.c.http, *r.f.internal, removedFeedPath, r.secret, http.StatusNotFound); err != nil {
		return err
	}
	wrong := "wrong-" + randomHex(16)
	r.c.used.remember(wrong)
	if err := expectInternalStatus(ctx, r.c.http, *r.f.internal, revocationsPath, wrong, http.StatusUnauthorized); err != nil {
		return err
	}
	fmt.Printf("identity: user-disable reached GetUsers as USER_STATE_DISABLED within %v; with the node secret %s answered 200 and the removed feed %s 404; a wrong node secret got 401\n",
		took.Round(time.Millisecond), revocationsPath, removedFeedPath)
	return nil
}

func (r directoryRun) restarted(ctx context.Context) error {
	waited, err := awaitReady(ctx, r.c.http, *r.f.admin, restartReadyLimit)
	if err != nil {
		return err
	}
	access, err := r.exchange(ctx, directoryLan, directoryLanName)
	if err != nil {
		return err
	}
	users, _, err := r.c.getUsers(ctx, access, []string{directoryLan, directoryBinh})
	if err != nil {
		return fmt.Errorf("GetUsers after the restart: %w", err)
	}
	got := usersByID(users)
	lan, binh := got[directoryLan], got[directoryBinh]
	if len(users) != 2 || lan.DisplayName != directoryLanName || lan.State != "USER_STATE_ACTIVE" || binh.DisplayName != binhNewName || binh.State != "USER_STATE_DISABLED" {
		return fmt.Errorf("GetUsers after the restart = %+v, want Lan active and %s disabled", users, binhNewName)
	}
	fmt.Printf("identity: after a restart /readyz turned true in %v, a fresh exchange for lan worked and GetUsers returned both users as before\n", waited.Round(100*time.Millisecond))
	return nil
}

func usersByID(users []directoryUser) map[string]directoryUser {
	got := make(map[string]directoryUser, len(users))
	for _, u := range users {
		got[u.ID] = u
	}
	return got
}

func awaitCLITook(ctx context.Context, dir string) (time.Duration, error) {
	deadline := time.Now().Add(cliDoneWait)
	for {
		raw, err := os.ReadFile(filepath.Join(dir, cliTookFile))
		if err == nil {
			ms, perr := strconv.ParseFloat(strings.TrimSpace(string(raw)), 64)
			if perr != nil {
				return 0, fmt.Errorf("read %s: %w", cliTookFile, perr)
			}
			return time.Duration(ms * float64(time.Millisecond)), nil
		}
		if time.Now().After(deadline) {
			return 0, fmt.Errorf("the script never wrote %s", cliTookFile)
		}
		select {
		case <-ctx.Done():
			return 0, ctx.Err()
		case <-time.After(20 * time.Millisecond):
		}
	}
}
