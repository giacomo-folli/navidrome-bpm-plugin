package metadata

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

type Writer interface {
	WriteBPM(path string, bpm float64) error
}

func NewWriter(enabled bool) Writer {
	if !enabled {
		return NoopWriter{}
	}
	return FFmpegWriter{}
}

type NoopWriter struct{}

func (NoopWriter) WriteBPM(string, float64) error { return nil }

type FFmpegWriter struct{}

func (FFmpegWriter) WriteBPM(path string, bpm float64) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".bpm-tags-*"+filepath.Ext(path))
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	_ = tmp.Close()
	_ = os.Remove(tmpPath)

	value := fmt.Sprintf("%.0f", bpm)
	cmd := exec.Command("ffmpeg", "-y", "-i", path, "-map", "0", "-c", "copy", "-metadata", "BPM="+value, "-metadata", "TBPM="+value, tmpPath)
	if out, err := cmd.CombinedOutput(); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("ffmpeg tag write failed: %w: %s", err, string(out))
	}
	return os.Rename(tmpPath, path)
}
