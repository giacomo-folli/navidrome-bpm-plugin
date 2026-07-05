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

// tempoEngine describes the resolved aubio CLI. The unified `aubio tempo`
// tool prints the final tempo as a single "142.42 bpm" line, while the
// classic `aubiotrack` binary prints one beat timestamp per line that we
// reduce to a tempo ourselves.
type tempoEngine struct {
	args   []string
	direct bool // output is the BPM itself, not beat timestamps
}

// tempoCommand resolves the beat-tracking CLI once.
var tempoCommand = sync.OnceValue(func() *tempoEngine {
	if _, err := exec.LookPath("aubio"); err == nil {
		return &tempoEngine{args: []string{"aubio", "tempo"}, direct: true}
	}
	if _, err := exec.LookPath("aubiotrack"); err == nil {
		return &tempoEngine{args: []string{"aubiotrack"}}
	}
	return nil
})

// detectBPM analyzes a file with aubio's beat tracker. If aubio cannot decode
// the file directly, it retries on a temporary WAV produced by ffmpeg.
func detectBPM(ctx context.Context, path string) (float64, error) {
	ctx, cancel := context.WithTimeout(ctx, perFileTimeout)
	defer cancel()

	bpm, err := runAubioTempo(ctx, path)
	if err != nil {
		fbBPM, fbErr := detectBPMViaFFmpeg(ctx, path)
		if fbErr == nil {
			return fbBPM, nil
		}
		return 0, fmt.Errorf("aubio: %w (ffmpeg fallback: %v)", err, fbErr)
	}
	return bpm, nil
}

// runAubioTempo runs the resolved aubio CLI on path and returns the tempo.
func runAubioTempo(ctx context.Context, path string) (float64, error) {
	engine := tempoCommand()
	if engine == nil {
		return 0, fmt.Errorf("no aubio beat tracker found in PATH")
	}
	cmd := exec.CommandContext(ctx, engine.args[0], append(engine.args[1:], path)...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return 0, fmt.Errorf("%w: %s", err, firstLine(stderr.String()))
	}
	values := parseBeatLines(stdout.String())
	if engine.direct {
		return bpmFromDirect(values)
	}
	return bpmFromBeats(values)
}

// bpmFromDirect interprets the output of the unified `aubio tempo` command,
// which already prints the estimated tempo ("142.42 bpm").
func bpmFromDirect(values []float64) (float64, error) {
	if len(values) == 0 {
		return 0, fmt.Errorf("aubio tempo printed no result")
	}
	bpm := float64(int(values[0] + 0.5))
	if bpm < minValidBPM || bpm > maxValidBPM {
		return 0, fmt.Errorf("implausible tempo %.0f BPM (valid range %d-%d)", bpm, minValidBPM, maxValidBPM)
	}
	return bpm, nil
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

	return runAubioTempo(ctx, tmp.Name())
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}
