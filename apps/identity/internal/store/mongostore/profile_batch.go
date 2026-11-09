package mongostore

import (
	"context"
	"errors"
	"fmt"
	"hash/fnv"
	"sync"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"

	"github.com/ivannguyendev/chatim/pkg/core/apperr"
)

var (
	errJITClosed    = fmt.Errorf("%w: just-in-time user writer closed", apperr.ErrUnavailable)
	errJITQueueFull = fmt.Errorf("%w: just-in-time user write queue full", apperr.ErrUnavailable)
)

type jitConfig struct {
	writers  int
	window   time.Duration
	maxBatch int
	queue    int
	timeout  time.Duration
}

type insertManyFunc func(ctx context.Context, docs []any) error

type jitResult struct {
	written bool
	err     error
}

type jitDoc struct {
	doc  bson.D
	done chan jitResult
}

type jitBatcher struct {
	writers []*jitWriter
	sizes   *docsHistogram
	stop    context.CancelFunc
	wg      sync.WaitGroup
	mu      sync.RWMutex
	closed  bool
}

type jitWriter struct {
	cfg         jitConfig
	queue       chan *jitDoc
	insert      insertManyFunc
	unavailable func(error) error
	sizes       *docsHistogram
}

func newJITBatcher(cfg jitConfig, insert insertManyFunc, unavailable func(error) error) *jitBatcher {
	ctx, stop := context.WithCancel(context.Background())
	b := &jitBatcher{writers: make([]*jitWriter, cfg.writers), sizes: newDocsHistogram(jitBatchBounds), stop: stop}
	for i := range b.writers {
		w := &jitWriter{cfg: cfg, queue: make(chan *jitDoc, cfg.queue), insert: insert, unavailable: unavailable, sizes: b.sizes}
		b.writers[i] = w
		b.wg.Go(func() { w.run(ctx) })
	}
	return b
}

func (b *jitBatcher) write(ctx context.Context, id string, doc bson.D) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	d := &jitDoc{doc: doc, done: make(chan jitResult, 1)}
	if err := b.enqueue(b.writerOf(id), d); err != nil {
		return false, err
	}
	select {
	case r := <-d.done:
		return r.written, r.err
	case <-ctx.Done():
		return false, ctx.Err()
	}
}

func (b *jitBatcher) writerOf(id string) *jitWriter {
	h := fnv.New32a()
	_, _ = h.Write([]byte(id))
	return b.writers[int(h.Sum32())%len(b.writers)]
}

func (b *jitBatcher) enqueue(w *jitWriter, d *jitDoc) error {
	b.mu.RLock()
	defer b.mu.RUnlock()
	if b.closed {
		return errJITClosed
	}
	select {
	case w.queue <- d:
		return nil
	default:
		return errJITQueueFull
	}
}

func (b *jitBatcher) close() {
	b.stop()
	b.mu.Lock()
	if !b.closed {
		b.closed = true
		for _, w := range b.writers {
			close(w.queue)
		}
	}
	b.mu.Unlock()
	b.wg.Wait()
}

func (w *jitWriter) run(ctx context.Context) {
	timer := time.NewTimer(w.cfg.window)
	timer.Stop()
	for {
		select {
		case d, ok := <-w.queue:
			if !ok {
				return
			}
			w.flush(ctx, w.collect(ctx, timer, d))
		case <-ctx.Done():
			for d := range w.queue {
				d.done <- jitResult{err: errJITClosed}
			}
			return
		}
	}
}

func (w *jitWriter) collect(ctx context.Context, timer *time.Timer, first *jitDoc) []*jitDoc {
	batch := []*jitDoc{first}
	if len(batch) >= w.cfg.maxBatch {
		return batch
	}
	timer.Reset(w.cfg.window)
	defer timer.Stop()
	for len(batch) < w.cfg.maxBatch {
		select {
		case d, ok := <-w.queue:
			if !ok {
				return batch
			}
			batch = append(batch, d)
		case <-timer.C:
			return batch
		case <-ctx.Done():
			return batch
		}
	}
	return batch
}

func (w *jitWriter) flush(ctx context.Context, batch []*jitDoc) {
	results := make([]jitResult, len(batch))
	if ctx.Err() != nil {
		fillResults(results, jitResult{err: errJITClosed})
	} else {
		docs := make([]any, len(batch))
		for i, d := range batch {
			docs[i] = d.doc
		}
		ictx, cancel := context.WithTimeout(ctx, w.cfg.timeout)
		err := w.insert(ictx, docs)
		cancel()
		w.sizes.observe(len(batch))
		w.classify(results, err)
	}
	for i, d := range batch {
		d.done <- results[i]
	}
}

func (w *jitWriter) classify(results []jitResult, err error) {
	if err == nil {
		fillResults(results, jitResult{written: true})
		return
	}
	bwe, ok := errors.AsType[mongo.BulkWriteException](err)
	if !ok || bwe.WriteConcernError != nil || len(bwe.WriteErrors) == 0 || !writeErrorsWithin(bwe.WriteErrors, len(results)) {
		fillResults(results, jitResult{err: w.unavailable(scrubBulkWrite(err))})
		return
	}
	fillResults(results, jitResult{written: true})
	for _, we := range bwe.WriteErrors {
		if mongo.IsDuplicateKeyError(we.WriteError) {
			results[we.Index] = jitResult{}
			continue
		}
		results[we.Index] = jitResult{err: w.unavailable(fmt.Errorf("bulk write: write error code %d", we.Code))}
	}
}

func scrubBulkWrite(err error) error {
	bwe, ok := errors.AsType[mongo.BulkWriteException](err)
	switch {
	case !ok:
		return err
	case bwe.WriteConcernError != nil:
		return fmt.Errorf("bulk write: write concern error code %d", bwe.WriteConcernError.Code)
	default:
		return fmt.Errorf("bulk write: %d write errors", len(bwe.WriteErrors))
	}
}

func writeErrorsWithin(errs []mongo.BulkWriteError, n int) bool {
	for _, we := range errs {
		if we.Index < 0 || we.Index >= n {
			return false
		}
	}
	return true
}

func fillResults(results []jitResult, r jitResult) {
	for i := range results {
		results[i] = r
	}
}
