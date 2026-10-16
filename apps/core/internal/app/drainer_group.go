package app

import (
	"context"
	"errors"
	"sync"
)

type drainerGroup []drainer

func (g drainerGroup) Run(ctx context.Context) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	var (
		wg    sync.WaitGroup
		once  sync.Once
		first error
	)
	for _, d := range g {
		wg.Go(func() {
			if err := d.Run(ctx); err != nil {
				once.Do(func() {
					first = err
					cancel()
				})
			}
		})
	}
	wg.Wait()
	return first
}

func (g drainerGroup) Close(ctx context.Context) error {
	errs := make([]error, len(g))
	var wg sync.WaitGroup
	for i, d := range g {
		wg.Go(func() { errs[i] = d.Close(ctx) })
	}
	wg.Wait()
	return errors.Join(errs...)
}
