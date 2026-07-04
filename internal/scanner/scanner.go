package scanner

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

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
	pageSize          int
}

func New(api API, musicDir, navidromeMusicDir string) *Scanner {
	return &Scanner{api: api, musicDir: musicDir, navidromeMusicDir: navidromeMusicDir, pageSize: 500}
}

func (s *Scanner) Tracks(ctx context.Context) ([]Track, error) {
	var tracks []Track
	for offset := 0; ; offset += s.pageSize {
		albums, err := s.api.Albums(ctx, offset, s.pageSize)
		if err != nil {
			return nil, err
		}
		for _, summary := range albums {
			album, err := s.api.Album(ctx, summary.ID)
			if err != nil {
				return nil, err
			}
			for _, song := range album.Songs {
				path := resolvePath(s.musicDir, s.navidromeMusicDir, song.Path)
				if path == "" {
					return nil, fmt.Errorf("song %q has no path; Navidrome must expose paths for local analysis", song.ID)
				}
				info, err := os.Stat(path)
				if err != nil {
					return nil, fmt.Errorf("stat resolved music path %q from Navidrome path %q: %w; set musicDir to the local host library root and navidromeMusicDir to the path Navidrome reports, usually /music", path, song.Path, err)
				}
				tracks = append(tracks, Track{
					ID:       song.ID,
					Title:    song.Title,
					Path:     path,
					Modified: info.ModTime().Unix(),
				})
			}
		}
		if len(albums) < s.pageSize {
			break
		}
	}
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
