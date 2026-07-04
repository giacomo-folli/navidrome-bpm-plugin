package playlist

import (
	"context"
	"fmt"
	"math"
	"sort"

	"github.com/giacomo-folli/navidrome-bpm-plugin/internal/api"
	"github.com/giacomo-folli/navidrome-bpm-plugin/internal/worker"
)

type API interface {
	Playlists(ctx context.Context) ([]api.Playlist, error)
	CreatePlaylist(ctx context.Context, name string, songIDs []string) (api.Playlist, error)
	Playlist(ctx context.Context, id string) (api.Playlist, error)
	UpdatePlaylist(ctx context.Context, id string, removeIndexes []int, addSongIDs []string) error
	DeletePlaylist(ctx context.Context, id string) error
	StartScan(ctx context.Context) error
}

type Options struct {
	BucketSize        int
	Minimum           int
	Maximum           int
	DeleteEmpty       bool
	RescanAfterTags   bool
	IncludeOutOfRange bool
}

type Syncer struct {
	api API
	opt Options
}

func NewSyncer(api API, opt Options) *Syncer {
	if opt.BucketSize <= 0 {
		opt.BucketSize = 10
	}
	return &Syncer{api: api, opt: opt}
}

func Bucket(bpm float64, size int) int {
	if size <= 0 {
		size = 10
	}
	return int(math.Round(bpm/float64(size))) * size
}

func Name(bucket int) string {
	return fmt.Sprintf("%03dbpm", bucket)
}

func (s *Syncer) Sync(ctx context.Context, results []worker.Result) error {
	existing, err := s.api.Playlists(ctx)
	if err != nil {
		return err
	}
	byName := map[string]api.Playlist{}
	for _, pl := range existing {
		byName[pl.Name] = pl
	}

	targets := map[int][]string{}
	for _, r := range results {
		bucket := Bucket(r.BPM, s.opt.BucketSize)
		if !s.opt.IncludeOutOfRange && (bucket < s.opt.Minimum || bucket > s.opt.Maximum) {
			continue
		}
		targets[bucket] = append(targets[bucket], r.SongID)
	}
	for _, ids := range targets {
		sort.Strings(ids)
	}

	for bucket := s.opt.Minimum; bucket <= s.opt.Maximum; bucket += s.opt.BucketSize {
		name := Name(bucket)
		want := targets[bucket]
		pl, ok := byName[name]
		if len(want) == 0 {
			if ok && s.opt.DeleteEmpty {
				if err := s.api.DeletePlaylist(ctx, pl.ID); err != nil {
					return err
				}
			}
			continue
		}
		if !ok {
			if _, err := s.api.CreatePlaylist(ctx, name, want); err != nil {
				return err
			}
			continue
		}
		current, err := s.api.Playlist(ctx, pl.ID)
		if err != nil {
			return err
		}
		remove := make([]int, len(current.Entry))
		for i := range current.Entry {
			remove[i] = len(current.Entry) - 1 - i
		}
		if err := s.api.UpdatePlaylist(ctx, current.ID, remove, want); err != nil {
			return err
		}
	}
	if s.opt.RescanAfterTags {
		return s.api.StartScan(ctx)
	}
	return nil
}
