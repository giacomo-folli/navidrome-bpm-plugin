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
	api      API
	musicDir string
	pageSize int
}

func New(api API, musicDir string) *Scanner {
	return &Scanner{api: api, musicDir: musicDir, pageSize: 500}
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
				path := resolvePath(s.musicDir, song.Path)
				if path == "" {
					return nil, fmt.Errorf("song %q has no path; Navidrome must expose paths for local analysis", song.ID)
				}
				info, err := os.Stat(path)
				if err != nil {
					return nil, fmt.Errorf("stat %s: %w", path, err)
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

func resolvePath(musicDir, songPath string) string {
	if songPath == "" {
		return ""
	}
	if filepath.IsAbs(songPath) {
		return filepath.Clean(songPath)
	}
	return filepath.Join(musicDir, songPath)
}
