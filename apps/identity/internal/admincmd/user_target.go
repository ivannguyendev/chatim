package admincmd

import (
	"context"
	"fmt"

	"github.com/ivannguyendev/chatim/apps/identity/internal/trust"
)

var errUserPair = fmt.Errorf("%w: give exactly one of -tenant with -user, or -issuer with -sub", errUsage)

type userTarget struct {
	f      flags
	tenant *string
	user   *string
	issuer *string
	sub    *string
}

func userFlags(name string) (flags, userTarget) {
	f := newFlags(name)
	return f, userTarget{
		f:      f,
		tenant: f.set.String("tenant", "", "tenant id"),
		user:   f.set.String("user", "", "user id"),
		issuer: f.set.String("issuer", "", "iss of the tenant tokens"),
		sub:    f.set.String("sub", "", "sub of the tenant tokens"),
	}
}

func (u userTarget) check() error {
	given := u.f.given()
	byID := given["tenant"] || given["user"]
	bySubject := given["issuer"] || given["sub"]
	switch {
	case byID == bySubject:
		return errUserPair
	case byID && (*u.tenant == "" || *u.user == ""):
		return errUserPair
	case bySubject:
		return checkSubjectPair(*u.issuer, *u.sub)
	default:
		return nil
	}
}

func (u userTarget) resolve(ctx context.Context, st Store) (string, string, error) {
	if *u.issuer == "" {
		return *u.tenant, *u.user, nil
	}
	return st.ResolveUser(ctx, *u.issuer, *u.sub)
}

func checkSubjectPair(issuer, sub string) error {
	if err := checkTrustName("issuer", issuer); err != nil {
		return err
	}
	if !trust.ValidSubject(sub) {
		return fmt.Errorf("%w: -sub must be 1 to %d printable ASCII bytes without spaces", errUsage, trustNameMaxBytes)
	}
	return nil
}

func userFind(ctx context.Context, env Env, st Store, args []string) error {
	f := newFlags("user-find")
	issuer := f.set.String("issuer", "", "iss of the tenant tokens")
	sub := f.set.String("sub", "", "sub of the tenant tokens")
	if err := f.parse(args, "issuer", "sub"); err != nil {
		return err
	}
	if err := checkSubjectPair(*issuer, *sub); err != nil {
		return err
	}
	tenant, user, err := st.ResolveUser(ctx, *issuer, *sub)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(env.Out, "%s %s\n", tenant, user)
	return err
}
