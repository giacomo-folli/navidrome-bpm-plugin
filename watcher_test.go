package main

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func testWatcherConfig(dir string) Config {
	return Config{
		MusicDir:    dir,
		Extensions:  map[string]bool{".mp3": true},
		SettleDelay: 200 * time.Millisecond,
	}
}

type enqueueRecorder struct {
	mu    sync.Mutex
	paths []string
}

func (r *enqueueRecorder) enqueue(path string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.paths = append(r.paths, path)
}

func (r *enqueueRecorder) snapshot() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.paths...)
}

func waitFor(t *testing.T, timeout time.Duration, cond func() bool) bool {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return true
		}
		time.Sleep(50 * time.Millisecond)
	}
	return cond()
}

func startTestWatcher(t *testing.T, dir string, rec *enqueueRecorder) *watcher {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	w, err := newWatcher(testWatcherConfig(dir), rec.enqueue, &selfWrites{ttl: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	go w.run(ctx)
	t.Cleanup(func() {
		cancel()
		w.close()
	})
	return w
}

func TestWatcherSettleSingleEnqueue(t *testing.T) {
	dir := t.TempDir()
	rec := &enqueueRecorder{}
	startTestWatcher(t, dir, rec)

	// Simulate a slow copy: write in chunks with pauses shorter than settle.
	path := filepath.Join(dir, "song.mp3")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	for range 3 {
		if _, err := f.Write(make([]byte, 1024)); err != nil {
			t.Fatal(err)
		}
		time.Sleep(100 * time.Millisecond)
	}
	f.Close()

	if !waitFor(t, 5*time.Second, func() bool { return len(rec.snapshot()) >= 1 }) {
		t.Fatal("file never enqueued")
	}
	time.Sleep(600 * time.Millisecond)
	if got := rec.snapshot(); len(got) != 1 || got[0] != path {
		t.Fatalf("want exactly one enqueue of %q, got %v", path, got)
	}
}

func TestWatcherNewSubdirectory(t *testing.T) {
	dir := t.TempDir()
	rec := &enqueueRecorder{}
	startTestWatcher(t, dir, rec)

	sub := filepath.Join(dir, "album")
	if err := os.Mkdir(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(sub, "track.mp3")
	if err := os.WriteFile(path, make([]byte, 2048), 0o644); err != nil {
		t.Fatal(err)
	}

	if !waitFor(t, 5*time.Second, func() bool {
		for _, p := range rec.snapshot() {
			if p == path {
				return true
			}
		}
		return false
	}) {
		t.Fatalf("file in new subdirectory never enqueued, got %v", rec.snapshot())
	}
}

func TestWatcherIgnoresSelfWrites(t *testing.T) {
	dir := t.TempDir()
	rec := &enqueueRecorder{}
	suppress := &selfWrites{ttl: 5 * time.Second}
	ctx, cancel := context.WithCancel(context.Background())
	w, err := newWatcher(testWatcherConfig(dir), rec.enqueue, suppress)
	if err != nil {
		t.Fatal(err)
	}
	go w.run(ctx)
	t.Cleanup(func() { cancel(); w.close() })

	path := filepath.Join(dir, "tagged.mp3")
	suppress.mark(path)
	if err := os.WriteFile(path, make([]byte, 512), 0o644); err != nil {
		t.Fatal(err)
	}
	time.Sleep(time.Second)
	if got := rec.snapshot(); len(got) != 0 {
		t.Fatalf("self-write should be suppressed, got %v", got)
	}
}
