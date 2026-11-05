package app

import "context"

type liveWatcher interface {
	Start(ctx context.Context) error
	Stop()
}

type liveWatchers []liveWatcher

func (ws liveWatchers) Start(ctx context.Context) error {
	for _, w := range ws {
		if err := w.Start(ctx); err != nil {
			return err
		}
	}
	return nil
}

func (ws liveWatchers) Stop() {
	for _, w := range ws {
		w.Stop()
	}
}
