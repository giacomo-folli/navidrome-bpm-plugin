package cache

import (
	"path/filepath"
	"testing"
	"time"
)

func TestGetFresh(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "cache.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	modified := time.Unix(1000, 0)
	if err := store.Upsert(Track{Path: "/music/a.flac", Modified: modified, BPM: 123, Confidence: 0.9}); err != nil {
		t.Fatal(err)
	}
	got, ok, err := store.GetFresh("/music/a.flac", modified)
	if err != nil {
		t.Fatal(err)
	}
	if !ok || got.BPM != 123 {
		t.Fatalf("fresh cache = (%v, %v), want bpm 123 and ok", got, ok)
	}
	_, ok, err = store.GetFresh("/music/a.flac", modified.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Fatalf("stale cache returned fresh")
	}
}
