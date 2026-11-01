package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"path/filepath"
	"slices"
	"time"

	"connectrpc.com/connect"
)

const (
	itAdminRole      = "it-admin"
	groupFeedLimit   = 15 * time.Second
	adminStatePrefix = "iam16-"
)

var itAdminPermissions = []string{"iam:groups.read", "iam:groups.members.write", "iam:accounts.read"}

type adminFlags struct {
	iam, internal, tenant, dir, binh, ha, secretFile *string
	timeout                                          *time.Duration
}

func parseAdminFlags(args []string) (adminFlags, error) {
	fs := flag.NewFlagSet("e2e iam-admin", flag.ContinueOnError)
	f := adminFlags{
		iam:        fs.String("iam", "http://chatim-iam-1:8080", "iam public url"),
		internal:   fs.String("internal", "http://chatim-iam-1:8081", "iam internal url"),
		tenant:     fs.String("tenant", "", "tenant created for this run"),
		dir:        fs.String("state", "/state", "directory holding the scenario state"),
		binh:       fs.String("binh", "", "account id of binh"),
		ha:         fs.String("ha", "", "account id of ha"),
		secretFile: fs.String("secret-file", "/state/iam16-node.secret", "file holding IAM_NODE_SECRET"),
		timeout:    fs.Duration("timeout", 10*time.Second, "per-call timeout"),
	}
	if err := fs.Parse(args); err != nil {
		return f, err
	}
	if *f.tenant == "" || *f.binh == "" || *f.ha == "" {
		return f, errors.New("-tenant, -binh and -ha are required")
	}
	return f, nil
}

func e2eIAMAdmin(ctx context.Context, args []string) error {
	f, err := parseAdminFlags(args)
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
	lan, err := firstLogin(ctx, c, *f.dir, "lan")
	if err != nil {
		return err
	}
	if err := lan.createRole(ctx, itAdminRole, itAdminPermissions...); err != nil {
		return fmt.Errorf("lan CreateRole %s: %w", itAdminRole, err)
	}
	if err := lan.assignGroupRole(ctx, "it", itAdminRole); err != nil {
		return fmt.Errorf("lan AssignGroupRole(it, %s): %w", itAdminRole, err)
	}
	fmt.Printf("iam: lan (tenant-admin) created %s %v and assigned it to group it\n", itAdminRole, itAdminPermissions)
	binh, err := firstLogin(ctx, c, *f.dir, "binh")
	if err != nil {
		return err
	}
	if err := binhInheritedRole(ctx, f, d, binh); err != nil {
		return err
	}
	if err := binhRefused(ctx, f, binh); err != nil {
		return err
	}
	if err := checkAccessTokenClaims(binh.access); err != nil {
		return err
	}
	fmt.Printf("iam: binh's access token holds only %v, no role or permission\n", accessTokenClaims)
	if err := lan.unassignGroupRole(ctx, "it", itAdminRole); err != nil {
		return fmt.Errorf("lan UnassignGroupRole(it, %s): %w", itAdminRole, err)
	}
	if err := expectCode(binh.setAccountGroups(ctx, *f.ha, "sale"), connect.CodePermissionDenied, "binh SetAccountGroups(ha, [sale]) after the unassign"); err != nil {
		return err
	}
	fmt.Printf("iam: after lan unassigned %s from it, binh's next call got permission_denied\n", itAdminRole)
	return nil
}

func firstLogin(ctx context.Context, c *iamClient, dir, username string) (*adminClient, error) {
	temp, err := readSecret(dir, adminStatePrefix+username+".pw")
	if err != nil {
		return nil, err
	}
	password := "e2e-" + randomHex(12)
	if err := c.changePassword(ctx, username, temp, password); err != nil {
		return nil, fmt.Errorf("%s change password: %w", username, err)
	}
	p, err := newProver()
	if err != nil {
		return nil, err
	}
	t, err := c.login(ctx, p, username, password)
	if err != nil {
		return nil, fmt.Errorf("%s login: %w", username, err)
	}
	return newAdminClient(c, t.Access), nil
}

func binhInheritedRole(ctx context.Context, f adminFlags, d *directoryClient, binh *adminClient) error {
	head, err := d.head(ctx)
	if err != nil {
		return fmt.Errorf("feed head: %w", err)
	}
	if err := binh.setAccountGroups(ctx, *f.ha, "sale"); err != nil {
		return fmt.Errorf("binh SetAccountGroups(ha, [sale]) through %s inherited from it: %w", itAdminRole, err)
	}
	started := time.Now()
	_, err = d.awaitChange(ctx, head, groupFeedLimit, nil, func(ch feedChange) bool {
		return isUser(ch, *f.tenant, *f.ha) && slices.Equal(ch.Groups, []string{"sale"})
	})
	if err != nil {
		return fmt.Errorf("feed after binh set ha's groups: %w", err)
	}
	fmt.Printf("iam: binh (in it-hcm under it) moved ha into sale; the internal feed returned ha with groups [sale] in %v\n", time.Since(started).Round(time.Millisecond))
	return nil
}

func binhRefused(ctx context.Context, f adminFlags, binh *adminClient) error {
	if err := expectCode(binh.setAccountGroups(ctx, *f.binh, "it-hcm", "admins"), connect.CodePermissionDenied, "binh SetAccountGroups(binh, [it-hcm, admins])"); err != nil {
		return err
	}
	if err := expectCode(binh.createRole(ctx, "binh-role", "iam:accounts.read"), connect.CodePermissionDenied, "binh CreateRole"); err != nil {
		return err
	}
	if err := expectCode(binh.setAccountGroups(ctx, *f.ha, "kho-b"), connect.CodeNotFound, "binh SetAccountGroups(ha, [kho-b of another tenant])"); err != nil {
		return err
	}
	fmt.Println("iam: binh joining admins and CreateRole got permission_denied; a group of another tenant got not_found")
	return nil
}
