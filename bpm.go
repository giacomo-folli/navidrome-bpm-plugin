package main

import (
	"fmt"
	"io"
	"os"

	"github.com/benjojo/bpm"
	"github.com/hajimehoshi/go-mp3"
)

func detectBPM(filePath string) (float64, error) {
	file, err := os.Open(filePath)
	if err != nil {
		return 0, fmt.Errorf("failed to open file: %w", err)
	}
	defer file.Close()

	decoder, err := mp3.NewDecoder(file)
	if err != nil {
		return 0, fmt.Errorf("failed to decode mp3: %w", err)
	}

	var samples []float32
	buf := make([]byte, 8192)

	for {
		n, err := decoder.Read(buf)
		if n > 0 {
			// go-mp3 outputs 16-bit little-endian stereo by default
			for i := 0; i < n-1; i += 2 {
				// parse little-endian int16
				sample := int16(buf[i]) | (int16(buf[i+1]) << 8)
				samples = append(samples, float32(sample))
			}
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			return 0, fmt.Errorf("error reading mp3 data: %w", err)
		}
	}

	if len(samples) == 0 {
		return 0, fmt.Errorf("no audio samples found")
	}

	// Downmix stereo to mono if needed
	// Actually go-mp3 is always stereo unless the source is mono, but let's assume it's stereo 
	// benjojo/bpm requires a float32 array, probably mono. 
	// We'll downmix by averaging every 2 samples.
	var mono []float32
	for i := 0; i < len(samples)-1; i += 2 {
		mono = append(mono, (samples[i]+samples[i+1])/2)
	}

	nrg := bpm.ReadFloatArray(mono)
	if len(nrg) == 0 {
		return 0, fmt.Errorf("failed to process energy array")
	}

	// The sampling rate of energy array in benjojo/bpm is based on INTERVAL which is hardcoded.
	// We call ScanForBpm with sensible limits.
	// slowest = 60, fastest = 200, steps = 10000, samples = len(nrg)
	detected := bpm.ScanForBpm(nrg, 60.0, 200.0, 10000, len(nrg))

	return detected, nil
}
