package main

import (
	"context"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
)

// watcher recursively watches the music dir and enqueues files once they
// have settled (stopped changing), so partially-copied files aren't analyzed
// mid-write.
type watcher struct {
	fs       *fsnotify.Watcher
	cfg      Config
	enqueue  func(string)
	suppress *selfWrites

	mu      sync.Mutex
	pending map[string]*time.Timer
}

func newWatcher(cfg Config, enqueue func(string), suppress *selfWrites) (*watcher, error) {
	fsw, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, err
	}
	w := &watcher{
		fs:       fsw,
		cfg:      cfg,
		enqueue:  enqueue,
		suppress: suppress,
		pending:  make(map[string]*time.Timer),
	}
	if err := w.addRecursive(cfg.MusicDir); err != nil {
		fsw.Close()
		return nil, err
	}
	return w, nil
}

func (w *watcher) close() {
	w.fs.Close()
	w.mu.Lock()
	defer w.mu.Unlock()
	for _, t := range w.pending {
		t.Stop()
	}
}

// addRecursive registers a watch on dir and every subdirectory (fsnotify is
// non-recursive).
func (w *watcher) addRecursive(dir string) error {
	return filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			slog.Warn("watch registration error", "path", path, "err", err)
			return nil
		}
		if !d.IsDir() {
			return nil
		}
		if strings.HasPrefix(d.Name(), ".") && path != dir {
			return filepath.SkipDir
		}
		if err := w.fs.Add(path); err != nil {
			slog.Warn("cannot watch directory", "path", path, "err", err)
		}
		return nil
	})
}

func (w *watcher) run(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case ev, ok := <-w.fs.Events:
			if !ok {
				return
			}
			w.handle(ctx, ev)
		case err, ok := <-w.fs.Errors:
			if !ok {
				return
			}
			slog.Warn("watcher error", "err", err)
		}
	}
}

func (w *watcher) handle(ctx context.Context, ev fsnotify.Event) {
	path := ev.Name

	if ev.Op.Has(fsnotify.Remove) || ev.Op.Has(fsnotify.Rename) {
		w.cancelPending(path)
		return
	}
	if !ev.Op.Has(fsnotify.Create) && !ev.Op.Has(fsnotify.Write) {
		return
	}

	if ev.Op.Has(fsnotify.Create) {
		if info, err := os.Stat(path); err == nil && info.IsDir() {
			// New directory: watch it, then scan it — files may have landed
			// inside before the watch went live.
			if strings.HasPrefix(filepath.Base(path), ".") {
				return
			}
			slog.Debug("watching new directory", "path", path)
			if err := w.addRecursive(path); err != nil {
				slog.Warn("cannot watch new directory", "path", path, "err", err)
			}
			sub := w.cfg
			sub.MusicDir = path
			if err := fullScan(ctx, sub, func(p string) { w.arm(p) }); err != nil {
				slog.Warn("scan of new directory incomplete", "path", path, "err", err)
			}
			return
		}
	}

	if !isCandidate(w.cfg, path) {
		return
	}
	if w.suppress.active(path) {
		slog.Debug("ignoring event from own tag write", "path", path)
		return
	}
	w.arm(path)
}

// arm (re)starts the settle timer for a path. Every new event resets it, so a
// file being copied keeps deferring until writes stop.
func (w *watcher) arm(path string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if t, ok := w.pending[path]; ok {
		t.Reset(w.cfg.SettleDelay)
		return
	}
	w.pending[path] = time.AfterFunc(w.cfg.SettleDelay, func() {
		w.settled(path)
	})
}

func (w *watcher) cancelPending(path string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if t, ok := w.pending[path]; ok {
		t.Stop()
		delete(w.pending, path)
	}
}

// settled fires when no events arrived for SettleDelay. Double-stat to make
// sure the size is stable before enqueueing (fsnotify has no portable
// close-after-write event).
func (w *watcher) settled(path string) {
	before, err := os.Stat(path)
	if err != nil {
		w.cancelPending(path)
		return
	}
	time.Sleep(500 * time.Millisecond)
	after, err := os.Stat(path)
	if err != nil {
		w.cancelPending(path)
		return
	}
	if before.Size() != after.Size() {
		w.mu.Lock()
		if t, ok := w.pending[path]; ok {
			t.Reset(w.cfg.SettleDelay)
		}
		w.mu.Unlock()
		return
	}
	w.cancelPending(path)
	w.enqueue(path)
}
