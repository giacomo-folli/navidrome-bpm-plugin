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

	if err := writeBPM(path, bpm, cfg.DryRun); err != nil {
		fails.record(path, info)
		return fmt.Errorf("tag write: %w", err)
	}
	if !cfg.DryRun {
		suppress.mark(path)
		slog.Info("tagged", "path", path, "bpm", int(bpm))
	}
	return nil
}
