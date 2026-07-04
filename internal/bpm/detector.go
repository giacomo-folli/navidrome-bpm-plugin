package bpm

import "os/exec"

type Detector interface {
	Detect(path string) (float64, error)
	Name() string
}

func NewDetector() Detector {
	return Aubio{}
}

func Availability() map[string]bool {
	return map[string]bool{
		"aubiotrack": commandExists("aubiotrack"),
	}
}

func commandExists(name string) bool {
	_, err := exec.LookPath(name)
	return err == nil
}
