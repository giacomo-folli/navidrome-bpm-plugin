package main

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
)

// readTmpoM4A reads the iTunes BPM atom (moov/udta/meta/ilst/tmpo/data) from
// an MP4 container. dhowden/tag misparses the 2-byte integer payload ffmpeg
// writes, so we walk the boxes ourselves.
func readTmpoM4A(path string) (int, bool, error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, false, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return 0, false, err
	}
	return findTmpo(f, 0, info.Size(), []string{"moov", "udta", "meta", "ilst", "tmpo", "data"})
}

func findTmpo(r io.ReaderAt, start, end int64, boxPath []string) (int, bool, error) {
	if len(boxPath) == 0 {
		// We're inside the data box: 4 bytes type flags + 4 bytes locale,
		// then a big-endian integer whose width varies by writer (2-4 bytes).
		payloadLen := end - start - 8
		if payloadLen < 1 || payloadLen > 8 {
			return 0, false, fmt.Errorf("unexpected tmpo payload length %d", payloadLen)
		}
		buf := make([]byte, payloadLen)
		if _, err := r.ReadAt(buf, start+8); err != nil {
			return 0, false, err
		}
		var v int
		for _, b := range buf {
			v = v<<8 | int(b)
		}
		return v, true, nil
	}

	pos := start
	for pos+8 <= end {
		var hdr [8]byte
		if _, err := r.ReadAt(hdr[:], pos); err != nil {
			return 0, false, err
		}
		size := int64(binary.BigEndian.Uint32(hdr[:4]))
		boxType := string(hdr[4:8])
		headerLen := int64(8)
		switch size {
		case 0: // box extends to end of enclosing space
			size = end - pos
		case 1: // 64-bit largesize follows
			var large [8]byte
			if _, err := r.ReadAt(large[:], pos+8); err != nil {
				return 0, false, err
			}
			size = int64(binary.BigEndian.Uint64(large[:]))
			headerLen = 16
		}
		if size < headerLen || pos+size > end {
			return 0, false, errors.New("malformed box structure")
		}
		if boxType == boxPath[0] {
			inner := pos + headerLen
			if boxType == "meta" {
				inner += 4 // meta is a full box: skip version/flags
			}
			return findTmpo(r, inner, pos+size, boxPath[1:])
		}
		pos += size
	}
	return 0, false, nil
}
