package bpm

import "fmt"

type Detector interface {
	Detect(path string) (float64, error)
}

func NewDetector(name string) Detector {
	switch name {
	case "aubio":
		return Aubio{}
	case "essentia", "":
		return Essentia{}
	default:
		return fallback{primary: Essentia{}, secondary: Aubio{}}
	}
}

type fallback struct {
	primary   Detector
	secondary Detector
}

func (f fallback) Detect(path string) (float64, error) {
	b, err := f.primary.Detect(path)
	if err == nil {
		return b, nil
	}
	b2, err2 := f.secondary.Detect(path)
	if err2 == nil {
		return b2, nil
	}
	return 0, fmt.Errorf("primary detector: %w; fallback detector: %v", err, err2)
}
