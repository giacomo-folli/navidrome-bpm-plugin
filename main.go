package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"
)

func main() {
	cfg, err := loadConfig(os.Args[1:])
	if err != nil {
		fmt.Fprintln(os.Stderr, "bpmd:", err)
		os.Exit(2)
	}

	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: cfg.LogLevel})))

	if cfg.List {
		// Tag reading is pure Go, so no aubio/ffmpeg prereqs apply here.
		if err := listUntagged(context.Background(), cfg, os.Stdout); err != nil {
			slog.Error(err.Error())
			os.Exit(1)
		}
		return
	}

	if err := checkPrereqs(); err != nil {
		slog.Error(err.Error())
		os.Exit(1)
	}

	if err := run(cfg); err != nil {
		slog.Error(err.Error())
		os.Exit(1)
	}
}

func checkPrereqs() error {
	engine := tempoCommand()
	if engine == nil {
		return fmt.Errorf("no aubio beat tracker (aubio or aubiotrack) found in PATH; install it (Arch: pacman -S aubio, Debian: apt install aubio-tools)")
	}
	slog.Info("found beat tracker", "command", strings.Join(engine.args, " "))
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		slog.Warn("ffmpeg not found in PATH; decode fallback and m4a/ogg/opus tagging will be unavailable")
	}
	return nil
}

func run(cfg Config) error {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	jobs := make(chan string, 4096)
	fails := newFailCache()
	suppress := &selfWrites{ttl: 2 * cfg.SettleDelay}

	enqueue := func(path string) {
		if ctx.Err() != nil {
			return
		}
		select {
		case jobs <- path:
		default:
			slog.Warn("job queue full, dropping file (a rescan will pick it up)", "path", path)
		}
	}

	watcher, err := newWatcher(cfg, enqueue, suppress)
	if err != nil {
		return fmt.Errorf("starting watcher: %w", err)
	}
	defer watcher.close()

	// A path can be queued twice (initial scan + a watcher event); make sure
	// two workers never process the same file at once — they would race on
	// the same temp file during the tag write.
	var inflight sync.Map
	var workers sync.WaitGroup
	for range cfg.Workers {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for {
				select {
				case <-ctx.Done():
					return
				case path := <-jobs:
					if _, busy := inflight.LoadOrStore(path, struct{}{}); busy {
						continue
					}
					if err := processFile(ctx, cfg, path, fails, suppress); err != nil {
						slog.Warn("processing failed", "path", path, "err", err)
					}
					inflight.Delete(path)
				}
			}
		}()
	}

	go watcher.run(ctx)

	slog.Info("starting", "dir", cfg.MusicDir, "workers", cfg.Workers, "dry_run", cfg.DryRun, "overwrite", cfg.Overwrite)
	if err := fullScan(ctx, cfg, enqueue); err != nil {
		slog.Warn("initial scan incomplete", "err", err)
	}

	if cfg.RescanInterval > 0 {
		go func() {
			ticker := time.NewTicker(cfg.RescanInterval)
			defer ticker.Stop()
			for {
				select {
				case <-ctx.Done():
					return
				case <-ticker.C:
					slog.Info("periodic rescan")
					if err := fullScan(ctx, cfg, enqueue); err != nil {
						slog.Warn("rescan incomplete", "err", err)
					}
				}
			}
		}()
	}

	<-ctx.Done()
	slog.Info("shutting down, waiting for in-flight work")
	workers.Wait()
	return nil
}
