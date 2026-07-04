package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/giacomo-folli/navidrome-bpm-plugin/internal/api"
	"github.com/giacomo-folli/navidrome-bpm-plugin/internal/bpm"
	"github.com/giacomo-folli/navidrome-bpm-plugin/internal/cache"
	"github.com/giacomo-folli/navidrome-bpm-plugin/internal/config"
	"github.com/giacomo-folli/navidrome-bpm-plugin/internal/metadata"
	"github.com/giacomo-folli/navidrome-bpm-plugin/internal/playlist"
	"github.com/giacomo-folli/navidrome-bpm-plugin/internal/scanner"
	"github.com/giacomo-folli/navidrome-bpm-plugin/internal/worker"
)

func main() {
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))

	cfg, err := config.Load()
	if err != nil {
		logger.Error("failed to load config", "error", err)
		os.Exit(1)
	}

	client := api.NewClient(cfg.Navidrome.URL, cfg.Navidrome.Username, cfg.Navidrome.Password)
	store, err := cache.Open(cfg.Cache.Path)
	if err != nil {
		logger.Error("failed to open cache", "error", err)
		os.Exit(1)
	}
	defer store.Close()

	detector := bpm.NewDetector()
	logger.Info("configured BPM detector", "detector", detector.Name(), "available", bpm.Availability())
	tagWriter := metadata.NewWriter(cfg.Metadata.WriteTags)
	libraryScanner := scanner.New(client, cfg.MusicDir, cfg.NavidromeMusicDir, logger)
	pool := worker.NewPool(cfg.Analysis.Workers, detector, store, tagWriter, logger)
	syncer := playlist.NewSyncer(client, playlist.Options{
		BucketSize:        cfg.Playlist.BucketSize,
		Minimum:           cfg.Playlist.Minimum,
		Maximum:           cfg.Playlist.Maximum,
		DeleteEmpty:       cfg.Playlist.DeleteEmpty,
		RescanAfterTags:   cfg.Metadata.RescanAfterWrite,
		IncludeOutOfRange: cfg.Playlist.IncludeOutOfRange,
	}, logger)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	run := func() {
		if err := runOnce(ctx, logger, libraryScanner, pool, syncer); err != nil {
			logger.Error("run failed", "error", err)
		}
	}

	run()
	if cfg.Scan.Interval <= 0 {
		return
	}

	ticker := time.NewTicker(cfg.Scan.Interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			logger.Info("stopping")
			return
		case <-ticker.C:
			run()
		}
	}
}

func runOnce(ctx context.Context, logger *slog.Logger, libraryScanner *scanner.Scanner, pool *worker.Pool, syncer *playlist.Syncer) error {
	logger.Info("Scanning library...")
	tracks, err := libraryScanner.Tracks(ctx)
	if err != nil {
		return err
	}
	logger.Info("tracks discovered", "count", len(tracks))

	results, stats, err := pool.Analyze(ctx, tracks)
	if err != nil {
		return err
	}
	logger.Info("cache status", "cached", stats.Cached, "new_or_changed", stats.Analyzed, "failed", stats.Failed)

	if err := syncer.Sync(ctx, results); err != nil {
		return err
	}
	logger.Info("Finished.")
	return nil
}
