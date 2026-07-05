package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
)

// processFile runs the full pipeline for one file: cheap checks first, then
// aubio analysis, then the in-place tag write.
func processFile(ctx context.Context, cfg Config, path string, fails *failCache, suppress *selfWrites) error {
	info, err := os.Stat(path)
	if err != nil {
		slog.Debug("file gone, skipping", "path", path)
		return nil
	}
	if info.Size() == 0 {
		slog.Debug("empty file, skipping", "path", path)
		return nil
	}
	if fails.known(path, info) {
		slog.Debug("known failure, skipping", "path", path)
		return nil
	}

	if has, val := hasBPMTag(path); has && !cfg.Overwrite {
		slog.Debug("BPM tag present, skipping", "path", path, "bpm", val)
		return nil
	}

	bpm, err := detectBPM(ctx, path)
	if err != nil {
		fails.record(path, info)
		return fmt.Errorf("bpm detection: %w", err)
	}

	// Mark before writing: the watcher sees our write events while writeBPM is
	// still running, so marking afterwards loses the race and every tagged
	// file gets re-enqueued (and re-analyzed) once the settle timer fires.
	suppress.mark(path)
	if err := writeBPM(path, bpm, cfg.DryRun); err != nil {
		fails.record(path, info)
		return fmt.Errorf("tag write: %w", err)
	}
	if !cfg.DryRun {
		suppress.mark(path) // refresh so the TTL covers slow writes too
		slog.Info("tagged", "path", path, "bpm", int(bpm))
		if has, _ := hasBPMTag(path); !has {
			slog.Warn("tag written but does not read back; file would be re-analyzed on rescan", "path", path)
		}
	}
	return nil
}
