package bpm

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
)

type Essentia struct{}

func (Essentia) Detect(path string) (float64, error) {
	cmd := exec.Command("essentia_streaming_extractor_music", path, "-")
	out, err := cmd.Output()
	if err != nil {
		return 0, err
	}
	return parseEssentiaBPM(out)
}

func parseEssentiaBPM(out []byte) (float64, error) {
	var doc map[string]any
	if err := json.Unmarshal(out, &doc); err == nil {
		for _, key := range []string{"rhythm.bpm", "bpm"} {
			if bpm, ok := lookupFloat(doc, key); ok {
				return bpm, nil
			}
		}
	}
	lines := bytes.Split(out, []byte{'\n'})
	for _, line := range lines {
		text := strings.TrimSpace(string(line))
		if strings.Contains(strings.ToLower(text), "bpm") {
			fields := strings.FieldsFunc(text, func(r rune) bool {
				return r == ':' || r == '=' || r == ',' || r == ' '
			})
			for i := len(fields) - 1; i >= 0; i-- {
				if bpm, err := strconv.ParseFloat(fields[i], 64); err == nil {
					return bpm, nil
				}
			}
		}
	}
	return 0, fmt.Errorf("bpm not found in essentia output")
}

func lookupFloat(doc map[string]any, dotted string) (float64, bool) {
	cur := any(doc)
	for _, part := range strings.Split(dotted, ".") {
		m, ok := cur.(map[string]any)
		if !ok {
			return 0, false
		}
		cur = m[part]
	}
	switch v := cur.(type) {
	case float64:
		return v, true
	case json.Number:
		f, err := v.Float64()
		return f, err == nil
	default:
		return 0, false
	}
}
