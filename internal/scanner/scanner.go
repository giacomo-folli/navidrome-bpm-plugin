package scanner

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"unicode"

	"github.com/giacomo-folli/navidrome-bpm-plugin/internal/api"
)

type API interface {
	Albums(ctx context.Context, offset, size int) ([]api.AlbumSummary, error)
	Album(ctx context.Context, id string) (api.Album, error)
}

type Track struct {
	ID       string
	Title    string
	Path     string
	Modified int64
}

type Scanner struct {
	api               API
	musicDir          string
	navidromeMusicDir string
	logger            *slog.Logger
	pageSize          int
}

func New(api API, musicDir, navidromeMusicDir string, logger *slog.Logger) *Scanner {
	if logger == nil {
		logger = slog.Default()
	}
	return &Scanner{api: api, musicDir: musicDir, navidromeMusicDir: navidromeMusicDir, logger: logger, pageSize: 500}
}

func (s *Scanner) Tracks(ctx context.Context) ([]Track, error) {
	var tracks []Track
	totalSongs := 0
	missingFiles := 0
	for offset := 0; ; offset += s.pageSize {
		albums, err := s.api.Albums(ctx, offset, s.pageSize)
		if err != nil {
			return nil, err
		}
		s.logger.Info("loaded album page", "offset", offset, "albums", len(albums))
		for _, summary := range albums {
			album, err := s.api.Album(ctx, summary.ID)
			if err != nil {
				return nil, err
			}
			for _, song := range album.Songs {
				totalSongs++
				path := resolvePath(s.musicDir, s.navidromeMusicDir, song.Path)
				if path == "" {
					missingFiles++
					s.logger.Warn("skipping song without path", "song_id", song.ID, "title", song.Title)
					continue
				}
				resolvedPath, info, usedFallback, err := statResolvedPath(path, song.Title)
				if err != nil {
					missingFiles++
					s.logger.Warn("skipping missing or inaccessible music file", "song_id", song.ID, "title", song.Title, "navidrome_path", song.Path, "resolved_path", path, "error", err)
					continue
				}
				if usedFallback {
					s.logger.Info("resolved music file using filename fallback", "song_id", song.ID, "title", song.Title, "navidrome_path", song.Path, "resolved_path", resolvedPath)
				}
				tracks = append(tracks, Track{
					ID:       song.ID,
					Title:    song.Title,
					Path:     resolvedPath,
					Modified: info.ModTime().Unix(),
				})
			}
		}
		if len(albums) < s.pageSize {
			break
		}
	}
	if totalSongs > 0 && len(tracks) == 0 {
		return nil, fmt.Errorf("no local music files were found for %d Navidrome songs; set musicDir to the local host library root and navidromeMusicDir to the path Navidrome reports, usually /music", totalSongs)
	}
	s.logger.Info("library scan resolved local files", "navidrome_songs", totalSongs, "local_files", len(tracks), "skipped_missing", missingFiles)
	return tracks, nil
}

func resolvePath(musicDir, navidromeMusicDir, songPath string) string {
	if songPath == "" {
		return ""
	}
	if filepath.IsAbs(songPath) {
		if navidromeMusicDir != "" {
			rel, err := filepath.Rel(filepath.Clean(navidromeMusicDir), filepath.Clean(songPath))
			if err == nil && rel != "." && rel != ".." && !startsWithDotDot(rel) {
				return filepath.Join(musicDir, rel)
			}
		}
		return filepath.Clean(songPath)
	}
	return filepath.Join(musicDir, songPath)
}

func startsWithDotDot(path string) bool {
	return path == ".." || len(path) > 3 && path[:3] == "../"
}

var trackPrefixPattern = regexp.MustCompile(`^\d+(?:-\d+)? - (.+)$`)

func statResolvedPath(path, title string) (string, os.FileInfo, bool, error) {
	// Strategy 1: exact path.
	info, err := os.Stat(path)
	if err == nil {
		return path, info, false, nil
	}

	// Strategy 2: strip track-number prefix (e.g. "01-04 - Song.mp3" → "Song.mp3").
	stripped := stripTrackPrefixPath(path)
	if stripped != path {
		if sinfo, serr := os.Stat(stripped); serr == nil {
			return stripped, sinfo, true, nil
		}
	}

	// Strategy 3: fuzzy match inside the parent directory.
	if match, minfo, ok := dirFuzzyMatch(path, title); ok {
		return match, minfo, true, nil
	}

	return path, nil, false, err
}

