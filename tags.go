package main

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"io"
	"log/slog"
	"net/http"
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
		// dhowden/tag aborts the whole parse on one malformed frame (e.g. the
		// odd-length UTF-16 text frames some downloaders write), hiding a valid
		// TBPM and causing endless re-analysis. Fall back to bogem/id3v2, which
		// decodes only the frame we ask for.
		if strings.ToLower(filepath.Ext(path)) == ".mp3" {
			if has, v := readTBPMID3(path); has {
				return true, v
			}
		}
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

// readTBPMID3 reads the TBPM frame with bogem/id3v2, ignoring all other
// frames so malformed ones elsewhere in the tag cannot break the read.
func readTBPMID3(path string) (bool, string) {
	t, err := id3v2.Open(path, id3v2.Options{Parse: true, ParseFrames: []string{"BPM"}})
	if err != nil {
		return false, ""
	}
	defer t.Close()
	s := strings.TrimSpace(t.GetTextFrame(t.CommonID("BPM")).Text)
	if s == "" || s == "0" {
		return false, ""
	}
	return true, s
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
	// bogem/id3v2 parses the whole tag and rewrites it from what it parsed, so
	// anything it silently skips is destroyed on Save. Route risky tags
	// through the ffmpeg remux instead, which handles them correctly.
	if reason := id3RewriteRisk(path); reason != "" {
		slog.Warn("mp3 tag unsafe for in-place rewrite, remuxing with ffmpeg", "path", path, "reason", reason)
		return writeBPMFFmpeg(path, value)
	}
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

// id3RewriteRisk reports why an in-place bogem/id3v2 rewrite could silently
// drop or corrupt frames; empty string means the tag is safe. The library
// ignores the tag header flags byte (unsynchronisation, extended header) and
// all per-frame flags, and it stops parsing at the first malformed frame
// header — Save() then writes back only the frames parsed so far. APIC
// usually sits last in the tag, so embedded cover art is the typical
// casualty.
func id3RewriteRisk(path string) string {
	f, err := os.Open(path)
	if err != nil {
		return "" // let the writer surface the real error
	}
	defer f.Close()

	hdr := make([]byte, 10)
	if _, err := io.ReadFull(f, hdr); err != nil || string(hdr[:3]) != "ID3" {
		return "" // no v2 tag: bogem writes a fresh one, nothing to lose
	}
	version := hdr[3]
	if version < 3 {
		return "id3v2.2 tag"
	}
	if hdr[5] != 0 {
		return fmt.Sprintf("tag header flags 0x%02x", hdr[5])
	}
	size := int(hdr[6]&0x7F)<<21 | int(hdr[7]&0x7F)<<14 | int(hdr[8]&0x7F)<<7 | int(hdr[9]&0x7F)
	area := make([]byte, size)
	if _, err := io.ReadFull(f, area); err != nil {
		return "tag shorter than declared size"
	}

	pos := 0
	for pos < len(area) {
		if area[pos] == 0 {
			// Padding starts here; anything non-zero after it is a frame the
			// parser would never reach.
			for _, b := range area[pos:] {
				if b != 0 {
					return "data after blank frame header"
				}
			}
			return ""
		}
		if pos+10 > len(area) {
			return "truncated frame header"
		}
		fh := area[pos : pos+10]
		for _, c := range fh[:4] {
			if (c < 'A' || c > 'Z') && (c < '0' || c > '9') {
				return fmt.Sprintf("invalid frame id %q", fh[:4])
			}
		}
		var fsize int
		if version == 4 {
			if (fh[4]|fh[5]|fh[6]|fh[7])&0x80 != 0 {
				return "non-syncsafe frame size in v2.4 tag"
			}
			fsize = int(fh[4])<<21 | int(fh[5])<<14 | int(fh[6])<<7 | int(fh[7])
		} else {
			fsize = int(fh[4])<<24 | int(fh[5])<<16 | int(fh[6])<<8 | int(fh[7])
		}
		if fh[8] != 0 || fh[9] != 0 {
			// Compression, encryption, grouping, per-frame unsync, data length
			// indicator: bogem reads the body raw and writes the flags back as
			// zero, corrupting the frame.
			return fmt.Sprintf("frame %s has flags %02x%02x", fh[:4], fh[8], fh[9])
		}
		pos += 10 + fsize
		if pos > len(area) {
			return fmt.Sprintf("frame %s overflows tag area", fh[:4])
		}
	}
	return ""
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
// re-encoding). Used for m4a/ogg/opus where no maintained Go writer exists,
// and as the fallback for mp3 tags bogem/id3v2 cannot safely rewrite.
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
	} else if strings.ToLower(filepath.Ext(path)) == ".mp3" {
		// v2.3 for compatibility with older readers; the mp3 muxer writes a
		// 4-char uppercase key like TBPM verbatim as that frame.
		args = append(args, "-id3v2_version", "3", "-metadata", "TBPM="+value)
	} else {
		args = append(args, "-metadata", "BPM="+value)
	}
	args = append(args, tmp)

	err := runFFmpeg(args)
	if err != nil && isOgg(path) {
		// The ogg muxer cannot carry cover art as a stream: the demuxer turns
		// a METADATA_BLOCK_PICTURE comment (how yt-dlp embeds thumbnails) into
		// an mjpeg/png video stream it then refuses to write back
		// ("Unsupported codec id in stream 1"). Remux the audio alone and
		// restore the art as a vorbis comment.
		err = remuxOggAudioOnly(path, tmp, value)
	}
	if err != nil {
		os.Remove(tmp)
		return fmt.Errorf("ffmpeg remux: %w", err)
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

func isOgg(path string) bool {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".ogg", ".oga", ".opus":
		return true
	}
	return false
}

// remuxOggAudioOnly rewrites path to tmp keeping only the audio stream. The
// existing tags are round-tripped through an ffmetadata file (avoids argv
// size limits for large covers), with BPM added and any embedded cover art
// re-encoded as a METADATA_BLOCK_PICTURE comment.
func remuxOggAudioOnly(path, tmp, value string) error {
	metaFile := tmp + ".ffmeta"
	defer os.Remove(metaFile)
	if err := runFFmpeg([]string{"-v", "error", "-y", "-i", path, "-f", "ffmetadata", metaFile}); err != nil {
		return fmt.Errorf("metadata export: %w", err)
	}

	extra := "BPM=" + value + "\n"
	if pic, err := extractCover(path); err != nil {
		slog.Warn("dropping embedded cover art", "path", path, "err", err)
	} else {
		extra += "METADATA_BLOCK_PICTURE=" + escapeFFMeta(vorbisPicture(pic)) + "\n"
	}
	f, err := os.OpenFile(metaFile, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	_, werr := f.WriteString(extra)
	if cerr := f.Close(); werr == nil {
		werr = cerr
	}
	if werr != nil {
		return fmt.Errorf("metadata append: %w", werr)
	}

	return runFFmpeg([]string{"-v", "error", "-y", "-i", path,
		"-f", "ffmetadata", "-i", metaFile, "-map_metadata", "1",
		"-map", "0:a", "-c", "copy", tmp})
}

// extractCover copies the embedded picture stream out of the container.
func extractCover(path string) ([]byte, error) {
	cmd := exec.Command("ffmpeg", "-v", "error", "-i", path,
		"-map", "0:v:0", "-c", "copy", "-f", "image2pipe", "-")
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("%w: %s", err, firstLine(stderr.String()))
	}
	if stdout.Len() == 0 {
		return nil, fmt.Errorf("empty picture stream")
	}
	return stdout.Bytes(), nil
}

// vorbisPicture encodes image data as a base64 FLAC picture block (type 3,
// front cover), the standard METADATA_BLOCK_PICTURE payload for ogg/opus.
// Width/height/depth are left 0 (unknown), which readers accept.
func vorbisPicture(data []byte) string {
	mime := http.DetectContentType(data)
	var b bytes.Buffer
	be32 := func(v uint32) { binary.Write(&b, binary.BigEndian, v) }
	be32(3)
	be32(uint32(len(mime)))
	b.WriteString(mime)
	be32(0) // description length
	be32(0) // width
	be32(0) // height
	be32(0) // depth
	be32(0) // palette colors
	be32(uint32(len(data)))
	b.Write(data)
	return base64.StdEncoding.EncodeToString(b.Bytes())
}

// escapeFFMeta escapes the characters the ffmetadata format treats specially.
func escapeFFMeta(s string) string {
	return strings.NewReplacer(
		`\`, `\\`, "=", `\=`, ";", `\;`, "#", `\#`, "\n", "\\\n",
	).Replace(s)
}

func runFFmpeg(args []string) error {
	cmd := exec.Command("ffmpeg", args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%w: %s", err, firstLine(stderr.String()))
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
