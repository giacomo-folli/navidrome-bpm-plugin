package main

import (
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"runtime"
	"strings"
	"time"
)

type Config struct {
	MusicDir       string
	Extensions     map[string]bool
	Workers        int
	DryRun         bool
	Overwrite      bool
	LogLevel       slog.Level
	SettleDelay    time.Duration
	RescanInterval time.Duration
}

const defaultExtensions = "mp3,flac,m4a,ogg,opus"

func loadConfig(args []string) (Config, error) {
	fs := flag.NewFlagSet("bpmd", flag.ContinueOnError)

	var cfg Config
	var exts, level string
	fs.StringVar(&cfg.MusicDir, "dir", "", "music directory to watch (required)")
	fs.StringVar(&exts, "ext", defaultExtensions, "comma-separated audio extensions to process")
	fs.IntVar(&cfg.Workers, "workers", max(1, runtime.NumCPU()/2), "number of parallel analysis workers")
	fs.BoolVar(&cfg.DryRun, "dry-run", false, "log what would be written without modifying files")
	fs.BoolVar(&cfg.Overwrite, "overwrite", false, "re-analyze and overwrite existing BPM tags")
	fs.StringVar(&level, "log-level", "info", "log level: debug|info|warn|error")
	fs.DurationVar(&cfg.SettleDelay, "settle", 3*time.Second, "wait for a file to stop changing before analyzing it")
	fs.DurationVar(&cfg.RescanInterval, "rescan", 0, "interval between full rescans (0 = only at startup)")

	if err := fs.Parse(args); err != nil {
		return cfg, err
	}
	if cfg.MusicDir == "" && fs.NArg() == 1 {
		cfg.MusicDir = fs.Arg(0)
	}
	if cfg.MusicDir == "" {
		return cfg, errors.New("music directory is required: bpmd -dir /path/to/music")
	}
	info, err := os.Stat(cfg.MusicDir)
	if err != nil {
		return cfg, fmt.Errorf("music directory: %w", err)
	}
	if !info.IsDir() {
		return cfg, fmt.Errorf("music directory %q is not a directory", cfg.MusicDir)
	}
	if cfg.Workers < 1 {
		cfg.Workers = 1
	}

	cfg.Extensions = make(map[string]bool)
	for _, e := range strings.Split(exts, ",") {
		e = strings.ToLower(strings.TrimPrefix(strings.TrimSpace(e), "."))
		if e != "" {
			cfg.Extensions["."+e] = true
		}
	}
	if len(cfg.Extensions) == 0 {
		return cfg, errors.New("-ext must list at least one extension")
	}

	switch strings.ToLower(level) {
	case "debug":
		cfg.LogLevel = slog.LevelDebug
	case "info":
		cfg.LogLevel = slog.LevelInfo
	case "warn":
		cfg.LogLevel = slog.LevelWarn
	case "error":
		cfg.LogLevel = slog.LevelError
	default:
		return cfg, fmt.Errorf("invalid -log-level %q", level)
	}

	return cfg, nil
}
