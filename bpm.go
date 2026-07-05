package main

import (
	"bufio"
	"encoding/binary"
	"fmt"
	"io"
	"math"
	"os"
	"time"

	"github.com/benjojo/bpm"
	mp3 "github.com/hajimehoshi/go-mp3"
	"github.com/navidrome/navidrome/plugins/pdk/go/pdk"
)

const (
	// energyInterval mirrors benjojo/bpm's INTERVAL: one energy sample is
	// emitted per this many mono PCM samples.
	energyInterval = 128
	// referenceRate mirrors benjojo/bpm's RATE constant; detected values must
	// be rescaled when the source sample rate differs.
	referenceRate = 44100.0

	minBPM = 60.0
	maxBPM = 200.0
	// Halved from the bpm-tools defaults (2048/1024): with only
	// maxAudioSeconds of audio the scan stays accurate at a quarter of the
	// cost, which matters under the Wasm slowdown.
	scanSteps   = 1024
	scanSamples = 512

	minAudioSeconds = 3.0
	// maxAudioSeconds caps how much audio is decoded per song. Decoding
	// dominates analysis cost (~3x the tempo scan natively) and Wasm on a
	// weak host CPU can be more than an order of magnitude slower than that,
	// so this is kept small enough that a song decodes in a few seconds even
	// in the worst observed environments; the tempo estimate is less stable
	// than with 15s of audio, but a batch must fit well inside Navidrome's
	// 30s plugin-call timeout.
	maxAudioSeconds = 5.0

	// analysisSoftDeadline aborts a song's decode before Navidrome's 30s hard
	// kill, so the song is recorded as failed (with timing details) and the
	// scan keeps going instead of the whole module being torn down. It must
	// stay under 30s minus batchTimeBudget (see main.go) with room for the
	// Subsonic calls around the analysis.
	analysisSoftDeadline = 10 * time.Second
)

func detectBPM(filePath string) (float64, error) {
	file, err := os.Open(filePath)
	if err != nil {
		return 0, fmt.Errorf("failed to open file: %w", err)
	}
	defer file.Close()

	// Hide the file's io.Seeker: go-mp3 otherwise pre-scans every frame of the
	// whole file with tiny unbuffered reads to build a seek table, which in the
	// Wasm sandbox costs one hosted WASI call each and can alone exceed
	// Navidrome's 30s plugin-call deadline.
	return detectBPMFromMP3(bufio.NewReaderSize(file, 64*1024))
}

func detectBPMFromMP3(r io.Reader) (float64, error) {
	initStart := wallNow()
	decoder, err := mp3.NewDecoder(r)
	if err != nil {
		return 0, fmt.Errorf("failed to decode mp3: %w", err)
	}
	initElapsed := wallNow().Sub(initStart)

	sampleRate := int(decoder.SampleRate())
	maxEnergySamples := int(maxAudioSeconds * float64(sampleRate) / energyInterval)

	acc := &energyAccumulator{}
	// go-mp3 always outputs 16-bit little-endian stereo at the source rate.
	// Stream it through the energy accumulator so we never hold the full PCM
	// data in memory (the Wasm sandbox has a limited heap).
	decodeStart := wallNow()
	deadline := decodeStart.Add(analysisSoftDeadline)
	// Keep the buffer small (~2 MP3 frames of PCM) so each Read decodes only
	// a sliver of audio and the deadline check above it runs often; Wasm has
	// no preemption, so a large Read could blow past the deadline unchecked.
	buf := make([]byte, 8*1024)
	rem := 0
	minEnergySamples := int(minAudioSeconds * float64(sampleRate) / energyInterval)
	for len(acc.nrg) < maxEnergySamples {
		if wallNow().After(deadline) {
			// A slow host may not decode maxAudioSeconds in time; analyze
			// whatever is buffered rather than failing, as long as it clears
			// the minimum the tempo scan needs.
			if len(acc.nrg) >= minEnergySamples {
				break
			}
			return 0, fmt.Errorf("decode too slow: %.1fs of audio in %s (decoder init took %s)",
				audioSeconds(len(acc.nrg), sampleRate), wallNow().Sub(decodeStart).Round(100*time.Millisecond), initElapsed.Round(time.Millisecond))
		}
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
	decodeElapsed := wallNow().Sub(decodeStart)

	scanStart := wallNow()
	tempo, err := detectTempoFromEnergy(acc.nrg, sampleRate)
	pdk.Log(pdk.LogDebug, fmt.Sprintf("bpm timing: init=%s decode=%s (%.1fs audio) scan=%s",
		initElapsed.Round(time.Millisecond), decodeElapsed.Round(time.Millisecond),
		audioSeconds(len(acc.nrg), sampleRate), wallNow().Sub(scanStart).Round(time.Millisecond)))
	return tempo, err
}

// audioSeconds converts an energy sample count back to seconds of audio.
func audioSeconds(energySamples, sampleRate int) float64 {
	return float64(energySamples) * energyInterval / float64(sampleRate)
}

// detectTempoFromEnergy scans an energy envelope (as produced by
// energyAccumulator from PCM at the given sample rate) for its tempo.
func detectTempoFromEnergy(nrg []float32, sampleRate int) (float64, error) {
	minEnergySamples := int(minAudioSeconds * float64(sampleRate) / energyInterval)
	if len(nrg) < minEnergySamples {
		return 0, fmt.Errorf("not enough audio to analyze (%d energy samples)", len(nrg))
	}

	// benjojo/bpm assumes 44100Hz input, so the scan bounds must be expressed
	// in that frame and the result scaled back; passing real-world bounds
	// shifts the effective range with the file's sample rate (65-218 BPM for
	// 48kHz files) and puts part of it beyond maxBPM.
	ratio := float64(sampleRate) / referenceRate
	detected := bpm.ScanForBpm(nrg, minBPM/ratio, maxBPM/ratio, scanSteps, scanSamples) * ratio

	if math.IsNaN(detected) || detected < minBPM || detected > maxBPM {
		return 0, fmt.Errorf("no plausible tempo found (got %.1f)", detected)
	}
	// The scan returns the autodifference trough even when the envelope has no
	// periodicity at all (e.g. only a beatless intro was decoded), and such
	// degenerate troughs sit at the edge of the scanned range. A result within
	// 2% of a bound is overwhelmingly more likely to be that artifact than a
	// song at exactly the range limit.
	if detected <= minBPM*1.02 || detected >= maxBPM*0.98 {
		return 0, fmt.Errorf("tempo scan pinned at range edge (%.1f), audio likely has no steady beat", detected)
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
