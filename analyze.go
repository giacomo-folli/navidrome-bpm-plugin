package main

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	minValidBPM    = 40
	maxValidBPM    = 250
	minBeats       = 8
	perFileTimeout = 2 * time.Minute
)

// tempoCommand resolves the beat-tracking CLI once. The unified `aubio` tool
// (Python distribution, Debian aubio-tools) and the classic `aubiotrack`
// binary (Arch's aubio package) both print one beat timestamp per line.
var tempoCommand = sync.OnceValue(func() []string {
	if _, err := exec.LookPath("aubio"); err == nil {
		return []string{"aubio", "tempo"}
	}
	if _, err := exec.LookPath("aubiotrack"); err == nil {
		return []string{"aubiotrack"}
	}
	return nil
})

// detectBPM analyzes a file with aubio's beat tracker. If aubio cannot decode
// the file directly, it retries on a temporary WAV produced by ffmpeg.
func detectBPM(ctx context.Context, path string) (float64, error) {
	ctx, cancel := context.WithTimeout(ctx, perFileTimeout)
	defer cancel()

	beats, err := runAubioTempo(ctx, path)
	if err != nil || len(beats) < minBeats {
		fbBPM, fbErr := detectBPMViaFFmpeg(ctx, path)
		if fbErr == nil {
			return fbBPM, nil
		}
		if err == nil {
			err = fmt.Errorf("only %d beats detected", len(beats))
		}
		return 0, fmt.Errorf("aubio: %w (ffmpeg fallback: %v)", err, fbErr)
	}
	return bpmFromBeats(beats)
}

// runAubioTempo returns the beat timestamps (seconds) reported by aubio.
func runAubioTempo(ctx context.Context, path string) ([]float64, error) {
	base := tempoCommand()
	if base == nil {
		return nil, fmt.Errorf("no aubio beat tracker found in PATH")
	}
	cmd := exec.CommandContext(ctx, base[0], append(base[1:], path)...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("%w: %s", err, firstLine(stderr.String()))
	}
	return parseBeatLines(stdout.String()), nil
}

// parseBeatLines extracts one float per line, skipping anything non-numeric
// (some aubio versions print headers or warnings on stdout).
func parseBeatLines(out string) []float64 {
	var beats []float64
	for _, line := range strings.Split(out, "\n") {
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		if v, err := strconv.ParseFloat(fields[0], 64); err == nil {
			beats = append(beats, v)
		}
	}
	return beats
}

// bpmFromBeats derives the tempo from beat timestamps using the median
// inter-beat interval, which is robust against dropped or spurious beats.
func bpmFromBeats(beats []float64) (float64, error) {
	if len(beats) < minBeats {
		return 0, fmt.Errorf("too few beats (%d) for a reliable tempo", len(beats))
	}
	deltas := make([]float64, 0, len(beats)-1)
	for i := 1; i < len(beats); i++ {
		if d := beats[i] - beats[i-1]; d > 0 {
			deltas = append(deltas, d)
		}
	}
	if len(deltas) < minBeats-1 {
		return 0, fmt.Errorf("too few valid beat intervals (%d)", len(deltas))
	}
	sort.Float64s(deltas)
	median := deltas[len(deltas)/2]
	if len(deltas)%2 == 0 {
		median = (median + deltas[len(deltas)/2-1]) / 2
	}
	bpm := float64(int(60/median + 0.5))
	if bpm < minValidBPM || bpm > maxValidBPM {
		return 0, fmt.Errorf("implausible tempo %.0f BPM (valid range %d-%d)", bpm, minValidBPM, maxValidBPM)
	}
	return bpm, nil
}

// detectBPMViaFFmpeg decodes the file to a temporary WAV and analyzes that.
func detectBPMViaFFmpeg(ctx context.Context, path string) (float64, error) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		return 0, fmt.Errorf("ffmpeg not available")
	}
	tmp, err := os.CreateTemp("", "bpmd-*.wav")
	if err != nil {
		return 0, err
	}
	tmp.Close()
	defer os.Remove(tmp.Name())

	slog.Debug("aubio could not decode directly, trying ffmpeg fallback", "path", filepath.Base(path))
	cmd := exec.CommandContext(ctx, "ffmpeg", "-v", "error", "-y",
		"-i", path, "-ac", "1", "-ar", "44100", "-f", "wav", tmp.Name())
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return 0, fmt.Errorf("ffmpeg decode: %w: %s", err, firstLine(stderr.String()))
	}

	beats, err := runAubioTempo(ctx, tmp.Name())
	if err != nil {
		return 0, err
	}
	return bpmFromBeats(beats)
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}
