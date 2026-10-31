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
	"strconv"
	"strings"
	"time"
)

const (
	feedWaitSeconds    = 10
	wakeLimit          = time.Second
	refreshLimit       = 2 * time.Second
	disabledFeedLimit  = 15 * time.Second
	restartReadyLimit  = 120 * time.Second
	cliDoneWait        = 60 * time.Second
	directoryStateFile = "iam15.json"
	pollOpenFile       = "iam15-poll-open"
	cliTookFile        = "iam15-cli-ms"
	binhNewName        = "Bình Trần"
)

type directoryFlags struct {
	iam, internal, admin, tenant, dir, stage, lan, binh, secretFile *string
	timeout                                                         *time.Duration
}

type directoryState struct {
	Access string `json:"access"`
	Next   string `json:"next"`
}

func parseDirectoryFlags(args []string) (directoryFlags, error) {
	fs := flag.NewFlagSet("e2e iam-directory", flag.ContinueOnError)
	f := directoryFlags{
		iam:        fs.String("iam", "http://chatim-iam-1:8080", "iam public url"),
		internal:   fs.String("internal", "http://chatim-iam-1:8081", "iam internal url"),
		admin:      fs.String("admin", "http://chatim-iam-1:9090", "iam admin url"),
		tenant:     fs.String("tenant", "", "tenant created for this run"),
		dir:        fs.String("state", "/state", "directory holding the scenario state"),
		stage:      fs.String("stage", "", "profile, disabled or restarted"),
		lan:        fs.String("lan", "", "account id of lan"),
		binh:       fs.String("binh", "", "account id of binh"),
		secretFile: fs.String("secret-file", "/state/iam15-node.secret", "file holding IAM_NODE_SECRET"),
		timeout:    fs.Duration("timeout", 10*time.Second, "per-call timeout"),
	}
	if err := fs.Parse(args); err != nil {
		return f, err
	}
	if *f.tenant == "" || *f.lan == "" || *f.binh == "" {
		return f, errors.New("-tenant, -lan and -binh are required")
	}
	return f, nil
}

func e2eIAMDirectory(ctx context.Context, args []string) error {
	f, err := parseDirectoryFlags(args)
	if err != nil {
		return err
	}
	secret, err := readSecret(filepath.Dir(*f.secretFile), filepath.Base(*f.secretFile))
	if err != nil || secret == "" {
		return fmt.Errorf("read the node secret: %w", err)
	}
	c := newIAMClient(*f.iam, *f.tenant, *f.timeout)
	c.remember(secret)
	defer saveUsed(*f.dir, c)
	d := newDirectoryClient(c, *f.internal, secret)
	switch *f.stage {
	case "profile":
		return directoryProfile(ctx, f, d)
	case "disabled":
		return directoryDisabled(ctx, f, d)
	case "restarted":
		return directoryRestarted(ctx, f, d)
	default:
		return errors.New("-stage must be profile, disabled or restarted")
	}
}

func directoryProfile(ctx context.Context, f directoryFlags, d *directoryClient) error {
	temp, err := readSecret(*f.dir, "iam15-lan.pw")
	if err != nil {
		return err
	}
	password := "e2e-" + randomHex(12)
	if err := d.iam.changePassword(ctx, "lan", temp, password); err != nil {
		return fmt.Errorf("change password: %w", err)
	}
	p, err := newProver()
	if err != nil {
		return err
	}
	lan, err := d.iam.login(ctx, p, "lan", password)
	if err != nil {
		return fmt.Errorf("login: %w", err)
	}
	if err := checkFirstUsers(ctx, f, d, lan.Access); err != nil {
		return err
	}
	head, err := d.head(ctx)
	if err != nil {
		return fmt.Errorf("feed head: %w", err)
	}
	openPath := filepath.Join(*f.dir, pollOpenFile)
	var opened, arrived time.Time
	signal := func() {
		opened = time.Now()
		_ = os.WriteFile(openPath, nil, 0o600)
	}
	next, err := d.awaitChange(ctx, head, cliDoneWait, signal, func(ch feedChange) bool {
		return isUser(ch, *f.tenant, *f.binh) && ch.DisplayName == binhNewName
	})
	if err != nil {
		return fmt.Errorf("long poll for the new name: %w", err)
	}
	arrived = time.Now()
	took, err := awaitCLITook(ctx, *f.dir)
	if err != nil {
		return err
	}
	wake := arrived.Sub(opened) - took
	if wake >= wakeLimit {
		return fmt.Errorf("the long poll returned %v after profile-set finished, want under %v", wake.Round(time.Millisecond), wakeLimit)
	}
	_, refreshed, err := d.awaitUser(ctx, lan.Access, *f.binh, refreshLimit, func(u directoryUser) bool { return u.DisplayName == binhNewName })
	if err != nil {
		return fmt.Errorf("GetUsers after profile-set: %w", err)
	}
	fmt.Printf("iam: the long poll returned the new name at most %v after profile-set finished (poll open %v, the command took %v); GetUsers showed it %v after the feed\n",
		max(wake, 0).Round(time.Millisecond), arrived.Sub(opened).Round(time.Millisecond), took.Round(time.Millisecond), refreshed.Round(time.Millisecond))
	return writeJSON(filepath.Join(*f.dir, directoryStateFile), directoryState{Access: lan.Access, Next: next})
}

