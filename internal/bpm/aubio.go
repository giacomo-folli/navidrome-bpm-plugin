package bpm

import (
	"bytes"
	"fmt"
	"os/exec"
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

func parseAubioBPM(out []byte) (float64, error) {
	for _, field := range bytes.Fields(out) {
		text := strings.TrimSpace(string(field))
		if bpm, err := strconv.ParseFloat(text, 64); err == nil && bpm > 0 {
			return bpm, nil
		}
	}
	return 0, fmt.Errorf("bpm not found in aubio output")
}
