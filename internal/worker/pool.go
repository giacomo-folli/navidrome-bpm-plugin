package worker

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/giacomo-folli/navidrome-bpm-plugin/internal/bpm"
	"github.com/giacomo-folli/navidrome-bpm-plugin/internal/cache"
	"github.com/giacomo-folli/navidrome-bpm-plugin/internal/metadata"
	"github.com/giacomo-folli/navidrome-bpm-plugin/internal/scanner"
)

type Result struct {
	SongID string
	Path   string
	BPM    float64
}

type Stats struct {
	Cached   int
	Analyzed int
	Failed   int
}

type Pool struct {
	workers  int
	detector bpm.Detector
	store    *cache.Store
	writer   metadata.Writer
	logger   *slog.Logger
}

func NewPool(workers int, detector bpm.Detector, store *cache.Store, writer metadata.Writer, logger *slog.Logger) *Pool {
	if workers < 1 {
		workers = 1
	}
	return &Pool{workers: workers, detector: detector, store: store, writer: writer, logger: logger}
}

func (p *Pool) Analyze(ctx context.Context, tracks []scanner.Track) ([]Result, Stats, error) {
	jobs := make(chan scanner.Track)
	results := make(chan Result)
	errs := make(chan struct{})
	var wg sync.WaitGroup
	var mu sync.Mutex
	stats := Stats{}

	for i := 0; i < p.workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for tr := range jobs {
				modified := time.Unix(tr.Modified, 0)
				cached, ok, err := p.store.GetFresh(tr.Path, modified)
				if err != nil {
					p.logger.Error("cache read failed", "path", tr.Path, "error", err)
					errs <- struct{}{}
					continue
				}
				if ok {
					mu.Lock()
					stats.Cached++
					mu.Unlock()
					results <- Result{SongID: tr.ID, Path: tr.Path, BPM: cached.BPM}
					continue
				}
				b, err := p.detector.Detect(tr.Path)
				if err != nil {
					p.logger.Error("bpm detection failed", "path", tr.Path, "error", err)
					mu.Lock()
					stats.Failed++
					mu.Unlock()
					errs <- struct{}{}
					continue
				}
				if err := p.store.Upsert(cache.Track{Path: tr.Path, Modified: modified, BPM: b, Confidence: 0}); err != nil {
					p.logger.Error("cache update failed", "path", tr.Path, "error", err)
					errs <- struct{}{}
					continue
				}
				if err := p.writer.WriteBPM(tr.Path, b); err != nil {
					p.logger.Error("metadata write failed", "path", tr.Path, "error", err)
				}
				mu.Lock()
				stats.Analyzed++
				mu.Unlock()
				results <- Result{SongID: tr.ID, Path: tr.Path, BPM: b}
			}
		}()
	}

	go func() {
		defer close(jobs)
		for _, tr := range tracks {
			select {
			case <-ctx.Done():
				return
			case jobs <- tr:
			}
		}
	}()

	go func() {
		wg.Wait()
		close(results)
		close(errs)
	}()

	var out []Result
	for results != nil || errs != nil {
		select {
		case r, ok := <-results:
			if !ok {
				results = nil
				continue
			}
			out = append(out, r)
		case _, ok := <-errs:
			if !ok {
				errs = nil
			}
		case <-ctx.Done():
			return out, stats, ctx.Err()
		}
	}
	return out, stats, nil
}
