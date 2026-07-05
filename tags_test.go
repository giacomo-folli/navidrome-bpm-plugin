package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/dhowden/tag"
)

// makeFixture generates a short silent-ish audio file via ffmpeg.
func makeFixture(t *testing.T, ext string) string {
	t.Helper()
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg not installed")
	}
	path := filepath.Join(t.TempDir(), "fixture"+ext)
	rate := "44100"
	if ext == ".opus" {
		rate = "48000" // libopus only encodes at 48k and below
	}
	out, err := exec.Command("ffmpeg", "-v", "error", "-y",
		"-f", "lavfi", "-i", "sine=frequency=440:duration=2",
		"-ar", rate, path).CombinedOutput()
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

// countStreams returns the number of streams ffprobe sees in the file.
func countStreams(t *testing.T, path string) int {
	t.Helper()
	out, err := exec.Command("ffprobe", "-v", "error", "-show_entries",
		"stream=index", "-of", "csv=p=0", path).Output()
	if err != nil {
		t.Fatalf("ffprobe streams: %v", err)
	}
	return len(strings.Fields(string(out)))
}

// TestTagOpusWithCoverArt reproduces the yt-dlp case: an opus file whose
// METADATA_BLOCK_PICTURE cover the ogg demuxer exposes as a video stream,
// which the plain -map 0 remux cannot write back. The tag write must still
// succeed and the art must survive.
func TestTagOpusWithCoverArt(t *testing.T) {
	path := makeFixture(t, ".opus")

	// Build a tiny jpeg and embed it the way the daemon re-embeds art.
	jpg := filepath.Join(t.TempDir(), "cover.jpg")
	out, err := exec.Command("ffmpeg", "-v", "error", "-y",
		"-f", "lavfi", "-i", "color=red:size=64x64", "-frames:v", "1", jpg).CombinedOutput()
	if err != nil {
		t.Fatalf("ffmpeg cover fixture: %v: %s", err, out)
	}
	pic, err := os.ReadFile(jpg)
	if err != nil {
		t.Fatal(err)
	}
	meta := filepath.Join(t.TempDir(), "meta.txt")
	if err := os.WriteFile(meta, []byte(";FFMETADATA1\nMETADATA_BLOCK_PICTURE="+
		escapeFFMeta(vorbisPicture(pic))+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	withArt := filepath.Join(t.TempDir(), "art.opus")
	out, err = exec.Command("ffmpeg", "-v", "error", "-y", "-i", path,
		"-f", "ffmetadata", "-i", meta, "-map_metadata", "1",
		"-map", "0:a", "-c", "copy", withArt).CombinedOutput()
	if err != nil {
		t.Fatalf("ffmpeg embed art: %v: %s", err, out)
	}
	if n := countStreams(t, withArt); n != 2 {
		t.Fatalf("art fixture has %d streams, want 2 (audio + attached pic)", n)
	}

	if err := writeBPM(withArt, 128, false); err != nil {
		t.Fatalf("writeBPM: %v", err)
	}
	if has, val := hasBPMTag(withArt); !has || strings.TrimSpace(val) != "128" {
		t.Fatalf("after write got (%v, %q), want (true, 128)", has, val)
	}
	if n := countStreams(t, withArt); n != 2 {
		t.Fatalf("cover art lost: %d streams after tagging, want 2", n)
	}
}

// TestHasBPMTagBrokenUTF16Frame reproduces the spotdown.app case: the tag
// carries a UTF-16 text frame with an odd byte count, which makes dhowden/tag
// abort the whole parse and hide a valid TBPM. hasBPMTag must fall back to a
// per-frame reader so the file is not re-analyzed forever.
func TestHasBPMTagBrokenUTF16Frame(t *testing.T) {
	src, err := os.ReadFile(makeFixture(t, ".mp3"))
	if err != nil {
		t.Fatal(err)
	}

	// ID3v2.3 TIT2 frame: encoding 0x01 (UTF-16 with BOM) + BOM + 3 bytes,
	// an odd-length UTF-16 payload.
	body := []byte{0x01, 0xFF, 0xFE, 'A', 0x00, 'B'}
	frame := append([]byte{'T', 'I', 'T', '2', 0, 0, 0, byte(len(body)), 0, 0}, body...)
	tagSize := len(frame)
	header := []byte{'I', 'D', '3', 3, 0, 0,
		byte(tagSize >> 21 & 0x7F), byte(tagSize >> 14 & 0x7F),
		byte(tagSize >> 7 & 0x7F), byte(tagSize & 0x7F)}

	path := filepath.Join(t.TempDir(), "broken.mp3")
	if err := os.WriteFile(path, append(append(header, frame...), src...), 0o644); err != nil {
		t.Fatal(err)
	}

	// The fixture must actually trip dhowden/tag, or the fallback under test
	// is never reached.
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tag.ReadFrom(f); err == nil {
		t.Fatal("dhowden/tag parsed the broken fixture; it no longer exercises the fallback")
	}
	f.Close()

	if has, val := hasBPMTag(path); has {
		t.Fatalf("broken fixture unexpectedly has BPM tag %q", val)
	}
	if err := writeBPM(path, 158, false); err != nil {
		t.Fatalf("writeBPM: %v", err)
	}
	has, val := hasBPMTag(path)
	if !has || val != "158" {
		t.Fatalf("after write got (%v, %q), want (true, 158)", has, val)
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

func TestListUntagged(t *testing.T) {
	dir := t.TempDir()
	tagged := filepath.Join(dir, "tagged.mp3")
	untagged := filepath.Join(dir, "sub", "untagged.mp3")
	if err := os.Mkdir(filepath.Dir(untagged), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(makeFixture(t, ".mp3"), tagged); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(makeFixture(t, ".mp3"), untagged); err != nil {
		t.Fatal(err)
	}
	if err := writeBPM(tagged, 128, false); err != nil {
		t.Fatal(err)
	}

	cfg := Config{MusicDir: dir, Extensions: map[string]bool{".mp3": true}}
	var out strings.Builder
	if err := listUntagged(context.Background(), cfg, &out); err != nil {
		t.Fatalf("listUntagged: %v", err)
	}
	if got := strings.TrimSpace(out.String()); got != untagged {
		t.Fatalf("listUntagged output %q, want %q", got, untagged)
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