func checkFirstUsers(ctx context.Context, f directoryFlags, d *directoryClient, access string) error {
	users, raw, err := d.getUsers(ctx, access, []string{*f.lan, *f.binh, "e2e-unknown-" + randomHex(4)})
	if err != nil {
		return err
	}
	lower := bytes.ToLower(raw)
	if bytes.Contains(lower, []byte("email")) || bytes.Contains(lower, []byte("username")) {
		return fmt.Errorf("GetUsers leaked email or username: %s", raw)
	}
	got := map[string]directoryUser{}
	for _, u := range users {
		got[u.ID] = u
	}
	if len(users) != 2 || got[*f.lan].DisplayName != "Lan" || got[*f.binh].DisplayName != "binh" {
		return fmt.Errorf("GetUsers(lan, binh, unknown) = %+v, want Lan and binh only", users)
	}
	fmt.Println("iam: GetUsers of lan, binh and an unknown id returned the two known users, with no email or username")
	return nil
}

func directoryDisabled(ctx context.Context, f directoryFlags, d *directoryClient) error {
	var st directoryState
	if err := readJSON(filepath.Join(*f.dir, directoryStateFile), &st); err != nil {
		return err
	}
	d.iam.remember(st.Access)
	started := time.Now()
	next, err := d.awaitChange(ctx, st.Next, disabledFeedLimit, nil, func(ch feedChange) bool {
		return isUser(ch, *f.tenant, *f.binh) && ch.State == "disabled"
	})
	if err != nil {
		return fmt.Errorf("feed after account-disable: %w", err)
	}
	fed := time.Since(started)
	if _, _, err := d.awaitUser(ctx, st.Access, *f.binh, refreshLimit, func(u directoryUser) bool { return u.State == "USER_STATE_DISABLED" }); err != nil {
		return fmt.Errorf("GetUsers after account-disable: %w", err)
	}
	wrong := "wrong-" + randomHex(16)
	if _, status, err := d.changes(ctx, wrong, "", 0, nil); err != nil || status != http.StatusUnauthorized {
		return fmt.Errorf("feed with a wrong secret = %d, %w; want 401", status, err)
	}
	fmt.Printf("iam: account-disable reached the feed as state disabled in %v and GetUsers as USER_STATE_DISABLED; a wrong node secret got 401\n", fed.Round(time.Millisecond))
	return writeJSON(filepath.Join(*f.dir, directoryStateFile), directoryState{Access: st.Access, Next: next})
}

func directoryRestarted(ctx context.Context, f directoryFlags, d *directoryClient) error {
	var st directoryState
	if err := readJSON(filepath.Join(*f.dir, directoryStateFile), &st); err != nil {
		return err
	}
	d.iam.remember(st.Access)
	waited, err := d.readyz(ctx, *f.admin, restartReadyLimit)
	if err != nil {
		return err
	}
	users, _, err := d.getUsers(ctx, st.Access, []string{*f.lan, *f.binh})
	if err != nil {
		return fmt.Errorf("GetUsers after the restart: %w", err)
	}
	got := map[string]directoryUser{}
	for _, u := range users {
		got[u.ID] = u
	}
	lan, binh := got[*f.lan], got[*f.binh]
	if len(users) != 2 || lan.DisplayName != "Lan" || lan.State != "USER_STATE_ACTIVE" || binh.DisplayName != binhNewName || binh.State != "USER_STATE_DISABLED" {
		return fmt.Errorf("GetUsers after the restart = %+v, want Lan active and %s disabled", users, binhNewName)
	}
	fmt.Printf("iam: after a restart /readyz turned true in %v and GetUsers returned both users as before\n", waited.Round(100*time.Millisecond))
	return nil
}

func isUser(ch feedChange, tenant, id string) bool {
	return ch.Kind == "user" && ch.TenantID == tenant && ch.ID == id
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
