package main

import (
	"bytes"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/bogem/id3v2/v2"
	"github.com/dhowden/tag"
	"github.com/go-flac/flacvorbis/v2"
	flac "github.com/go-flac/go-flac/v2"
)

// tmpSuffix marks in-progress tag rewrites; the scanner and watcher ignore it.
const tmpSuffix = ".bpmtmp"

// hasBPMTag reports whether the file already carries a BPM tag and its value.
// A file dhowden/tag cannot parse is treated as untagged: aubio/ffmpeg may
// still handle it, so the caller proceeds.
func hasBPMTag(path string) (bool, string) {
	// MP4 containers need a custom reader: dhowden/tag misparses the tmpo
	// integer atom (it reads 0 for ffmpeg-written values).
	if isMP4(path) {
		v, found, err := readTmpoM4A(path)
		if err != nil {
			slog.Debug("could not read mp4 atoms, treating as untagged", "path", path, "err", err)
			return false, ""
		}
		if found && v > 0 {
			return true, strconv.Itoa(v)
		}
		return false, ""
	}

	f, err := os.Open(path)
	if err != nil {
		return false, ""
	}
	defer f.Close()

	meta, err := tag.ReadFrom(f)
	if err != nil {
		slog.Debug("could not read tags, treating as untagged", "path", path, "err", err)
		return false, ""
	}
	for key, val := range meta.Raw() {
		switch strings.ToUpper(strings.TrimSpace(key)) {
		case "TBPM", "BPM", "TMPO":
			s := strings.TrimSpace(fmt.Sprint(val))
			if s != "" && s != "0" {
				return true, s
			}
		}
	}
	return false, ""
}

// writeBPM writes the BPM tag in place, dispatching on file extension.
func writeBPM(path string, bpm float64, dryRun bool) error {
	ext := strings.ToLower(filepath.Ext(path))
	value := strconv.Itoa(int(bpm + 0.5))

	if dryRun {
		slog.Info("dry-run: would write BPM", "path", path, "bpm", value, "format", ext)
		return nil
	}

	switch ext {
	case ".mp3":
		return writeBPMID3(path, value)
	case ".flac":
		return writeBPMFLAC(path, value)
	default:
		return writeBPMFFmpeg(path, value)
	}
}

func writeBPMID3(path, value string) error {
	t, err := id3v2.Open(path, id3v2.Options{Parse: true})
	if err != nil {
		return fmt.Errorf("id3v2 open: %w", err)
	}
	defer t.Close()
	t.AddTextFrame(t.CommonID("BPM"), t.DefaultEncoding(), value)
	if err := t.Save(); err != nil {
		return fmt.Errorf("id3v2 save: %w", err)
	}
	return nil
}

func writeBPMFLAC(path, value string) error {
	f, err := flac.ParseFile(path)
	if err != nil {
		return fmt.Errorf("flac parse: %w", err)
	}

	var cmt *flacvorbis.MetaDataBlockVorbisComment
	cmtIdx := -1
	for i, block := range f.Meta {
		if block.Type == flac.VorbisComment {
			cmt, err = flacvorbis.ParseFromMetaDataBlock(*block)
			if err != nil {
				return fmt.Errorf("flac vorbis comment parse: %w", err)
			}
			cmtIdx = i
			break
		}
	}
	if cmt == nil {
		cmt = flacvorbis.New()
	}

	// Replace any existing BPM comments rather than appending duplicates.
	kept := cmt.Comments[:0]
	for _, c := range cmt.Comments {
		if !strings.HasPrefix(strings.ToUpper(c), "BPM=") {
			kept = append(kept, c)
		}
	}
	cmt.Comments = kept
	if err := cmt.Add("BPM", value); err != nil {
		return fmt.Errorf("flac vorbis comment add: %w", err)
	}

	block := cmt.Marshal()
	if cmtIdx >= 0 {
		f.Meta[cmtIdx] = &block
	} else {
		f.Meta = append(f.Meta, &block)
	}

	// go-flac rewrites the whole file; do it via a sibling temp + rename so a
	// crash mid-write never corrupts the original.
	tmp := path + tmpSuffix + ".flac"
	if err := f.Save(tmp); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("flac save: %w", err)
	}
	if err := preservePermissions(path, tmp); err != nil {
		slog.Debug("could not preserve permissions", "path", path, "err", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("flac rename: %w", err)
	}
	return nil
}

// writeBPMFFmpeg remuxes the file with a BPM metadata entry (stream copy, no
// re-encoding). Used for m4a/ogg/opus where no maintained Go writer exists.
func writeBPMFFmpeg(path, value string) error {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		return fmt.Errorf("ffmpeg not available, cannot tag %s files", filepath.Ext(path))
	}
	tmp := path + tmpSuffix + filepath.Ext(path)
	args := []string{"-v", "error", "-y", "-i", path, "-map", "0", "-c", "copy"}
	if isMP4(path) {
		// The mov muxer maps this to the canonical iTunes tmpo atom. Do NOT
		// use -movflags use_metadata_tags: it switches to the mdta scheme,
		// which most tag readers (TagLib, mutagen, dhowden) cannot read.
		args = append(args, "-metadata", "tmpo="+value)
	} else {
		args = append(args, "-metadata", "BPM="+value)
	}
	args = append(args, tmp)

	cmd := exec.Command("ffmpeg", args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("ffmpeg remux: %w: %s", err, firstLine(stderr.String()))
	}
	if err := preservePermissions(path, tmp); err != nil {
		slog.Debug("could not preserve permissions", "path", path, "err", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("remux rename: %w", err)
	}
	return nil
}

func preservePermissions(orig, tmp string) error {
	info, err := os.Stat(orig)
	if err != nil {
		return err
	}
	return os.Chmod(tmp, info.Mode())
}

func isMP4(path string) bool {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".m4a", ".m4b", ".mp4", ".aac":
		return true
	}
	return false
}

// isTempFile reports whether the path is one of our in-progress rewrites.
func isTempFile(path string) bool {
	return strings.Contains(filepath.Base(path), tmpSuffix)
}
