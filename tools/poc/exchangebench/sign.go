package main

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/ivannguyendev/chatim/tools/internal/devtoken"
)

const probeUser = "probe"

type signer struct {
	key devtoken.TenantKey
	cfg config
	ids []string
}

func userSet(c config) ([]string, error) {
	if c.usersFile == "" {
		ids := make([]string, c.users)
		for i := range ids {
			ids[i] = c.userPrefix + strconv.Itoa(i)
		}
		return ids, nil
	}
	data, err := os.ReadFile(filepath.Clean(c.usersFile))
	if err != nil {
		return nil, fmt.Errorf("read -users-file: %w", err)
	}
	ids := strings.Fields(string(data))
	if len(ids) == 0 {
		return nil, errors.New("-users-file holds no user id")
	}
	return ids, nil
}

func loadKey(path, alg string) (devtoken.TenantKey, error) {
	data, err := os.ReadFile(filepath.Clean(path))
	if err != nil {
		return devtoken.TenantKey{}, fmt.Errorf("read -key: %w", err)
	}
	key, err := devtoken.ParseTenantKey(data)
	if err != nil {
		return devtoken.TenantKey{}, fmt.Errorf("parse -key: %w", err)
	}
	if key.Algorithm != alg {
		return devtoken.TenantKey{}, fmt.Errorf("-key holds a %s key, -alg says %s", key.Algorithm, alg)
	}
	return key, nil
}

func (s signer) sign(user string, issued time.Time) (string, error) {
	return s.key.Sign(devtoken.TenantClaims{
		Issuer: s.cfg.issuer, Audience: s.cfg.audience, Tenant: s.cfg.tenant,
		Subject: user, Name: "Bench " + user, IssuedAt: issued, TTL: s.cfg.ttl,
	})
}

func (s signer) presign(ctx context.Context, total int) ([]string, error) {
	order := rand.Perm(len(s.ids))
	tokens := make([]string, total)
	issued := time.Now()
	jobs := make(chan int)
	errs := make(chan error, s.cfg.signers)
	var wg sync.WaitGroup
	for range s.cfg.signers {
		wg.Go(func() {
			for i := range jobs {
				token, err := s.sign(s.ids[order[i%len(order)]], issued)
				if err != nil {
					errs <- err
					return
				}
				tokens[i] = token
			}
		})
	}
	var err error
feed:
	for i := range total {
		select {
		case jobs <- i:
		case err = <-errs:
			break feed
		case <-ctx.Done():
			err = ctx.Err()
			break feed
		}
	}
	close(jobs)
	wg.Wait()
	if err != nil {
		return nil, fmt.Errorf("sign tenant tokens: %w", err)
	}
	select {
	case err = <-errs:
		return nil, fmt.Errorf("sign tenant tokens: %w", err)
	default:
	}
	return tokens, nil
}
