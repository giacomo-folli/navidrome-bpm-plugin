package main

import (
	"context"
	"math"
	"os/exec"
	"testing"
)

func regularBeats(n int, interval float64) []float64 {
	beats := make([]float64, n)
	for i := range beats {
		beats[i] = float64(i) * interval
	}
	return beats
}

func TestBPMFromBeatsRegular(t *testing.T) {
	bpm, err := bpmFromBeats(regularBeats(20, 0.5))
	if err != nil {
		t.Fatal(err)
	}
	if bpm != 120 {
		t.Fatalf("got %v, want 120", bpm)
	}
}

func TestBPMFromBeatsDroppedBeat(t *testing.T) {
	// One missing beat produces a single 1.0s gap; the median must ignore it.
	beats := append(regularBeats(10, 0.5), 5.5, 6.0, 6.5, 7.0, 7.5, 8.0)
	bpm, err := bpmFromBeats(beats)
	if err != nil {
		t.Fatal(err)
	}
	if bpm != 120 {
		t.Fatalf("got %v, want 120", bpm)
	}
}

func TestBPMFromBeatsTooFew(t *testing.T) {
	if _, err := bpmFromBeats(regularBeats(5, 0.5)); err == nil {
		t.Fatal("want error for too few beats")
	}
}

func TestBPMFromBeatsImplausible(t *testing.T) {
	if _, err := bpmFromBeats(regularBeats(20, 0.1)); err == nil {
		t.Fatal("want error for 600 BPM")
	}
}

func TestBPMFromDirect(t *testing.T) {
	// The unified `aubio tempo` CLI prints a single "142.42 bpm" line.
	bpm, err := bpmFromDirect(parseBeatLines("142.424242 bpm\n"))
	if err != nil {
		t.Fatal(err)
	}
	if bpm != 142 {
		t.Fatalf("got %v, want 142", bpm)
	}
	if _, err := bpmFromDirect(nil); err == nil {
		t.Fatal("want error for empty output")
	}
	if _, err := bpmFromDirect([]float64{999}); err == nil {
		t.Fatal("want error for implausible tempo")
	}
}

func TestParseBeatLines(t *testing.T) {
	out := "0.487528\n0.998866\nsome warning line\n1.486077\n\n2.001678 extra\n"
	beats := parseBeatLines(out)
	want := []float64{0.487528, 0.998866, 1.486077, 2.001678}
	if len(beats) != len(want) {
		t.Fatalf("got %v, want %v", beats, want)
	}
	for i := range want {
		if math.Abs(beats[i]-want[i]) > 1e-9 {
			t.Fatalf("beat %d: got %v, want %v", i, beats[i], want[i])
		}
	}
}

// TestDetectBPMClickTrack is an integration test: it needs aubio and ffmpeg.
func TestDetectBPMClickTrack(t *testing.T) {
	if tempoCommand() == nil {
		t.Skip("aubio/aubiotrack not installed")
	}
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg not installed")
	}
	path := t.TempDir() + "/click120.wav"
	// 120 BPM click track: a short 1 kHz burst every 0.5 s for 30 s.
	out, err := exec.Command("ffmpeg", "-v", "error", "-y",
		"-f", "lavfi", "-i", "aevalsrc=if(lt(mod(t\\,0.5)\\,0.05)\\,sin(2*PI*1000*t)\\,0):d=30",
		"-ar", "44100", path).CombinedOutput()
	if err != nil {
		t.Fatalf("ffmpeg click track: %v: %s", err, out)
	}
	bpm, err := detectBPM(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	// Bare click tracks are octave-ambiguous: aubio may lock onto every
	// second click and report 60. Either answer proves the exec/parse/median
	// pipeline works, which is what this test is for.
	if math.Abs(bpm-120) > 3 && math.Abs(bpm-60) > 3 {
		t.Fatalf("got %v BPM, want 120±3 or 60±3", bpm)
	}
}
