package bpm

import (
	"math"
	"testing"
)

func TestParseAubioBPM(t *testing.T) {
	// 120 BPM = 0.5s between beats
	out := []byte("0.500\n1.000\n1.500\n2.000\n2.500\n3.000\n")
	bpm, err := parseAubioBPM(out)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if math.Abs(bpm-120) > 0.5 {
		t.Fatalf("parseAubioBPM() = %v, want ~120", bpm)
	}

	// 140 BPM ≈ 0.4286s between beats
	out140 := []byte("0.000\n0.4286\n0.8571\n1.2857\n1.7143\n2.1429\n")
	bpm140, err := parseAubioBPM(out140)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if math.Abs(bpm140-140) > 1 {
		t.Fatalf("parseAubioBPM() = %v, want ~140", bpm140)
	}
}

func TestParseAubioBPM_NotEnoughBeats(t *testing.T) {
	_, err := parseAubioBPM([]byte("0.500\n"))
	if err == nil {
		t.Fatal("expected error for single beat")
	}
}

func TestParseAubioBPM_Empty(t *testing.T) {
	_, err := parseAubioBPM([]byte(""))
	if err == nil {
		t.Fatal("expected error for empty output")
	}
}
