package bpm

import (
	"bytes"
	"fmt"
	"math"
	"os/exec"
	"sort"
	"strconv"
	"strings"
)

type Aubio struct{}

func (Aubio) Name() string {
	return "aubio"
}

func (Aubio) Detect(path string) (float64, error) {
	cmd := exec.Command("aubiotrack", "-i", path)
	out, err := cmd.Output()
	if err != nil {
		return 0, err
	}
	return parseAubioBPM(out)
}

// parseAubioBPM computes BPM from the beat timestamps (in seconds) that
// aubiotrack prints, one per line. It derives tempo from the median
// inter-beat interval: BPM = 60 / median(interval).
func parseAubioBPM(out []byte) (float64, error) {
	var beats []float64
	for _, line := range bytes.Split(out, []byte("\n")) {
		text := strings.TrimSpace(string(line))
		if text == "" {
			continue
		}
		if ts, err := strconv.ParseFloat(text, 64); err == nil && ts >= 0 {
			beats = append(beats, ts)
		}
	}
	if len(beats) < 2 {
		return 0, fmt.Errorf("not enough beats in aubio output to compute BPM (got %d)", len(beats))
	}

	var intervals []float64
	for i := 1; i < len(beats); i++ {
		dt := beats[i] - beats[i-1]
		if dt > 0 {
			intervals = append(intervals, dt)
		}
	}
	if len(intervals) == 0 {
		return 0, fmt.Errorf("no valid beat intervals found in aubio output")
	}

	sort.Float64s(intervals)
	median := intervals[len(intervals)/2]
	bpm := 60.0 / median
	return math.Round(bpm*10) / 10, nil
}
