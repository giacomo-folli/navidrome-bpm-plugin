package main

import (
	"encoding/binary"
	"fmt"
	"io"
	"math"
	"os"

	"github.com/benjojo/bpm"
	mp3 "github.com/hajimehoshi/go-mp3"
)

const (
	// energyInterval mirrors benjojo/bpm's INTERVAL: one energy sample is
	// emitted per this many mono PCM samples.
	energyInterval = 128
	// referenceRate mirrors benjojo/bpm's RATE constant; detected values must
	// be rescaled when the source sample rate differs.
	referenceRate = 44100.0

	minBPM      = 60.0
	maxBPM      = 200.0
	scanSteps   = 2048 // bpm-tools defaults
	scanSamples = 1024

	minAudioSeconds = 10.0
	// maxAudioSeconds caps how much audio is decoded per song: enough for a
	// stable tempo estimate while keeping each analysis well inside
	// Navidrome's 30s plugin-call timeout (Wasm decoding is roughly an order
	// of magnitude slower than native).
	maxAudioSeconds = 30.0
)

func detectBPM(filePath string) (float64, error) {
	file, err := os.Open(filePath)
	if err != nil {
		return 0, fmt.Errorf("failed to open file: %w", err)
	}
	defer file.Close()

	return detectBPMFromMP3(file)
}

func detectBPMFromMP3(r io.Reader) (float64, error) {
	decoder, err := mp3.NewDecoder(r)
	if err != nil {
		return 0, fmt.Errorf("failed to decode mp3: %w", err)
	}

	sampleRate := int(decoder.SampleRate())
	maxEnergySamples := int(maxAudioSeconds * float64(sampleRate) / energyInterval)

	acc := &energyAccumulator{}
	// go-mp3 always outputs 16-bit little-endian stereo at the source rate.
	// Stream it through the energy accumulator so we never hold the full PCM
	// data in memory (the Wasm sandbox has a limited heap).
	buf := make([]byte, 32*1024)
	rem := 0
	for len(acc.nrg) < maxEnergySamples {
		n, err := decoder.Read(buf[rem:])
		total := rem + n
		used := acc.feedStereoS16LE(buf[:total])
		rem = copy(buf, buf[used:total])
		if err == io.EOF {
			break
		}
		if err != nil {
			return 0, fmt.Errorf("error reading mp3 data: %w", err)
		}
	}

	return detectTempoFromEnergy(acc.nrg, sampleRate)
}

// detectTempoFromEnergy scans an energy envelope (as produced by
// energyAccumulator from PCM at the given sample rate) for its tempo.
func detectTempoFromEnergy(nrg []float32, sampleRate int) (float64, error) {
	minEnergySamples := int(minAudioSeconds * float64(sampleRate) / energyInterval)
	if len(nrg) < minEnergySamples {
		return 0, fmt.Errorf("not enough audio to analyze (%d energy samples)", len(nrg))
	}

	detected := bpm.ScanForBpm(nrg, minBPM, maxBPM, scanSteps, scanSamples)
	// benjojo/bpm assumes 44100Hz input; rescale for the actual rate.
	detected *= float64(sampleRate) / referenceRate

	if math.IsNaN(detected) || detected < minBPM || detected > maxBPM {
		return 0, fmt.Errorf("no plausible tempo found (got %.1f)", detected)
	}
	return detected, nil
}

// energyAccumulator folds PCM samples into benjojo/bpm's energy envelope
// (the same peak-follower as its ReadFloatArray, but incremental).
type energyAccumulator struct {
	v   float64
	n   int
	nrg []float32
}

func (a *energyAccumulator) addMono(sample float64) {
	z := math.Abs(sample)
	if z > a.v {
		a.v += (z - a.v) / 8
	} else {
		a.v -= (a.v - z) / 512
	}
	a.n++
	if a.n == energyInterval {
		a.n = 0
		a.nrg = append(a.nrg, float32(a.v))
	}
}

// feedStereoS16LE consumes complete 4-byte L/R frames from buf, downmixing to
// mono, and returns the number of bytes consumed. Leftover bytes of a partial
// frame must be carried over into the next call.
func (a *energyAccumulator) feedStereoS16LE(buf []byte) int {
	consumed := len(buf) - len(buf)%4
	for i := 0; i < consumed; i += 4 {
		l := int16(binary.LittleEndian.Uint16(buf[i:]))
		r := int16(binary.LittleEndian.Uint16(buf[i+2:]))
		a.addMono((float64(l) + float64(r)) / 2)
	}
	return consumed
}
