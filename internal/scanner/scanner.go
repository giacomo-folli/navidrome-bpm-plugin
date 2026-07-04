package scanner

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"

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
				resolvedPath, info, usedFallback, err := statResolvedPath(path)
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

func statResolvedPath(path string) (string, os.FileInfo, bool, error) {
	info, err := os.Stat(path)
	if err == nil {
		return path, info, false, nil
	}

	fallback := stripTrackPrefixPath(path)
	if fallback == path {
		return path, nil, false, err
	}
	fallbackInfo, fallbackErr := os.Stat(fallback)
	if fallbackErr != nil {
		return path, nil, false, err
	}
	return fallback, fallbackInfo, true, nil
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
