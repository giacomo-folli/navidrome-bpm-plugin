package main

import (
	"os"
	"sync"
	"time"
)

type fileSig struct {
	size  int64
	mtime time.Time
}

// failCache remembers files that failed analysis so they aren't retried
// within this run unless they change. In-memory only: skip-if-tagged already
// makes the daemon idempotent across restarts.
type failCache struct {
	mu sync.Mutex
	m  map[string]fileSig
}

func newFailCache() *failCache {
	return &failCache{m: make(map[string]fileSig)}
}

func (c *failCache) record(path string, info os.FileInfo) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.m[path] = fileSig{size: info.Size(), mtime: info.ModTime()}
}

func (c *failCache) known(path string, info os.FileInfo) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	sig, ok := c.m[path]
	return ok && sig.size == info.Size() && sig.mtime.Equal(info.ModTime())
}

// selfWrites suppresses watcher events caused by our own tag writes; without
// it, -overwrite mode would re-analyze its own output forever.
type selfWrites struct {
	ttl time.Duration
	m   sync.Map // path -> time.Time
}

func (s *selfWrites) mark(path string) {
	s.m.Store(path, time.Now())
}

func (s *selfWrites) active(path string) bool {
	v, ok := s.m.Load(path)
	if !ok {
		return false
	}
	if time.Since(v.(time.Time)) > s.ttl {
		s.m.Delete(path)
		return false
	}
	return true
}
