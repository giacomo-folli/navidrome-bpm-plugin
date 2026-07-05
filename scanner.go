package main

import (
	"context"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"path/filepath"
	"strings"
)

// fullScan walks root and enqueues every candidate audio file.
func fullScan(ctx context.Context, cfg Config, enqueue func(string)) error {
	return filepath.WalkDir(cfg.MusicDir, func(path string, d fs.DirEntry, err error) error {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err != nil {
			slog.Warn("scan error", "path", path, "err", err)
			return nil
		}
		name := d.Name()
		if strings.HasPrefix(name, ".") {
			if d.IsDir() && path != cfg.MusicDir {
				return filepath.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			return nil
		}
		if isCandidate(cfg, path) {
			enqueue(path)
		}
		return nil
	})
}

// listUntagged walks the music dir and prints every candidate file that has
// no BPM tag to stdout, one path per line.
func listUntagged(ctx context.Context, cfg Config, out io.Writer) error {
	var files, untagged int
	err := fullScan(ctx, cfg, func(path string) {
		files++
		if has, _ := hasBPMTag(path); !has {
			untagged++
			fmt.Fprintln(out, path)
		}
	})
	slog.Info("scan complete", "files", files, "untagged", untagged)
	return err
}

// isCandidate reports whether a path looks like an audio file we handle.
func isCandidate(cfg Config, path string) bool {
	if isTempFile(path) || strings.HasPrefix(filepath.Base(path), ".") {
		return false
	}
	return cfg.Extensions[strings.ToLower(filepath.Ext(path))]
}
