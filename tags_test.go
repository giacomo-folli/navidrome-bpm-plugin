package main

import (
	"fmt"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// makeFixture generates a short silent-ish audio file via ffmpeg.
func makeFixture(t *testing.T, ext string) string {
	t.Helper()
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg not installed")
	}
	path := filepath.Join(t.TempDir(), "fixture"+ext)
	out, err := exec.Command("ffmpeg", "-v", "error", "-y",
		"-f", "lavfi", "-i", "sine=frequency=440:duration=2",
		"-ar", "44100", path).CombinedOutput()
	if err != nil {
		t.Fatalf("ffmpeg fixture (%s): %v: %s", ext, err, out)
	}
	return path
}

func TestTagRoundTrip(t *testing.T) {
	for _, ext := range []string{".mp3", ".flac", ".m4a", ".ogg"} {
		t.Run(ext, func(t *testing.T) {
			path := makeFixture(t, ext)

			if has, val := hasBPMTag(path); has {
				t.Fatalf("fresh fixture unexpectedly has BPM tag %q", val)
			}

			if err := writeBPM(path, 128, false); err != nil {
				t.Fatalf("writeBPM: %v", err)
			}

			has, val := hasBPMTag(path)
			if !has {
				t.Fatal("BPM tag not found after write")
			}
			if n, err := strconv.ParseFloat(strings.TrimSpace(val), 64); err != nil || int(n) != 128 {
				t.Fatalf("read back BPM %q, want 128", val)
			}

			// The audio must still decode after the in-place rewrite.
			out, err := exec.Command("ffprobe", "-v", "error",
				"-show_entries", "format=duration",
				"-of", "default=nokey=1:noprint_wrappers=1", path).Output()
			if err != nil {
				t.Fatalf("ffprobe after tagging: %v", err)
			}
			dur, err := strconv.ParseFloat(strings.TrimSpace(string(out)), 64)
			if err != nil || dur < 1.5 || dur > 2.5 {
				t.Fatalf("duration after tagging = %q, want ~2s", out)
			}
		})
	}
}

func TestWriteBPMDryRun(t *testing.T) {
	path := makeFixture(t, ".mp3")
	if err := writeBPM(path, 128, true); err != nil {
		t.Fatalf("dry-run writeBPM: %v", err)
	}
	if has, _ := hasBPMTag(path); has {
		t.Fatal("dry-run must not write a tag")
	}
}

func TestWriteBPMOverwrite(t *testing.T) {
	path := makeFixture(t, ".mp3")
	for _, bpm := range []float64{100, 145} {
		if err := writeBPM(path, bpm, false); err != nil {
			t.Fatalf("writeBPM(%v): %v", bpm, err)
		}
	}
	has, val := hasBPMTag(path)
	if !has || val != "145" {
		t.Fatalf("after overwrite got (%v, %q), want (true, 145)", has, val)
	}
}

func TestIsCandidate(t *testing.T) {
	cfg := Config{Extensions: map[string]bool{".mp3": true, ".flac": true}}
	cases := []struct {
		path string
		want bool
	}{
		{"/m/a.mp3", true},
		{"/m/a.MP3", true},
		{"/m/a.flac", true},
		{"/m/a.wav", false},
		{"/m/.hidden.mp3", false},
		{fmt.Sprintf("/m/a.mp3%s.mp3", tmpSuffix), false},
	}
	for _, c := range cases {
		if got := isCandidate(cfg, c.path); got != c.want {
			t.Errorf("isCandidate(%q) = %v, want %v", c.path, got, c.want)
		}
	}
}
