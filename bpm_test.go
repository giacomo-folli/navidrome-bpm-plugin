package main

import (
	"encoding/binary"
	"math"
	"testing"
)

// clickTrack generates mono PCM of decaying pulses at the given tempo.
func clickTrack(sampleRate int, tempo float64, seconds float64) []float64 {
	total := int(float64(sampleRate) * seconds)
	samples := make([]float64, total)
	beatPeriod := int(float64(sampleRate) * 60 / tempo)
	for beat := 0; beat*beatPeriod < total; beat++ {
		start := beat * beatPeriod
		for i := 0; i < 2000 && start+i < total; i++ {
			samples[start+i] = 20000 * math.Exp(-float64(i)/400)
		}
	}
	return samples
}

func detectClickTrack(t *testing.T, sampleRate int, tempo float64) float64 {
	t.Helper()
	acc := &energyAccumulator{}
	for _, s := range clickTrack(sampleRate, tempo, 60) {
		acc.addMono(s)
	}
	detected, err := detectTempoFromEnergy(acc.nrg, sampleRate)
	if err != nil {
		t.Fatalf("detectTempoFromEnergy failed: %v", err)
	}
	return detected
}

func TestDetectTempo44100(t *testing.T) {
	detected := detectClickTrack(t, 44100, 120)
	if math.Abs(detected-120) > 3 {
		t.Errorf("expected ~120 BPM, got %.2f", detected)
	}
}

func TestDetectTempo48000(t *testing.T) {
	detected := detectClickTrack(t, 48000, 120)
	if math.Abs(detected-120) > 3 {
		t.Errorf("expected ~120 BPM at 48kHz, got %.2f", detected)
	}
}

func TestDetectTempoTooShort(t *testing.T) {
	acc := &energyAccumulator{}
	for _, s := range clickTrack(44100, 120, 1) {
		acc.addMono(s)
	}
	if _, err := detectTempoFromEnergy(acc.nrg, 44100); err == nil {
		t.Error("expected error for too-short audio")
	}
}

// TestFeedStereoS16LE checks that chunked stereo byte feeding (with partial
// frames carried across chunk boundaries) matches direct mono feeding.
func TestFeedStereoS16LE(t *testing.T) {
	mono := clickTrack(44100, 120, 12)

	want := &energyAccumulator{}
	for _, s := range mono {
		want.addMono(float64(int16(s))) // quantized like the encoded bytes
	}

	pcm := make([]byte, len(mono)*4)
	for i, s := range mono {
		binary.LittleEndian.PutUint16(pcm[i*4:], uint16(int16(s)))
		binary.LittleEndian.PutUint16(pcm[i*4+2:], uint16(int16(s)))
	}

	got := &energyAccumulator{}
	buf := make([]byte, 0, 4096)
	// Odd chunk size to force partial frames at every boundary.
	const chunk = 4093
	for off := 0; off < len(pcm); off += chunk {
		end := off + chunk
		if end > len(pcm) {
			end = len(pcm)
		}
		buf = append(buf, pcm[off:end]...)
		used := got.feedStereoS16LE(buf)
		buf = append(buf[:0], buf[used:]...)
	}
	if len(buf) != 0 {
		t.Errorf("expected no leftover bytes, got %d", len(buf))
	}

	if len(got.nrg) != len(want.nrg) {
		t.Fatalf("energy length mismatch: got %d, want %d", len(got.nrg), len(want.nrg))
	}
	for i := range got.nrg {
		if math.Abs(float64(got.nrg[i]-want.nrg[i])) > 1e-3 {
			t.Fatalf("energy[%d] mismatch: got %f, want %f", i, got.nrg[i], want.nrg[i])
		}
	}
}
