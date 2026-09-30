package seedload

import (
	"context"
	"fmt"
	"math/rand/v2"
	"sync"
	"sync/atomic"
	"time"

	"github.com/ivannguyendev/chatim/tools/poc/internal/msgtext"
	"github.com/ivannguyendev/chatim/tools/poc/internal/roomset"
)

type Row struct {
	Room, Seq uint64
	From      string
	Text      string
}

type Insert func(ctx context.Context, rows []Row) error

func Run(ctx context.Context, jobs []roomset.Job, workers int, texts []string, insert Insert) (int64, error) {
	var inserted atomic.Int64
	stop := progress(&inserted, total(jobs))
	defer stop()
	feed := make(chan roomset.Job)
	errs := make(chan error, workers)
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Go(func() {
			rng := rand.New(rand.NewPCG(rand.Uint64(), rand.Uint64()))
			for job := range feed {
				rows := make([]Row, 0, len(job.Rooms)*int(job.To-job.From+1))
				for _, room := range job.Rooms {
					for seq := job.From; seq <= job.To; seq++ {
						rows = append(rows, Row{Room: room, Seq: seq, From: msgtext.Sender(rng), Text: msgtext.Pick(texts, rng)})
					}
				}
				if err := insert(ctx, rows); err != nil {
					errs <- fmt.Errorf("insert rooms %d..: %w", job.Rooms[0], err)
					return
				}
				inserted.Add(int64(len(rows)))
			}
		})
	}
	var runErr error
loop:
	for _, job := range jobs {
		select {
		case feed <- job:
		case runErr = <-errs:
			break loop
		case <-ctx.Done():
			runErr = ctx.Err()
			break loop
		}
	}
	close(feed)
	wg.Wait()
	if runErr == nil {
		select {
		case runErr = <-errs:
		default:
		}
	}
	return inserted.Load(), runErr
}

func total(jobs []roomset.Job) int64 {
	var n int64
	for _, j := range jobs {
		n += int64(len(j.Rooms)) * int64(j.To-j.From+1)
	}
	return n
}

func progress(done *atomic.Int64, total int64) func() {
	t := time.NewTicker(5 * time.Second)
	quit := make(chan struct{})
	go func() {
		for {
			select {
			case <-t.C:
				fmt.Printf("  %d / %d messages\n", done.Load(), total)
			case <-quit:
				return
			}
		}
	}()
	return func() { t.Stop(); close(quit) }
}
