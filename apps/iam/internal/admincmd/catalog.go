package admincmd

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"
	"unicode"

	"github.com/ivannguyendev/chatim/apps/iam/internal/authz"
	"github.com/ivannguyendev/chatim/apps/iam/internal/store"
	"github.com/ivannguyendev/chatim/pkg/apperr"
)

func clientPermissionsSet(ctx context.Context, env Env, st Store, args []string) error {
	f := newFlags("client-permissions-set")
	client := f.set.String("client", "", "client id")
	file := f.set.String("file", "", "file with one permission and its description per line")
	if err := f.parse(args, "client", "file"); err != nil {
		return err
	}
	data, err := os.ReadFile(*file)
	if err != nil {
		return err
	}
	entries, err := parseCatalog(string(data))
	if err != nil {
		return err
	}
	at := audit(env, "", "client_permissions_set", "", "")
	at.Detail = map[string]string{"client": *client, "permissions": strconv.Itoa(len(entries))}
	if err := st.SetClientPermissions(ctx, *client, entries, at); err != nil {
		return err
	}
	_, err = fmt.Fprintf(env.Out, "client %s: %d permissions\n", *client, len(entries))
	return err
}

func parseCatalog(text string) ([]store.ClientPermission, error) {
	entries := []store.ClientPermission{}
	for i, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		cut := strings.IndexFunc(line, unicode.IsSpace)
		if cut < 0 {
			return nil, fmt.Errorf("%w: line %d needs a permission and a description", apperr.ErrInvalidArgument, i+1)
		}
		p, err := authz.Parse(line[:cut])
		if err != nil {
			return nil, fmt.Errorf("line %d: %w", i+1, err)
		}
		entries = append(entries, store.ClientPermission{Permission: string(p), Description: strings.TrimSpace(line[cut:])})
	}
	return entries, nil
}