func stripTrackPrefixPath(path string) string {
	dir := filepath.Dir(path)
	base := filepath.Base(path)
	matches := trackPrefixPattern.FindStringSubmatch(base)
	if len(matches) != 2 {
		return path
	}
	return filepath.Join(dir, matches[1])
}

// dirFuzzyMatch lists the parent directory and attempts to find the best
// matching file using progressively fuzzier strategies:
//  1. Case-insensitive exact match on the full filename.
//  2. Normalised match (lowercase, punctuation stripped).
//  3. Best Jaccard word-overlap score (using both filename and song title).
func dirFuzzyMatch(path, title string) (string, os.FileInfo, bool) {
	dir := filepath.Dir(path)
	ext := strings.ToLower(filepath.Ext(path))
	wantBase := stripExt(filepath.Base(path))

	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", nil, false
	}

	type candidate struct {
		fullPath string
		base     string // filename without extension
		info     os.FileInfo
	}
	var candidates []candidate
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if !strings.EqualFold(filepath.Ext(name), ext) {
			continue
		}
		fi, ierr := e.Info()
		if ierr != nil {
			continue
		}
		candidates = append(candidates, candidate{
			fullPath: filepath.Join(dir, name),
			base:     stripExt(name),
			info:     fi,
		})
	}
	if len(candidates) == 0 {
		return "", nil, false
	}

	// Pass 1: case-insensitive exact match.
	for _, c := range candidates {
		if strings.EqualFold(c.base, wantBase) {
			return c.fullPath, c.info, true
		}
	}

	// Pass 2: normalised match (ignore punctuation / special characters).
	normWant := normalizeForMatch(wantBase)
	for _, c := range candidates {
		if normalizeForMatch(c.base) == normWant {
			return c.fullPath, c.info, true
		}
	}

	// Pass 3: best fuzzy match by Jaccard word overlap.
	normTitle := normalizeForMatch(title)
	var bestPath string
	var bestInfo os.FileInfo
	var bestScore float64
	for _, c := range candidates {
		normC := normalizeForMatch(c.base)
		score := wordOverlap(normWant, normC)
		if normTitle != "" {
			if ts := wordOverlap(normTitle, normC); ts > score {
				score = ts
			}
		}
		if score > bestScore {
			bestScore = score
			bestPath = c.fullPath
			bestInfo = c.info
		}
	}
	const minScore = 0.5
	if bestScore >= minScore && bestInfo != nil {
		return bestPath, bestInfo, true
	}
	return "", nil, false
}

// --------------- helpers ---------------

func stripExt(name string) string {
	return strings.TrimSuffix(name, filepath.Ext(name))
}

// normalizeForMatch lowercases the string and replaces every non-letter,
// non-digit character with a space, then collapses runs of whitespace.
func normalizeForMatch(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || r == ' ' {
			b.WriteRune(r)
		} else {
			b.WriteRune(' ')
		}
	}
	return strings.Join(strings.Fields(b.String()), " ")
}

// wordOverlap returns the Jaccard similarity (0..1) of the word sets
// extracted from two normalised strings.
func wordOverlap(a, b string) float64 {
	wa := strings.Fields(a)
	wb := strings.Fields(b)
	if len(wa) == 0 && len(wb) == 0 {
		return 1
	}
	if len(wa) == 0 || len(wb) == 0 {
		return 0
	}
	setA := make(map[string]bool, len(wa))
	for _, w := range wa {
		setA[w] = true
	}
	setB := make(map[string]bool, len(wb))
	for _, w := range wb {
		setB[w] = true
	}
	intersection := 0
	for w := range setA {
		if setB[w] {
			intersection++
		}
	}
	union := len(setA) + len(setB) - intersection
	if union == 0 {
		return 0
	}
	return float64(intersection) / float64(union)
}
