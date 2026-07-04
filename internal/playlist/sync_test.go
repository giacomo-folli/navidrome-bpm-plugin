package playlist

import "testing"

func TestBucket(t *testing.T) {
	tests := map[float64]int{
		125: 130,
		128: 130,
		133: 130,
		136: 140,
	}
	for bpm, want := range tests {
		if got := Bucket(bpm, 10); got != want {
			t.Fatalf("Bucket(%v) = %d, want %d", bpm, got, want)
		}
	}
}

func TestName(t *testing.T) {
	if got := Name(60); got != "060bpm" {
		t.Fatalf("Name(60) = %q", got)
	}
}
